// Thin API client. All server communication goes through here. The web UI authenticates
// with an HttpOnly session cookie, which the browser sends automatically (same origin).

import { forgetNote, keepNote, kept, notePath, readOffline, setOffline, writeOffline } from './offline';
import i18n from '../i18n';

// ApiError is an error answer from the API. code is a stable identifier that the UI
// translates (see errorText in lib/errors.ts); message is the server's English text.
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
  ) {
    super(message);
  }
}

// unreachable says whether a status means the server itself couldn't be reached (a proxy
// answering for it).
const unreachable = (status: number) => status === 502 || status === 503 || status === 504;

// request calls the API. Reads the app keeps offline (see offline.ts) are stored on success
// and answered from the kept copy while the server can't be reached; fallback can answer
// what wasn't kept.
async function request<T>(method: string, path: string, body?: unknown, fallback?: () => Promise<T | undefined>): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const read = method === 'GET' && kept(path);
  const fromCache = async (cause: unknown): Promise<T> => {
    setOffline(true);
    const data = read ? ((await readOffline<T>(path)) ?? (await fallback?.())) : undefined;
    if (data !== undefined) return data;
    if (read) throw new ApiError(0, 'Not available offline', 'offlineMissing');
    throw cause;
  };
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      credentials: 'same-origin',
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch (err) {
    return fromCache(err);
  }
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // Non-JSON error body (e.g. from a proxy); fall through to the status message.
  }
  if (!res.ok) {
    const body = data as { error?: string; code?: string } | null;
    const err = new ApiError(res.status, body?.error || `Error ${res.status}`, body?.code);
    if (unreachable(res.status) && !body?.code) return fromCache(err);
    setOffline(false);
    throw err;
  }
  setOffline(false);
  if (read) {
    const note = /^\/recordings\/[^/?]+$/.test(path);
    void (note ? keepNote(data as Recording) : writeOffline(path, data));
  } else if (method !== 'GET' && isRecording(data)) {
    void keepNote(data);
  }
  return data as T;
}

const isRecording = (v: unknown): v is Recording =>
  !!v && typeof v === 'object' && typeof (v as Recording).id === 'string' && typeof (v as Recording).status === 'string' && 'recordingId' in v;

export type Role = 'admin' | 'user';

export interface Account {
  id: string;
  email: string;
  role: Role;
  language?: string;
  // The IANA time zone task dates and reminders are meant in; the app keeps it in sync
  // with the browser's.
  timeZone?: string;
  // The color scheme ("light", "dark"); absent follows the system.
  appearance?: string;
  // The text size ("xsmall", "small", "large", "xlarge"); absent is the default size.
  fontSize?: string;
}

export interface Theme {
  id: string;
  name: string;
  description: string;
  instructions: string;
  builtIn: boolean;
  customized?: boolean;
}

export interface ThemeInput {
  name: string;
  description: string;
  instructions: string;
}

// Label is a label for notes. Built-in labels (e.g. "task") are named by the UI.
export interface Label {
  id: string;
  name: string;
  color: string;
  builtIn: boolean;
}

export interface LabelInput {
  name: string;
  color: string;
}

// Folder is a folder the user sorts notes into. parentId is the folder it is in; absent at
// the top level.
export interface Folder {
  id: string;
  name: string;
  parentId?: string;
  // Order among the folders in the same place, from 1 up; absent for unordered folders.
  position?: number;
  // What the user may do in the folder: their own, or shared with them as editor or viewer.
  access?: ShareAccess;
  // The folder, or one it is in, is shared.
  shared?: boolean;
  // A folder shared with the user that they can file in their own folders, but not change.
  movable?: boolean;
  // The folder of the paired reMarkable's documents; it can't be deleted while paired.
  remarkable?: boolean;
}

export interface FolderInput {
  name: string;
  parentId?: string;
}

export interface SummaryOptions {
  language?: string;
  model?: string;
  themeId?: string;
}

export interface User {
  id: string;
  email: string;
  role: Role;
  builtIn: boolean;
  usesEnvPassword: boolean;
  pocketConfigured: boolean;
  createdAt: string;
  passwordChangedAt?: string;
}

