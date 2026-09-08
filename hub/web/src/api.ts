// API client for the visual-tmux-client JSON control plane. Owns the shared bearer
// token (persisted in localStorage) and surfaces authentication failures as
// a dedicated AuthError so the UI can distinguish "token rejected" from
// "network error".

const TOKEN_KEY = 'visual-tmux-client-token'

export interface Session {
  name: string
  windows: number
  attached: number
  created: number
}

/** AuthError is thrown when the hub rejects the token with a 401. */
export class AuthError extends Error {
  constructor() {
    super('AUTH_FAILED')
    this.name = 'AuthError'
  }
}

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

async function authFetch(path: string, init?: RequestInit): Promise<Response> {
  const token = getToken()
  const headers = new Headers(init?.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const res = await fetch(path, { ...init, headers })
  if (res.status === 401) {
    clearToken()
    throw new AuthError()
  }
  return res
}

async function errorText(res: Response): Promise<string> {
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string') return body.error
  } catch {
    /* non-JSON error body */
  }
  return `${res.status} ${res.statusText}`
}

export async function listSessions(): Promise<Session[]> {
  const res = await authFetch('/api/hosts/local/sessions')
  if (!res.ok) throw new Error(await errorText(res))
  const data = await res.json()
  return data.sessions ?? []
}

export async function createSession(name?: string): Promise<void> {
  const res = await authFetch('/api/hosts/local/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(name ? { name } : {}),
  })
  if (!res.ok) throw new Error(await errorText(res))
}

export async function renameSession(oldName: string, newName: string): Promise<void> {
  const res = await authFetch(`/api/hosts/local/sessions/${encodeURIComponent(oldName)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: newName }),
  })
  if (!res.ok) throw new Error(await errorText(res))
}

export async function killSession(name: string): Promise<void> {
  const res = await authFetch(`/api/hosts/local/sessions/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
  if (!res.ok) throw new Error(await errorText(res))
}

/** Issue a single-use, 30s, session-bound WebSocket ticket. */
export async function issueTicket(session: string): Promise<string> {
  const res = await authFetch('/api/ws-ticket', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ hostId: 'local', session }),
  })
  if (!res.ok) throw new Error(await errorText(res))
  const data = await res.json()
  if (!data.ticket) throw new Error('no ticket in response')
  return data.ticket as string
}
