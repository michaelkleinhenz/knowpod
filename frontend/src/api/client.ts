// Thin API client. All server communication goes through here. The web UI authenticates
// with an HttpOnly session cookie, which the browser sends automatically (same origin).

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
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
    const message = (data as { error?: string } | null)?.error || `Error ${res.status}`;
    throw new ApiError(res.status, message);
  }
  return data as T;
}

export interface Info {
  service: string;
  apiVersion: string;
}

export interface Account {
  email: string;
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

export const api = {
  info: () => request<Info>('GET', '/info'),
  health,
  openapi: () => request<OpenAPISpec>('GET', '/openapi.json'),
  devices: () => request<Device[]>('GET', '/admin/devices'),
  createDevice: (name: string) => request<DeviceWithToken>('POST', '/admin/devices', { name }),
  rotateDeviceToken: (id: string) => request<DeviceWithToken>('POST', `/admin/devices/${encodeURIComponent(id)}/token`),
  removeDevice: (id: string) => request<void>('DELETE', `/admin/devices/${encodeURIComponent(id)}`),
  me: () => request<Account>('GET', '/auth/me'),
  login: (email: string, password: string) => request<Account>('POST', '/auth/login', { email, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PUT', '/auth/password', { currentPassword, newPassword }),
};