export interface PocketSettings {
  webhookPath: string;
  webhookSecretConfigured: boolean;
  apiKeyConfigured: boolean;
  apiKeyHint?: string;
}

export interface Device {
  id: string;
  name: string;
  createdAt: string;
  lastSeenAt?: string;
  revokedAt?: string;
}

// DeviceWithToken is returned when a token is issued; the token is not retrievable later.
export interface DeviceWithToken {
  device: Device;
  token: string;
}

export type RecordingStatus =
  | 'remote'
  | 'uploading'
  | 'received'
  | 'stored'
  | 'transcribed'
  | 'summarized'
  | 'failed';

// NoteType is the kind of note: an audio recording (transcribed and summarized), a text
// note written in the editor, whose title and text live in summary, a document from the
// reMarkable cloud, whose text read from the pages is its transcript, or a kanban board of
// other notes, whose title lives in summary.
export type NoteType = 'audio' | 'text' | 'document' | 'board';

// TabletCopy is a text note's EPUB copy on the owner's reMarkable.
export interface TabletCopy {
  documentId: string;
  removed?: boolean;
  sentAt?: string;
  // Why the last send failed; it is tried again.
  error?: string;
}

// BoardScope selects the notes a board shows: those in a folder (id '' is the top level),
// with a label, or matching a saved filter. An empty kind shows none.
export interface BoardScope {
  kind: '' | 'folder' | 'label' | 'filter';
  id: string;
}

// SavedFilter is a named search in the filter language (see lib/filterQuery.ts); pinned ones
// are shown in the notes list.
export interface SavedFilter {
  id: string;
  name: string;
  query: string;
  pinned: boolean;
}

export interface SavedFilterInput {
  name: string;
  query: string;
  pinned: boolean;
}

// TimeEntry is time spent on a note. The running timer has no end; until is when a focus
// session stops by itself. seconds is how long it lasted (a running one up to now).
export interface TimeEntry {
  id: string;
  noteId: string;
  noteTitle: string;
  noteNumber?: number;
  start: string;
  end?: string;
  until?: string;
  seconds: number;
}

// CalendarSettings is the state of the user's calendar feed; feedPath is only returned
// right after a link was made.
export interface CalendarSettings {
  enabled: boolean;
  createdAt?: string;
  feedPath?: string;
}

// McpSettings is the state of the user's MCP server access; token is only returned right
// after it was made.
export interface McpSettings {
  enabled: boolean;
  createdAt?: string;
  token?: string;
}

// McpApp is an AI assistant the user connected to the MCP server through OAuth.
export interface McpApp {
  id: string;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
}

// OAuthRequest holds the parameters of an OAuth authorization request, named as in OAuth.
export type OAuthRequest = Record<string, string>;

// OAuthAuthorization says which assistant asks for access, or (redirectTo) where the browser
// goes back to when the request can't be granted.
export interface OAuthAuthorization {
  clientName?: string;
  redirectUri?: string;
  redirectTo?: string;
}

// BoardColumn is a column of a board with the IDs of the notes put into it, in order.
export interface BoardColumn {
  id: string;
  name: string;
  notes?: string[];
}

// Board is a board note's setup. Notes in the scope that are in no column are shown in the
// first one.
export interface Board {
  scope: BoardScope;
  columns: BoardColumn[];
}

// RepeatUnit is the step of a recurring task; "weekday" is Monday to Friday.
export type RepeatUnit = 'day' | 'weekday' | 'week' | 'month' | 'year';

// Repeat makes a task recurring: every `every` units, on the given weekdays (0 = Sunday)
// for weekly ones, on monthDay for monthly ones.
export interface Repeat {
  every: number;
  unit: RepeatUnit;
  weekdays?: number[];
  monthDay?: number;
}

// Due is when a task is due, in the user's time zone: a date (YYYY-MM-DD), optionally a
// time (HH:MM), how it repeats, and how many minutes before it (for a day without a time:
// before 9:00) to remind; no remind sends no reminder.
export interface Due {
  date: string;
  time?: string;
  repeat?: Repeat;
  remind?: number;
}

// Priority ranks a task: 1 is the most urgent, 3 the least, 0 none.
export type Priority = 0 | 1 | 2 | 3;

