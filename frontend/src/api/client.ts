// Thin API client. All server communication goes through here. The web UI authenticates
// with an HttpOnly session cookie, which the browser sends automatically (same origin).

import { forgetNote, keepNote, kept, notePath, readOffline, setOffline, writeOffline } from './offline';

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

export interface Recording {
  id: string;
  deviceId: string;
  // Absent for audio recordings.
  type?: 'text' | 'document' | 'board';
  source?: 'pocket' | 'upload' | 'remarkable';
  title?: string;
  recordingId: string;
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
    createdAt: string;
  };
  summaryOptions?: SummaryOptions;
  highlights?: { offsetMs: number; at?: string }[];
  // IDs of the note's labels; done is the check mark of a note labeled "task".
  labels?: string[];
  done?: boolean;
  // A task's due date and priority, and when its next reminder is sent.
  due?: Due;
  priority?: Priority;
  remindAt?: string;
  // How many minutes a task is expected to take, and the time logged on it.
  estimate?: number;
  trackedSeconds?: number;
  // The folder the note is in; absent at the top level and for sub-notes.
  folderId?: string;
  // The note this one is a sub-note of; a sub-note is shown under it, wherever it is.
  parentId?: string;
  // A board's scope and columns.
  board?: Board;
  // When the note was moved to the trash; it is deleted for good TRASH_DAYS later.
  deletedAt?: string;
  lastError?: string;
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
  lastResult?: { documents: number; imported: number; updated: number };
}

export interface ModelOption {
  id: string;
  name: string;
  contextLength: number;
  promptPrice: string;
  completionPrice: string;
  audioPrice?: string;
}

// uploadRecording sends an audio file as the request body and reports progress (0..1). It
// uses XMLHttpRequest because fetch can't report upload progress.
function uploadRecording(file: File, onProgress: (fraction: number) => void): Promise<Recording> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/recordings');
    xhr.withCredentials = true;
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream');
    xhr.setRequestHeader('X-Filename', encodeURIComponent(file.name));
    if (file.lastModified) xhr.setRequestHeader('X-Recorded-At', new Date(file.lastModified).toISOString());
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
  createTextNote: (title: string, markdown: string, parentId?: string, task?: TaskFields) =>
    request<Recording>('POST', '/recordings/text', { title, markdown, parentId, ...task }),
  createBoard: (title: string, board: Board) => request<Recording>('POST', '/recordings/board', { title, board }),
  setBoard: (id: string, board: Board) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/board`, board),
  editSummary: (id: string, title: string, markdown: string) =>
    request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/summary`, { title, markdown }),
  setNoteLabels: (id: string, labels: string[]) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/labels`, { labels }),
  setNoteDone: (id: string, done: boolean) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/done`, { done }),
  setNoteDue: (id: string, due: Due | null) => request<Recording>('PUT', `/recordings/${encodeURIComponent(id)}/due`, { due }),
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
  createActionItemTask: (id: string, itemId: string) =>
    request<{ task: Recording; note: Recording }>('POST', `/recordings/${encodeURIComponent(id)}/action-items/${encodeURIComponent(itemId)}/task`),
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
  labels: () => request<Label[]>('GET', '/labels'),
  createLabel: (l: LabelInput) => request<Label>('POST', '/labels', l),
  updateLabel: (id: string, l: LabelInput) => request<Label>('PUT', `/labels/${encodeURIComponent(id)}`, l),
  deleteLabel: (id: string) => request<void>('DELETE', `/labels/${encodeURIComponent(id)}`),
  downloadURL: (id: string, kind: 'summary' | 'transcript') => `/api/v1/recordings/${encodeURIComponent(id)}/${kind}`,
  audioURL: (id: string, download = false) => `/api/v1/recordings/${encodeURIComponent(id)}/audio${download ? '?download=1' : ''}`,
  fileURL: (id: string, download = false) => `/api/v1/recordings/${encodeURIComponent(id)}/file${download ? '?download=1' : ''}`,
  uploadRecording,
  users: () => request<User[]>('GET', '/admin/users'),
  createUser: (u: { email: string; password: string; role: Role }) => request<User>('POST', '/admin/users', u),
  updateUser: (id: string, u: { email?: string; role?: Role }) => request<User>('PUT', `/admin/users/${encodeURIComponent(id)}`, u),
  setUserPassword: (id: string, password: string) =>
    request<void>('PUT', `/admin/users/${encodeURIComponent(id)}/password`, { password }),
  deleteUser: (id: string) => request<void>('DELETE', `/admin/users/${encodeURIComponent(id)}`),
  openRouterSettings: () => request<OpenRouterSettings>('GET', '/admin/settings/openrouter'),
  saveOpenRouterSettings: (u: { apiKey?: string; transcriptionModel?: string; summaryModel?: string; documentModel?: string }) =>
    request<OpenRouterSettings>('PUT', '/admin/settings/openrouter', u),
  aiModels: () => request<{ transcription: ModelOption[]; summary: ModelOption[]; document: ModelOption[] }>('GET', '/ai/models'),
  aiLanguages: () => request<string[]>('GET', '/ai/languages'),
  themes: () => request<Theme[]>('GET', '/themes'),
  createTheme: (t: ThemeInput) => request<Theme>('POST', '/themes', t),
  updateTheme: (id: string, t: ThemeInput) => request<Theme>('PUT', `/themes/${encodeURIComponent(id)}`, t),
  deleteTheme: (id: string) => request<void>('DELETE', `/themes/${encodeURIComponent(id)}`),
  savePreferences: (p: { language?: string; timeZone?: string; appearance?: string }) => request<Account>('PUT', '/me/preferences', p),
  createDevice: (name: string) => request<DeviceWithToken>('POST', '/devices', { name }),
  rotateDeviceToken: (id: string) => request<DeviceWithToken>('POST', `/devices/${encodeURIComponent(id)}/token`),
  removeDevice: (id: string) => request<void>('DELETE', `/devices/${encodeURIComponent(id)}`),
  me: () => request<Account>('GET', '/auth/me'),
  login: (email: string, password: string) => request<Account>('POST', '/auth/login', { email, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PUT', '/auth/password', { currentPassword, newPassword }),
};
