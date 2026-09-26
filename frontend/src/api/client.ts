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

export const api = {
  info: () => request<Info>('GET', '/info'),
  me: () => request<Account>('GET', '/auth/me'),
  login: (email: string, password: string) => request<Account>('POST', '/auth/login', { email, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('PUT', '/auth/password', { currentPassword, newPassword }),
};