// TaskFields make a new note a task.
export interface TaskFields {
  task?: boolean;
  due?: Due;
  priority?: Priority;
}

// ActionItem is a follow-up the AI found in a conversation, offered as a task.
export interface ActionItem {
  id: string;
  text: string;
  owner?: string;
  due?: string;
  taskId?: string;
  dismissed?: boolean;
}

// PushDevice is a browser that receives the user's notifications.
export interface PushDevice {
  id: string;
  userAgent?: string;
  createdAt: string;
}

export interface NotificationStatus {
  available: boolean;
  publicKey?: string;
  devices: PushDevice[];
  // listening counts the desktop apps that receive notifications over a live connection.
  listening: number;
}

// RECORDINGS_LIMIT is how many notes the list loads.
export const RECORDINGS_LIMIT = 200;

// TRASH_DAYS is how long notes stay in the trash before they are deleted for good.
export const TRASH_DAYS = 14;

export interface Attachment {
  id: string;
  name: string;
  contentType: string;
  size: number;
}

export interface Recording {
  id: string;
  deviceId: string;
  // Absent for audio recordings.
  type?: 'text' | 'document' | 'board';
  // Absent for device uploads; "upload" for files uploaded in the browser (audio, photos,
  // PDFs), "recorder" for voice memos recorded in the app, "briefing" for briefings.
  source?: 'pocket' | 'upload' | 'recorder' | 'remarkable' | 'briefing';
  title?: string;
  recordingId: string;
  // The media type of a file fetched or uploaded (e.g. "image/jpeg" for an uploaded photo).
  sourceContentType?: string;
  // The note's number among the user's notes; "#12" in a note's text links to note 12.
  number?: number;
  status: RecordingStatus;
  size: number;
  recordedAt?: string;
  createdAt: string;
  updatedAt?: string;
  format?: { sampleRate: number; channels: number; bitsPerSample: number; durationMs: number };
  audio?: { key: string; contentType: string; size: number };
  // A document's PDF (notebooks are rendered to one) or EPUB, and its number of pages.
  file?: { key: string; contentType: string; size: number };
  pages?: number;
  // Files attached to the note, shown in the sidebar.
  attachments?: Attachment[];
  transcript?: { text: string; model: string; createdAt: string };
  summary?: {
    title: string;
    markdown?: string;
    model: string;
    language?: string;
    themeId?: string;
    themeName?: string;
    editedAt?: string;
    // Follow-ups found in the conversation; left out in the notes list.
    actionItems?: ActionItem[];
    // Names the AI recognized for the transcript's speaker labels, offered for renaming.
    speakers?: SpeakerName[];
    createdAt: string;
  };
  summaryOptions?: SummaryOptions;
  highlights?: { offsetMs: number; at?: string }[];
  // IDs of the note's labels; done is the check mark of a note labeled "task".
  labels?: string[];
  done?: boolean;
  // When the task was last checked off.
  doneAt?: string;
  // A task's due date and priority, and when its next reminder is sent.
  due?: Due;
  priority?: Priority;
  // assigneeId is the user a task is assigned to (the owner or someone the note is shared with).
  assigneeId?: string;
  remindAt?: string;
  // How many minutes a task is expected to take, and the time logged on it.
  estimate?: number;
  trackedSeconds?: number;
  // The folder the note is in; absent at the top level and for sub-notes.
  folderId?: string;
  // The note this one is a sub-note of; a sub-note is shown under it, wherever it is.
  parentId?: string;
  // Order among the notes in the same place (folder or parent note), from 1 up; absent for
  // unordered notes, which follow by title.
  position?: number;
  // A board's scope and columns.
  board?: Board;
  // A text note's copy on the owner's reMarkable, sent while the note is in the
  // reMarkable folder; removed once the note left it (the copy is in the tablet's trash).
  tablet?: TabletCopy;
  // When the note was moved to the trash; it is deleted for good TRASH_DAYS later.
  deletedAt?: string;
  lastError?: string;
  // The note's owner, and who made it when that was someone else (in a shared note).
  ownerId?: string;
  createdBy?: string;
  // What the user may do with the note, and whether it is shared with anyone. A shared
  // note shows the user's own folder, labels, order and reminder.
  access?: ShareAccess;
  shared?: boolean;
  // version counts every change of the note; revision the changes of its title and text,
  // sent back when editing them so that someone else's edit is never undone.
  version?: number;
  revision?: number;
}

