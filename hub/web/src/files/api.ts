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

/**
 * requireMtime turns a modification time the hub reported into a number.
 *
 * There is deliberately no zero fallback. Zero is a real modification time, so a
 * client that adopted it as "last observed" would send `expected_mtime=0` on the
 * next save -- which never matches the file, turning every save into a conflict
 * the user has to confirm, with nothing on screen explaining why. A hub that does
 * not report one is a hub this client cannot edit against safely, so it says so.
 */
function requireMtime(raw: unknown): number {
  // A number, or a string that is entirely a number -- nothing else. Coercing
  // whatever else arrives is how `false` becomes 0 and `[]` becomes 0, and zero
  // is a real modification time: it would be adopted as an observation nobody
  // made, and every later save would carry an expected time that never matches.
  const value =
    typeof raw === 'number'
      ? raw
      : typeof raw === 'string' && raw.trim() !== ''
        ? Number(raw)
        : Number.NaN
  if (!Number.isFinite(value)) {
    throw new FileApiError('mtime_unavailable')
  }
  return value
}

/**
 * optionalMtime reads a modification time a hub may not have reported.
 *
 * The write path uses this where the read path uses requireMtime. By the time the
 * answer to a write is read, the bytes are already on disk: refusing to parse the
 * time cannot undo that, it only leaves the tab unable to record that it saved --
 * so every later attempt writes the file again and fails in the same way, and the
 * tab can never be closed without a warning. Reporting "no time" instead lets the
 * tab record the save and leaves the next one to be told the file changed.
 */
function optionalMtime(raw: unknown): number | null {
  try {
    return requireMtime(raw)
  } catch {
    return null
  }
}

/** mtimeOf reads the modification time the hub reports alongside the bytes. */
function mtimeOf(res: Response): number {
  return requireMtime(res.headers.get('X-File-Mtime'))
}

export interface FileContents {
  /** text is the decoded contents, empty when the hub reported the file binary. */
  text: string
  mtime: number
  /** binary is the hub's classification, not a guess from the file's name. */
  binary: boolean
}

/** releaseBody drops a response body the caller will not read, so the connection
 * is not held until the response is collected.
 *
 * It never fails the caller: whatever it reports could only replace the reason
 * the caller already has, and on the success path there is no reason to report
 * at all. */
async function releaseBody(res: Response): Promise<void> {
  try {
    await res.body?.cancel()
  } catch {
    /* already consumed or errored; there is nothing left to release */
  }
}

export async function readFile(path: string): Promise<FileContents> {
  const res = await request(withPath('read', path))
  let mtime: number
  try {
    mtime = mtimeOf(res)
  } catch (err) {
    await releaseBody(res)
    throw err
  }
  // A binary file's bytes are not decoded at all: decoding them is what produces
  // replacement characters, and saving that text back would overwrite the
  // original bytes with them.
  if (res.headers.get('X-File-Binary') === '1') {
    await releaseBody(res)
    return { text: '', mtime, binary: true }
  }
  try {
    return { text: await res.text(), mtime, binary: false }
  } catch (err) {
    await releaseBody(res)
    throw err
  }
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
 * reports afterwards, or null when it reported none.
 *
 * Pass the mtime last observed to have the hub refuse a write that lost a race,
 * or null to force the overwrite. The declared size is a byte count, because
 * that is what the hub compares it against.
 */
export async function writeFile(
  path: string,
  body: string,
  expectedMtime: number | null,
): Promise<number | null> {
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
  // Parsed the same way the read path parses its header, so a hub that reports
  // the time as a numeric string is not accepted on one route and rejected on
  // the other -- which would report a write that succeeded as a failure.
  //
  // A body that is not JSON is treated the same way as one without the field: a
  // 2xx means the bytes are on disk, and reporting a parse failure would say the
  // save failed while every retry succeeded on disk and failed the same way.
  const data = (await res.json().catch(() => null)) as { mtime?: number | string | null } | null
  return optionalMtime(data?.mtime)
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
