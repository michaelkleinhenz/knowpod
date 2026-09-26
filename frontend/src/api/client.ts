// Thin API client. All server communication goes through here.

export interface ApiError {
  error: string;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new Error((data as ApiError)?.error || `Error ${res.status}`);
  }
  return data as T;
}

export interface Info {
  service: string;
  apiVersion: string;
}

export const api = {
  info: () => request<Info>('GET', '/info'),
};