// SpeakerName names the speaker with a label ("Speaker 1") in a transcript.
export interface SpeakerName {
  label: string;
  name: string;
}

// AskTurn is an earlier question and its answer, sent along with a follow-up question.
export interface AskTurn {
  question: string;
  answer: string;
}

// AskSource is a note an answer cites as [ref]; quote is the passage it draws on, offsetMs
// where that is said in a recording.
export interface AskSource {
  ref: number;
  id: string;
  number?: number;
  title: string;
  type: 'audio' | 'text' | 'document';
  date: string;
  quote?: string;
  offsetMs?: number;
}

// AskAnswer is the answer to a question about the notes, in Markdown citing its sources as [1].
export interface AskAnswer {
  answer: string;
  sources: AskSource[];
  model: string;
}

// BriefingSection is a part of the daily briefing besides the tasks due today.
export type BriefingSection = 'overdue' | 'upcoming' | 'new' | 'digest' | 'actionItems';
export const BRIEFING_SECTIONS: BriefingSection[] = ['overdue', 'upcoming', 'new', 'digest', 'actionItems'];

// BriefingSettings say when the daily briefing and the weekly review are made: at time
// (HH:MM, in the user's time zone), the review on weeklyDay (0 is Sunday); whether the daily
// briefing is announced, what it shows, and how many days back it looks for action items.
export interface BriefingSettings {
  daily: boolean;
  weekly: boolean;
  time: string;
  weeklyDay: number;
  notify: boolean;
  sections: BriefingSection[];
  actionItemDays: number;
}

// DailyBriefing is today's daily briefing, shown on the home page; only off is set when the
// user turned it off.
export interface DailyBriefing {
  off?: boolean;
  day?: string;
  title?: string;
  markdown?: string;
  summary?: string;
  language?: string;
  madeAt?: string;
}

// ShareRole is what a user a note is shared with may do: read it, or also change it.
export type ShareRole = 'viewer' | 'editor';
export type ShareAccess = ShareRole | 'owner';

export interface ShareUser {
  userId: string;
  email: string;
  role: ShareAccess;
  // Shared through a note this one is under; changed there.
  inherited?: boolean;
}

// Sharing says who a note is shared with.
export interface Sharing {
  owner: ShareUser;
  members: ShareUser[];
  access: ShareAccess;
  // reporter is the user who made the note (notes only).
  reporter?: ShareUser;
}

// NoteEvent is a change of a note the user sees, sent over GET /me/events.
export interface NoteEvent {
  type: 'note' | 'reload';
  id?: string;
  version?: number;
}

export interface OpenRouterSettings {
  apiKeyConfigured: boolean;
  apiKeyHint?: string;
  transcriptionModel: string;
  summaryModel: string;
  // Reads document pages; empty uses the transcription model.
  documentModel: string;
  updatedAt?: string;
}

// RemarkableSettings is the user's link to the reMarkable cloud.
export interface RemarkableSettings {
  paired: boolean;
  pairedAt?: string;
  // The top-level folder whose documents are imported.
  folder: string;
  // Where to get a one-time code for pairing.
  connectUrl: string;
  lastPullAt?: string;
  lastError?: string;
  lastResult?: { documents: number; imported: number; updated: number; ignored?: number };
  // Names of documents that aren't imported (compared without case).
  ignoredNames: string[];
}

export interface ModelOption {
  id: string;
  name: string;
  contextLength: number;
  promptPrice: string;
  completionPrice: string;
  audioPrice?: string;
}

// UploadOptions describe a voice memo recorded in the app: when it started and the moments
// marked while recording (milliseconds from the start).
export interface UploadOptions {
  recorder?: boolean;
  recordedAt?: Date;
  highlights?: number[];
}

