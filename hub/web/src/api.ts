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
  try {
    localStorage.setItem(TOKEN_KEY, token)
  } catch {
    /* ignore storage quota */
  }
}

export function clearToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* ignore storage quota */
  }
}

// The token travels in an Authorization header, and browsers only accept
// ISO-8859-1 header values. Full-width characters from an IME (or any other
// non-Latin text) can never authenticate — rejecting them with a clear
// message beats entering the app and throwing "String contains non
// ISO-8859-1 code point" on every request.
const HEADER_SAFE_RE = /^[\x20-\x7E\xA0-\xFF]+$/

/** isHeaderSafeToken reports whether a token can be sent in an HTTP header. */
export function isHeaderSafeToken(token: string): boolean {
  return HEADER_SAFE_RE.test(token)
}

async function authFetch(path: string, init?: RequestInit): Promise<Response> {
  const token = getToken()
  if (token !== null && !isHeaderSafeToken(token)) {
    // A stored token that cannot be sent in a header can never authenticate
    // (it may predate input validation). Treat it as rejected so the client
    // returns to the prompt instead of failing on every request.
    clearToken()
    throw new AuthError()
  }
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
    const body = (await res.json()) as { error?: unknown } | null
    if (body && typeof body.error === 'string') return body.error
  } catch {
    /* non-JSON error body */
  }
  return `${res.status} ${res.statusText}`
}

interface SessionsResponse {
  sessions?: Session[]
}

export async function listSessions(): Promise<Session[]> {
  const res = await authFetch('/api/hosts/local/sessions')
  if (!res.ok) throw new Error(await errorText(res))
  const data = (await res.json()) as SessionsResponse | null
  return Array.isArray(data?.sessions) ? data.sessions : []
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

interface TicketResponse {
  ticket?: unknown
}

/** Issue a single-use, 30s, session-bound WebSocket ticket. */
export async function issueTicket(session: string): Promise<string> {
  const res = await authFetch('/api/ws-ticket', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ hostId: 'local', session }),
  })
  if (!res.ok) throw new Error(await errorText(res))
  const data = (await res.json()) as TicketResponse | null
  if (!data || typeof data.ticket !== 'string' || !data.ticket) {
    throw new Error('no ticket in response')
  }
  return data.ticket
}
