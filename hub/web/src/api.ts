// API client for the visual-tmux-client JSON control plane. Owns the shared bearer
// token (persisted in localStorage) and surfaces authentication failures as
// a dedicated AuthError so the UI can distinguish "token rejected" from
// "network error".

const TOKEN_KEY = 'visual-tmux-client-token'

/** PaneSummary describes the most representative pane of a session. */
export interface PaneSummary {
  window_name: string
  title: string
  current_command: string
  window_active: boolean
}

export interface Session {
  name: string
  windows: number
  attached: number
  created: number
  /** Supplementary; absent when no representative pane could be determined. */
  pane?: PaneSummary
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

/** authFetch attaches the stored bearer token and normalises 401 into AuthError. */
export async function authFetch(path: string, init?: RequestInit): Promise<Response> {
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

export async function errorText(res: Response): Promise<string> {
  try {
    const text = await res.text()
    try {
      const body = JSON.parse(text) as { error?: unknown; message?: unknown } | null
      if (body && typeof body.error === 'string') return body.error
      if (body && typeof body.message === 'string') return body.message
    } catch {
      const trimmed = text.trim()
      if (trimmed) return trimmed.slice(0, 200)
    }
  } catch {
    /* stream read failure */
  }
  return res.statusText ? `${res.status} ${res.statusText}` : `${res.status}`
}

interface SessionsResponse {
  sessions?: Session[]
}

export async function listSessions(hostId = 'local'): Promise<Session[]> {
  const res = await authFetch(`/api/hosts/${encodeURIComponent(hostId)}/sessions`)
  if (!res.ok) throw new Error(await errorText(res))
  const data = (await res.json()) as SessionsResponse | null
  return Array.isArray(data?.sessions) ? data.sessions : []
}

export async function createSession(name?: string, hostId = 'local'): Promise<Session> {
  const trimmed = name?.trim()
  const res = await authFetch(`/api/hosts/${encodeURIComponent(hostId)}/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(trimmed ? { name: trimmed } : {}),
  })
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as Session
}

export async function renameSession(
  oldName: string,
  newName: string,
  hostId = 'local',
): Promise<Session> {
  const res = await authFetch(
    `/api/hosts/${encodeURIComponent(hostId)}/sessions/${encodeURIComponent(oldName)}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: newName }),
    },
  )
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as Session
}

export async function killSession(name: string, hostId = 'local'): Promise<void> {
  const res = await authFetch(
    `/api/hosts/${encodeURIComponent(hostId)}/sessions/${encodeURIComponent(name)}`,
    {
      method: 'DELETE',
    },
  )
  if (!res.ok) throw new Error(await errorText(res))
}

interface TicketResponse {
  ticket?: unknown
}

/** Issue a single-use, 30s, session-bound WebSocket ticket. */
export async function issueTicket(session: string, hostId = 'local'): Promise<string> {
  const res = await authFetch('/api/ws-ticket', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ hostId, session }),
  })
  if (!res.ok) throw new Error(await errorText(res))
  const data = (await res.json()) as TicketResponse | null
  if (!data || typeof data.ticket !== 'string' || !data.ticket) {
    throw new Error('no ticket in response')
  }
  return data.ticket
}

export interface HostItem {
  id: string
  revision: number
  connectionRevision: number
  name: string
  address?: string
  port?: number
  username?: string
  credentialType?: string
  hasCredential: boolean
  trusted: boolean
}

export interface ProbeResult {
  challengeToken: string
  fingerprint: string
  keyType: string
}

export interface CapabilityStatus {
  available: boolean
  error?: string
}

export interface FileCapability {
  enabled: boolean
  boundary?: string
  rootHandleConfinement?: boolean
  atomicReplacement?: boolean
  error?: string
}

export interface HostCapabilities {
  hostId?: string
  revision?: number
  online?: boolean
  ssh?: CapabilityStatus | boolean
  tmux?: CapabilityStatus | boolean
  sftp?: CapabilityStatus | boolean
  files?: FileCapability
  posixRename?: boolean
  error?: string
}

export interface HostCredentials {
  password?: string
  privateKey?: string
  passphrase?: string
}

export interface HostInput {
  id?: string
  revision?: number
  name: string
  address: string
  port: number
  username: string
  credentialType?: 'password' | 'private_key'
  credentials?: HostCredentials
  challengeToken?: string
}

interface HostsResponse {
  hosts?: HostItem[]
}

export async function listHosts(): Promise<HostItem[]> {
  const res = await authFetch('/api/hosts')
  if (!res.ok) throw new Error(await errorText(res))
  const data = (await res.json()) as HostsResponse | null
  return Array.isArray(data?.hosts) ? data.hosts : []
}

export async function getHost(id: string): Promise<HostItem> {
  const res = await authFetch(`/api/hosts/${encodeURIComponent(id)}`)
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as HostItem
}

export async function createHost(input: HostInput): Promise<HostItem> {
  const res = await authFetch('/api/hosts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as HostItem
}

export async function updateHost(
  id: string,
  input: HostInput,
  expectedRevision: number,
): Promise<HostItem> {
  const res = await authFetch(
    `/api/hosts/${encodeURIComponent(id)}?revision=${expectedRevision}`,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...input, revision: expectedRevision }),
    },
  )
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as HostItem
}

export async function deleteHost(id: string, expectedRevision: number): Promise<void> {
  const res = await authFetch(
    `/api/hosts/${encodeURIComponent(id)}?revision=${expectedRevision}`,
    {
      method: 'DELETE',
    },
  )
  if (!res.ok) throw new Error(await errorText(res))
}

export async function probeDraftHost(address: string, port: number): Promise<ProbeResult> {
  const res = await authFetch('/api/hosts/probe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ address, port }),
  })
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as ProbeResult
}

export async function probeExistingHost(id: string): Promise<ProbeResult> {
  const res = await authFetch(`/api/hosts/${encodeURIComponent(id)}/probe`, {
    method: 'POST',
  })
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as ProbeResult
}

export async function confirmHostTrust(id: string, challengeToken: string): Promise<HostItem> {
  const res = await authFetch(`/api/hosts/${encodeURIComponent(id)}/trust`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ challengeToken }),
  })
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as HostItem
}

export async function testHostCapabilities(id: string): Promise<HostCapabilities> {
  const res = await authFetch(`/api/hosts/${encodeURIComponent(id)}/capabilities`)
  if (!res.ok) throw new Error(await errorText(res))
  return (await res.json()) as HostCapabilities
}

