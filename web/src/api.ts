import type { Approval, DeviceInfo } from './protocol/messages';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!res.ok) {
    const msg = (data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string')
      ? (data as { error: string }).error
      : res.statusText || `HTTP ${res.status}`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  me: () => req<{ authenticated: boolean; client_id?: string }>('GET', '/api/me'),
  login: (password: string) => req<{ ok: boolean }>('POST', '/api/login', { password }),
  logout: () => req<{ ok: boolean }>('POST', '/api/logout'),
  devices: () => req<DeviceInfo[]>('GET', '/api/devices'),
  renameDevice: (id: string, name: string) => req<DeviceInfo>('PATCH', `/api/devices/${encodeURIComponent(id)}`, { name }),
  revokeDevice: (id: string) => req<{ ok: boolean }>('DELETE', `/api/devices/${encodeURIComponent(id)}`),
  pairNew: () => req<{ code: string; expires_at: string }>('POST', '/api/pair/new'),
  approvals: (status: 'pending' | 'all' = 'pending') => req<Approval[]>('GET', `/api/approvals?status=${status}`),
  decide: (key: string, decision: 'allow' | 'deny') => req<{ ok: boolean }>('POST', `/api/approvals/${encodeURIComponent(key)}/decide`, { decision }),
  audit: (limit = 100) => req<Array<{ id: number; at: string; actor: string; device_id?: string; sid?: number; action: string; detail?: string }>>('GET', `/api/audit?limit=${limit}`),
  webhookGet: () => req<{ url: string }>('GET', '/api/settings/webhook'),
  webhookPut: (url: string) => req<{ url: string }>('PUT', '/api/settings/webhook', { url }),
};
