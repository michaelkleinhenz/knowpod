// Thin API client. All server communication goes through here. The web UI authenticates
// with an HttpOnly session cookie, which the browser sends automatically (same origin).

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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // Non-JSON error body (e.g. from a proxy); fall through to the status message.
  }
  if (!res.ok) {
    const body = data as { error?: string; code?: string } | null;
    throw new ApiError(res.status, body?.error || `Error ${res.status}`, body?.code);
  }
  return data as T;
}

export interface Info {
  service: string;
  apiVersion: string;
}

export type Role = 'admin' | 'user';

export interface Account {
  id: string;
  email: string;
  role: Role;
  language?: string;
}

export interface Theme {
  id: string;
  name: string;
  description: string;
  instructions: string;
  builtIn: boolean;
}

export interface ThemeInput {
  name: string;
  description: string;
  instructions: string;
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

export interface Recording {
  id: string;
  deviceId: string;
  source?: 'pocket' | 'upload';
  title?: string;
  recordingId: string;
  status: RecordingStatus;
  size: number;
  recordedAt?: string;
  createdAt: string;
  format?: { sampleRate: number; channels: number; bitsPerSample: number; durationMs: number };
  audio?: { key: string; contentType: string; size: number };
  transcript?: { text: string; model: string; createdAt: string };
  summary?: {
    title: string;
    markdown?: string;
    model: string;
    language?: string;
    themeId?: string;
    themeName?: string;
    createdAt: string;
  };
  summaryOptions?: SummaryOptions;
  lastError?: string;
}

export interface OpenRouterSettings {
  apiKeyConfigured: boolean;
  apiKeyHint?: string;
  transcriptionModel: string;
  summaryModel: string;
  updatedAt?: string;
}

export interface ModelOption {
  id: string;
  name: string;
  contextLength: number;
  promptPrice: string;
  completionPrice: string;
  audioPrice?: string;
}

export interface Health {
  status: string;
  service: string;
}

// The subset of an OpenAPI 3 document that the Status page displays.
export interface OpenAPIOperation {
  tags?: string[];
  summary?: string;
  description?: string;
  security?: Record<string, string[]>[];
  parameters?: { name: string; in: string; required?: boolean; description?: string }[];
  requestBody?: { content?: Record<string, unknown> };
  responses?: Record<string, { description?: string }>;
}

export interface OpenAPISpec {
  info: { title: string; version: string; description?: string };
  tags?: { name: string; description?: string }[];
  paths: Record<string, Record<string, OpenAPIOperation | unknown>>;
}

// health reads /healthz, which lives outside /api/v1 and answers 503 with a JSON body when
// the database is unreachable.
async function health(): Promise<Health> {
  const res = await fetch('/healthz', { credentials: 'same-origin' });
  return (await res.json()) as Health;
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
  info: () => request<Info>('GET', '/info'),
  health,
  openapi: () => request<OpenAPISpec>('GET', '/openapi.json'),
  devices: () => request<Device[]>('GET', '/devices'),
  pocket: () => request<PocketSettings>('GET', '/me/pocket'),
  savePocket: (u: { webhookSecret?: string; apiKey?: string }) => request<PocketSettings>('PUT', '/me/pocket', u),
  aiStatus: () => request<{ transcription: boolean; summary: boolean }>('GET', '/ai/status'),
  recordings: () => request<Recording[]>('GET', '/recordings?limit=200'),
  recording: (id: string) => request<Recording>('GET', `/recordings/${encodeURIComponent(id)}`),
  deleteRecording: (id: string) => request<void>('DELETE', `/recordings/${encodeURIComponent(id)}`),
  retranscribe: (id: string) => request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/retranscribe`),
  resummarize: (id: string, opts?: SummaryOptions) =>
    request<Recording>('POST', `/recordings/${encodeURIComponent(id)}/resummarize`, opts),
  audioURL: (id: string, download = false) => `/api/v1/recordings/${encodeURIComponent(id)}/audio${download ? '?download=1' : ''}`,
  uploadRecording,
  users: () => request<User[]>('GET', '/admin/users'),
  createUser: (u: { email: string; password: string; role: Role }) => request<User>('POST', '/admin/users', u),
  updateUser: (id: string, u: { email?: string; role?: Role }) => request<User>('PUT', `/admin/users/${encodeURIComponent(id)}`, u),
  setUserPassword: (id: string, password: string) =>
    request<void>('PUT', `/admin/users/${encodeURIComponent(id)}/password`, { password }),
  deleteUser: (id: string) => request<void>('DELETE', `/admin/users/${encodeURIComponent(id)}`),
  openRouterSettings: () => request<OpenRouterSettings>('GET', '/admin/settings/openrouter'),
  saveOpenRouterSettings: (u: { apiKey?: string; transcriptionModel?: string; summaryModel?: string }) =>
    request<OpenRouterSettings>('PUT', '/admin/settings/openrouter', u),
  aiModels: () => request<{ transcription: ModelOption[]; summary: ModelOption[] }>('GET', '/ai/models'),
  aiLanguages: () => request<string[]>('GET', '/ai/languages'),
  themes: () => request<Theme[]>('GET', '/themes'),
  createTheme: (t: ThemeInput) => request<Theme>('POST', '/themes', t),
  updateTheme: (id: string, t: ThemeInput) => request<Theme>('PUT', `/themes/${encodeURIComponent(id)}`, t),
  deleteTheme: (id: string) => request<void>('DELETE', `/themes/${encodeURIComponent(id)}`),
  savePreferences: (p: { language?: string }) => request<Account>('PUT', '/me/preferences', p),
  createDevice: (name: string) => request<DeviceWithToken>('POST', '/devices', { name }),
  rotateDeviceToken: (id: string) => request<DeviceWithToken>('POST', `/devices/${encodeURIComponent(id)}/token`),
  removeDevice: (id: string) => request<void>('DELETE', `/devices/${encodeURIComponent(id)}`),
  me: () => request<Account>('GET', '/auth/me'),
  login: (email: string, password: string) => request<Account>('POST', '/auth/login', { email, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PUT', '/auth/password', { currentPassword, newPassword }),
};
