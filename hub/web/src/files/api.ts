// API client for the hub's file endpoints. Every call goes through the shared
// bearer token in ../api, and a refusal carries the hub's own error code so a
// caller can match on it -- the save flow depends on telling a lost race
// ("conflict") from every other failure.

import { authFetch, errorText } from '../api'

/** Entry is one child of a directory listing. */
export interface Entry {
  name: string
  is_dir: boolean
  size: number
  mtime: number
}

export interface ListResult {
  path: string
  entries: Entry[]
  truncated: boolean
}

/** FileApiError carries the hub's error code (for example `conflict`). */
export class FileApiError extends Error {
  readonly code: string

  constructor(code: string) {
    super(code)
    this.name = 'FileApiError'
    this.code = code
  }
}

/** isFileApiError reports whether an error is the hub refusing with `code`. */
export function isFileApiError(err: unknown, code: string): boolean {
  return err instanceof FileApiError && err.code === code
}

const BASE = '/api/hosts/local/files'

function withPath(route: string, path: string): string {
  return `${BASE}/${route}?path=${encodeURIComponent(path)}`
}

async function request(target: string, init?: RequestInit): Promise<Response> {
  const res = await authFetch(target, init)
  if (!res.ok) throw new FileApiError(await errorText(res))
  return res
}

export async function listDirectory(path: string): Promise<ListResult> {
  const res = await request(withPath('list', path))
  const data = (await res.json()) as Partial<ListResult> | null
  return {
    path: typeof data?.path === 'string' ? data.path : path,
    entries: Array.isArray(data?.entries) ? data.entries : [],
    truncated: data?.truncated === true,
  }
}

/** mtimeOf reads the modification time the hub reports alongside the bytes. */
function mtimeOf(res: Response): number {
  const value = Number(res.headers.get('X-File-Mtime'))
  return Number.isFinite(value) ? value : 0
}

export interface FileContents {
  text: string
  mtime: number
}

export async function readFile(path: string): Promise<FileContents> {
  const res = await request(withPath('read', path))
  return { text: await res.text(), mtime: mtimeOf(res) }
}

/** readFileBytes fetches the raw bytes, for previewing an image. */
export async function readFileBytes(path: string): Promise<Blob> {
  const res = await request(withPath('read', path))
  return await res.blob()
}

/**
 * downloadFile fetches the bytes for saving. It goes through fetch rather than a
 * plain link because the hub requires the bearer token, which a navigation
 * cannot carry.
 */
export async function downloadFile(path: string): Promise<Blob> {
  const res = await request(withPath('download', path))
  return await res.blob()
}

/**
 * writeFile replaces a file's contents and returns the modification time the hub
 * reports afterwards.
 *
 * Pass the mtime last observed to have the hub refuse a write that lost a race,
 * or null to force the overwrite. The declared size is a byte count, because
 * that is what the hub compares it against.
 */
export async function writeFile(
  path: string,
  body: string,
  expectedMtime: number | null,
): Promise<number> {
  const query = new URLSearchParams({
    path,
    size: String(new TextEncoder().encode(body).length),
  })
  if (expectedMtime !== null) query.set('expected_mtime', String(expectedMtime))

  const res = await request(`${BASE}/write?${query.toString()}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/octet-stream' },
    body,
  })
  const data = (await res.json()) as { mtime?: unknown } | null
  return typeof data?.mtime === 'number' ? data.mtime : 0
}

async function post(route: string, payload: unknown): Promise<void> {
  await request(`${BASE}/${route}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export async function createEntry(path: string, isDir: boolean): Promise<void> {
  await post('create', { path, kind: isDir ? 'dir' : 'file' })
}

export async function renameEntry(path: string, newPath: string): Promise<void> {
  await post('rename', { path, new_path: newPath })
}

export async function deleteEntry(path: string, recursive: boolean): Promise<void> {
  await post('delete', { path, recursive })
}

/** StartDirectory is where the manager opens, and whether the hub substituted it. */
export interface StartDirectory {
  path: string
  /**
   * substituted is true when the session's own directory falls outside a
   * configured boundary, in which case path names one that does not. Only the
   * hub can decide this: a browser has no way to discover which directories the
   * boundary permits.
   */
  substituted: boolean
}

/**
 * fetchWorkingDirectory asks for the directory the manager should open at. It is
 * read once, as a seed: the boundary is decided the same way wherever the user
 * navigates afterwards, so a later pane change never moves an open manager.
 */
export async function fetchWorkingDirectory(session: string): Promise<StartDirectory> {
  const target = `/api/hosts/local/sessions/${encodeURIComponent(session)}/working-directory`
  const res = await request(target)
  const data = (await res.json()) as { path?: unknown; substituted?: unknown } | null
  return {
    path: typeof data?.path === 'string' ? data.path : '',
    substituted: data?.substituted === true,
  }
}