// uploadNoteImage stores a picture for a note's text and returns the URL to show it from.
async function uploadNoteImage(noteId: string, file: Blob): Promise<{ id: string; url: string }> {
  const res = await fetch(`/api/v1/recordings/${encodeURIComponent(noteId)}/images`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    credentials: 'same-origin',
    body: file,
  });
  const text = await res.text();
  let data: { error?: string; code?: string; id?: string; url?: string } | null = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // fall through
  }
  if (!res.ok || !data?.url) throw new ApiError(res.status, data?.error || `Error ${res.status}`, data?.code);
  return { id: data.id ?? '', url: data.url };
}

export interface BackupSummary {
  createdAt: string;
  collections: Record<string, number>;
  objects: number;
  objectBytes: number;
}

// restoreBackup replaces all data with the contents of a backup file.
function restoreBackup(file: File): Promise<BackupSummary> {
  return restoreFrom('/api/v1/admin/restore?confirm=replace-all-data', file);
}

// restorePersonalBackup replaces the user's own notes and content with the contents of a
// personal backup file.
function restorePersonalBackup(file: File): Promise<BackupSummary> {
  return restoreFrom('/api/v1/me/restore?confirm=replace-my-data', file);
}

async function restoreFrom(url: string, file: File): Promise<BackupSummary> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/zip' },
    credentials: 'same-origin',
    body: file,
  });
  const text = await res.text();
  let data: (BackupSummary & { error?: string; code?: string }) | null = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // fall through
  }
  if (!res.ok || !data) throw new ApiError(res.status, data?.error || `Error ${res.status}`, data?.code);
  return data;
}

// uploadAttachment attaches a file to a note and reports progress (0..1).
function uploadAttachment(noteId: string, file: File, onProgress: (fraction: number) => void): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/v1/recordings/${encodeURIComponent(noteId)}/attachments?name=${encodeURIComponent(file.name)}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    xhr.onload = () => {
      let data: (Attachment & { error?: string; code?: string }) | null = null;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        // fall through
      }
      if (xhr.status >= 200 && xhr.status < 300 && data?.id) resolve(data);
      else reject(new ApiError(xhr.status, data?.error || `Error ${xhr.status}`, data?.code));
    };
    xhr.onerror = () => reject(new ApiError(0, 'Network error'));
    xhr.send(file);
  });
}

// uploadRecording sends a file (audio, a photo or a PDF) as the request body and reports
// progress (0..1). It uses XMLHttpRequest because fetch can't report upload progress.
function uploadRecording(file: File, onProgress: (fraction: number) => void, opts: UploadOptions = {}): Promise<Recording> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/recordings');
    xhr.withCredentials = true;
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream');
    xhr.setRequestHeader('X-Filename', encodeURIComponent(file.name));
    const recordedAt = opts.recordedAt ?? (file.lastModified ? new Date(file.lastModified) : undefined);
    if (recordedAt) xhr.setRequestHeader('X-Recorded-At', recordedAt.toISOString());
    if (opts.recorder) xhr.setRequestHeader('X-Recorder', '1');
    if (opts.highlights?.length) xhr.setRequestHeader('X-Highlights', opts.highlights.map((ms) => Math.round(ms)).join(','));
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    xhr.onload = () => {
      let data: unknown = null;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        // fall through
      }
      const body = data as { error?: string; code?: string } | null;
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as Recording);
      else reject(new ApiError(xhr.status, body?.error || `Error ${xhr.status}`, body?.code));
    };
    xhr.onerror = () => reject(new ApiError(0, 'Network error during upload', 'network'));
    xhr.send(file);
  });
}

export const api = {
  devices: () => request<Device[]>('GET', '/devices'),
  pocket: () => request<PocketSettings>('GET', '/me/pocket'),
  savePocket: (u: { webhookSecret?: string; apiKey?: string }) => request<PocketSettings>('PUT', '/me/pocket', u),
  remarkable: () => request<RemarkableSettings>('GET', '/me/remarkable'),
  pairRemarkable: (code: string) => request<RemarkableSettings>('POST', '/me/remarkable/pair', { code }),
  unpairRemarkable: () => request<void>('DELETE', '/me/remarkable'),
  setRemarkableIgnoredNames: (ignoredNames: string[]) => request<RemarkableSettings>('PATCH', '/me/remarkable', { ignoredNames }),
  pullRemarkable: () => request<RemarkableSettings>('POST', '/me/remarkable/pull'),
  aiStatus: () => request<{ transcription: boolean; summary: boolean }>('GET', '/ai/status'),
  recordings: () => request<Recording[]>('GET', `/recordings?limit=${RECORDINGS_LIMIT}`),
  recordingByNumber: (n: number) => request<Recording[]>('GET', `/recordings?number=${n}`),
  recording: (id: string) => request<Recording>('GET', notePath(id)),
  // trashNote moves a note to the trash; deleteNote deletes it for good.
  trashNote: (id: string) => request<Recording>('DELETE', `/recordings/${encodeURIComponent(id)}`),
  restoreNote: (id: string) => request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/restore`),
  deleteNote: async (id: string) => {
    await request<void>('DELETE', `/recordings/${encodeURIComponent(id)}?permanent=1`);
    await forgetNote(id);
  },
  trash: () => request<Recording[]>('GET', `/recordings?trash=only&limit=${RECORDINGS_LIMIT}`),
  emptyTrash: () => request<void>('DELETE', '/recordings/trash'),
  // allRecordings loads the notes list with each note's text, for keeping them offline.
  allRecordings: () => request<Recording[]>('GET', `/recordings?limit=${RECORDINGS_LIMIT}&full=1`),
  retranscribe: (id: string) => request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/retranscribe`),
  resummarize: (id: string, opts?: SummaryOptions) =>
    request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/resummarize`, opts),
  // createTextNote creates a note under parentId, or else in folderId (the top level when absent).
  createTextNote: (title: string, markdown: string, parentId?: string, task?: TaskFields, folderId?: string) =>
    request<Recording>('POST', '/recordings/text', { title, markdown, parentId, folderId, ...task }),
  createBoard: (title: string, board: Board, folderId?: string) => request<Recording>('POST', '/recordings/board', { title, board, folderId }),
  reorderNotes: (ids: string[]) => request<void>('PUT', '/recordings/order', { ids }),
  setBoard: (id: string, board: Board) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/board`, board),
  // editSummary saves the title and text; with baseRevision it fails with the code
  // "changed" when someone else edited them since.
  editSummary: (id: string, title: string, markdown: string, baseRevision?: number) =>
    request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/summary`, { title, markdown, baseRevision }),
  sharing: (id: string) => request<Sharing>('GET', `/recordings/${encodeURIComponent(id)}/shares`),
  share: (id: string, email: string, role: ShareRole) => request<Sharing>('POST', `/recordings/${encodeURIComponent(id)}/shares`, { email, role }),
  setShareRole: (id: string, userId: string, role: ShareRole) =>
    request<Sharing>('PUT', `/recordings/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`, { role }),
  // unshare stops sharing the note with a user; for the user themselves (leaving it) the
  // answer is empty.
  unshare: (id: string, userId: string) =>
    request<Sharing | null>('DELETE', `/recordings/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`),
  folderSharing: (id: string) => request<Sharing>('GET', `/folders/${encodeURIComponent(id)}/shares`),
  shareFolder: (id: string, email: string, role: ShareRole) => request<Sharing>('POST', `/folders/${encodeURIComponent(id)}/shares`, { email, role }),
  setFolderShareRole: (id: string, userId: string, role: ShareRole) =>
    request<Sharing>('PUT', `/folders/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`, { role }),
  // unshareFolder stops sharing the folder with a user; for the user themselves (leaving
  // it) the answer is empty.
  unshareFolder: (id: string, userId: string) =>
    request<Sharing | null>('DELETE', `/folders/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`),
  eventsURL: '/api/v1/me/events',
  setNoteLabels: (id: string, labels: string[]) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/labels`, { labels }),
  // final checks off a repeating task for good instead of moving it to its next date.
  setNoteDone: (id: string, done: boolean, final = false) =>
    request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/done`, final ? { done, final } : { done }),
  setNoteDue: (id: string, due: Due | null) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/due`, { due }),
  setNoteAssignee: (id: string, assigneeId: string) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/assignee`, { assigneeId }),
  setNotePriority: (id: string, priority: Priority) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/priority`, { priority }),
  setNoteEstimate: (id: string, minutes: number) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/estimate`, { minutes }),
  filters: () => request<SavedFilter[]>('GET', '/filters'),
  createFilter: (f: SavedFilterInput) => request<SavedFilter>('POST', '/filters', f),
  updateFilter: (id: string, f: SavedFilterInput) => request<SavedFilter>('PUT', `/filters/${encodeURIComponent(id)}`, f),
  deleteFilter: (id: string) => request<void>('DELETE', `/filters/${encodeURIComponent(id)}`),
  timer: () => request<{ timer: TimeEntry | null }>('GET', '/timer'),
  startTimer: (noteId: string, minutes?: number) => request<{ timer: TimeEntry }>('POST', '/timer', { noteId, minutes }),
  stopTimer: () => request<{ stopped: TimeEntry | null }>('DELETE', '/timer'),
  timeEntries: (from: string, to: string) => request<TimeEntry[]>('GET', `/time-entries?from=${from}&to=${to}`),
  addTimeEntry: (noteId: string, start: string, end: string) => request<TimeEntry>('POST', '/time-entries', { noteId, start, end }),
  deleteTimeEntry: (id: string) => request<void>('DELETE', `/time-entries/${encodeURIComponent(id)}`),
  timeExportURL: (from: string, to: string) => `/api/v1/time-entries/export?from=${from}&to=${to}`,
  calendar: () => request<CalendarSettings>('GET', '/me/calendar'),
  enableCalendar: () => request<CalendarSettings>('POST', '/me/calendar'),
  disableCalendar: () => request<void>('DELETE', '/me/calendar'),
  mcp: () => request<McpSettings>('GET', '/me/mcp'),
  enableMcp: () => request<McpSettings>('POST', '/me/mcp'),
  disableMcp: () => request<void>('DELETE', '/me/mcp'),
  mcpApps: () => request<McpApp[]>('GET', '/me/mcp/apps'),
  disconnectMcpApp: (id: string) => request<void>('DELETE', `/me/mcp/apps/${encodeURIComponent(id)}`),
  oauthAuthorization: (params: OAuthRequest) =>
    request<OAuthAuthorization>('GET', `/oauth/authorize?${new URLSearchParams(params).toString()}`),
  oauthDecide: (params: OAuthRequest, approve: boolean) =>
    request<{ redirectTo: string }>('POST', '/oauth/authorize', { ...params, approve }),
  createActionItemTask: (id: string, itemId: string) =>
    request<{ task: Recording; note: Recording }>('POST', `/recordings/${encodeURIComponent(id)}/action-items/${encodeURIComponent(itemId)}/task`),
  renameSpeaker: (id: string, from: string, to: string) =>
    request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/speakers/rename`, { from, to }),
  ask: (question: string, history: AskTurn[] = []) => request<AskAnswer>('POST', '/ask', { question, history }),
  briefing: () => request<BriefingSettings>('GET', '/me/briefing'),
  saveBriefing: (b: BriefingSettings) => request<BriefingSettings>('PUT', '/me/briefing', b),
  // Briefings are written in the language chosen in the settings, else in the one shown.
  makeBriefing: (kind: 'weekly') => request<Recording>('POST', `/me/briefing/run?lang=${i18n.language}`, { kind }),
  todayBriefing: () => request<DailyBriefing>('GET', `/me/briefing/today?lang=${i18n.language}`),
  remakeTodayBriefing: () => request<DailyBriefing>('POST', `/me/briefing/today?lang=${i18n.language}`),
  dismissActionItem: (id: string, itemId: string, dismissed: boolean) =>
    request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/action-items/${encodeURIComponent(itemId)}/dismissed`, { dismissed }),
  notifications: () => request<NotificationStatus>('GET', '/me/notifications'),
  subscribePush: (sub: PushSubscriptionJSON) => request<PushDevice>('POST', '/me/notifications/subscriptions', sub),
  unsubscribePush: (id: string) => request<void>('DELETE', `/me/notifications/subscriptions/${encodeURIComponent(id)}`),
  testNotification: () => request<{ sent: number }>('POST', '/me/notifications/test'),
  setNoteFolder: (id: string, folderId: string) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/folder`, { folderId }),
  setNoteParent: (id: string, parentId: string) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/parent`, { parentId }),
  folders: () => request<Folder[]>('GET', '/folders'),
  createFolder: (f: FolderInput) => request<Folder>('POST', '/folders', f),
  updateFolder: (id: string, f: FolderInput) => request<Folder>('PUT', `/folders/${encodeURIComponent(id)}`, f),
  deleteFolder: (id: string) => request<void>('DELETE', `/folders/${encodeURIComponent(id)}`),
  reorderFolders: (ids: string[]) => request<void>('PUT', '/folders/order', { ids }),
  labels: () => request<Label[]>('GET', '/labels'),
  createLabel: (l: LabelInput) => request<Label>('POST', '/labels', l),
  updateLabel: (id: string, l: LabelInput) => request<Label>('PUT', `/labels/${encodeURIComponent(id)}`, l),
  deleteLabel: (id: string) => request<void>('DELETE', `/labels/${encodeURIComponent(id)}`),
  downloadURL: (id: string, kind: 'summary' | 'transcript') => `/api/v1/recordings/${encodeURIComponent(id)}/${kind}`,
  audioURL: (id: string, download = false) => `/api/v1/recordings/${encodeURIComponent(id)}/audio${download ? '?download=1' : ''}`,
  fileURL: (id: string, download = false) => `/api/v1/recordings/${encodeURIComponent(id)}/file${download ? '?download=1' : ''}`,
  uploadRecording,
  uploadNoteImage,
  uploadAttachment,
  attachmentURL: (noteId: string, id: string) => `/api/v1/recordings/${encodeURIComponent(noteId)}/attachments/${encodeURIComponent(id)}`,
  deleteAttachment: (noteId: string, id: string) =>
    request<Recording>('DELETE', `/recordings/${encodeURIComponent(noteId)}/attachments/${encodeURIComponent(id)}`),
  users: () => request<User[]>('GET', '/admin/users'),
  createUser: (u: { email: string; password: string; role: Role }) => request<User>('POST', '/admin/users', u),
  updateUser: (id: string, u: { email?: string; role?: Role }) => request<User>('PUT', `/admin/users/${encodeURIComponent(id)}`, u),
  setUserPassword: (id: string, password: string) =>
    request<void>('PUT', `/admin/users/${encodeURIComponent(id)}/password`, { password }),
  deleteUser: (id: string) => request<void>('DELETE', `/admin/users/${encodeURIComponent(id)}`),
  backupURL: '/api/v1/admin/backup',
  restoreBackup,
  personalBackupURL: '/api/v1/me/backup',
  restorePersonalBackup,
  openRouterSettings: () => request<OpenRouterSettings>('GET', '/admin/settings/openrouter'),
  saveOpenRouterSettings: (u: { apiKey?: string; transcriptionModel?: string; summaryModel?: string; documentModel?: string }) =>
    request<OpenRouterSettings>('PUT', '/admin/settings/openrouter', u),
  aiModels: () => request<{ transcription: ModelOption[]; summary: ModelOption[]; document: ModelOption[] }>('GET', '/ai/models'),
  aiLanguages: () => request<string[]>('GET', '/ai/languages'),
  themes: () => request<Theme[]>('GET', '/themes'),
  createTheme: (t: ThemeInput) => request<Theme>('POST', '/themes', t),
  updateTheme: (id: string, t: ThemeInput) => request<Theme>('PUT', `/themes/${encodeURIComponent(id)}`, t),
  deleteTheme: (id: string) => request<void>('DELETE', `/themes/${encodeURIComponent(id)}`),
  savePreferences: (p: { language?: string; timeZone?: string; appearance?: string; fontSize?: string }) => request<Account>('PUT', '/me/preferences', p),
  createDevice: (name: string) => request<DeviceWithToken>('POST', '/devices', { name }),
  rotateDeviceToken: (id: string) => request<DeviceWithToken>('POST', `/devices/${encodeURIComponent(id)}/token`),
  removeDevice: (id: string) => request<void>('DELETE', `/devices/${encodeURIComponent(id)}`),
  me: () => request<Account>('GET', '/auth/me'),
  login: (email: string, password: string) => request<Account>('POST', '/auth/login', { email, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PUT', '/auth/password', { currentPassword, newPassword }),
};
