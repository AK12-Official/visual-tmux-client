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

/**
 * Stamp is a modification time the hub reported, at whichever precisions it
 * reported it.
 *
 * Two fields rather than one because they answer different questions. `millis`
 * is a number the client can hold and compare exactly, which is why it is what
 * travels in the JSON contract. `nanos` is the time the filesystem actually
 * recorded, kept as the opaque decimal string the hub sent: it exceeds
 * JavaScript's safe integer range, so parsing it into a number would round it,
 * and a rounded value matches no file -- turning every save into a conflict the
 * user has to confirm with nothing on screen explaining why.
 *
 * It is `nanos` that a save is checked against when the hub sent one, so two
 * edits inside the same millisecond are two edits. `millis` is the fallback for
 * a hub that reported only milliseconds.
 */
export interface Stamp {
  millis: number
  /** nanos is the exact time, or null when the hub reported only milliseconds. */
  nanos: string | null
}

/** stampFromList is the modification time a directory listing reported, which
 * carries milliseconds only. */
export function stampFromList(mtime: number): Stamp {
  return { millis: mtime, nanos: null }
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
  // A safe integer, because this value is echoed back as the observation the next
  // save is compared against, and it only means anything if it survives the round
  // trip in both directions. It goes on the wire as its decimal form, which the
  // hub reads with a 64-bit integer parse; and what comes back is parsed here
  // into a double, which returns every integer unchanged only up to 2^53. A value
  // that fails either bound is refused rather than adopted as an observation, and
  // never reaches the wire.
  if (!Number.isSafeInteger(value)) {
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

/**
 * optionalStamp reads the modification time a write reported.
 *
 * The millisecond value is required, because a hub that did not report one is a
 * hub this client cannot edit against safely; a missing or unparsable value
 * leaves the caller to record the save without a time to compare against.
 *
 * The nanosecond value is optional and is never parsed: it is carried back to
 * the hub exactly as it arrived.
 */
function optionalStamp(rawMtime: unknown, rawNanos: unknown): Stamp | null {
  const millis = optionalMtime(rawMtime)
  if (millis === null) return null
  return { millis, nanos: exactNanos(rawNanos) }
}

/** NANOS is a decimal nanosecond count, which is how the exact modification time
 * travels. It is never parsed -- the value exceeds what a JavaScript number holds
 * exactly, so it is carried back to the hub as the string it arrived as. */
const NANOS = /^-?\d+$/

/** INT64_MIN and INT64_MAX are what the hub parses this value into, so they are
 * what it has to fit in. A run of digits is not enough: one more than a signed
 * 64-bit integer can hold is still all digits. */
const INT64_MIN = -(2n ** 63n)
const INT64_MAX = 2n ** 63n - 1n

/** exactNanos reads an exact modification time, or null when the value is not one
 * this client can carry back.
 *
 * Only a value the hub will be able to parse is adopted. This goes back on the
 * next save, and echoing one it cannot read would make every later save answer
 * invalid-body, with reopening the file as the only way out. Reporting "no exact
 * time" instead costs the finer comparison and nothing else, so an absent,
 * empty, malformed, or out-of-range value all mean the same thing here. */
function exactNanos(raw: unknown): string | null {
  if (typeof raw !== 'string' || !NANOS.test(raw)) return null
  const value = BigInt(raw)
  if (value < INT64_MIN || value > INT64_MAX) return null
  return raw
}

/** stampOf reads the modification time the hub reports alongside the bytes. */
function stampOf(res: Response): Stamp {
  return {
    millis: requireMtime(res.headers.get('X-File-Mtime')),
    // Absent from a hub that predates it, in which case the millisecond value is
    // what the next save is compared against -- weaker, but still a comparison.
    nanos: exactNanos(res.headers.get('X-File-Mtime-Nanos')),
  }
}

export interface FileContents {
  /** text is the decoded contents, empty when the hub reported the file binary. */
  text: string
  stamp: Stamp
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
  let stamp: Stamp
  try {
    stamp = stampOf(res)
  } catch (err) {
    await releaseBody(res)
    throw err
  }
  // A binary file's bytes are not decoded at all: decoding them is what produces
  // replacement characters, and saving that text back would overwrite the
  // original bytes with them.
  if (res.headers.get('X-File-Binary') === '1') {
    await releaseBody(res)
    return { text: '', stamp, binary: true }
  }
  try {
    return { text: await res.text(), stamp, binary: false }
  } catch (err) {
    await releaseBody(res)
    throw err
  }
}

/** headerSize reads the length a response reports, or null when it did not
 * report one this client can use.
 *
 * The absence is checked before the value is turned into a number, because
 * `Number(null)` is 0 -- and zero is a safe, non-negative integer, so a missing
 * header would sail through a numeric test as "nothing to worry about" and the
 * bound below would silently stop applying. Which is the one case it exists for.
 */
function headerSize(res: Response): number | null {
  const header = res.headers.get('X-File-Size')
  if (header === null || header.trim() === '') return null
  const value = Number(header)
  return Number.isSafeInteger(value) && value >= 0 ? value : null
}

/**
 * ImageBytes is what fetching a file for the image preview produced: the bytes,
 * or the length that made them not worth fetching.
 */
export type ImageBytes = { blob: Blob } | { tooLarge: number }

/**
 * readFileBytes fetches the raw bytes, for previewing an image.
 *
 * A file over the bound is refused before its body is read, so nothing enormous
 * crosses the wire only to be discarded. The length that decides is the one the
 * response reports rather than the one a directory listing gave earlier: a file
 * that grew since it was listed is exactly the case this exists for, and the
 * listing cannot know about it.
 */
export async function readFileBytes(path: string, limit: number): Promise<ImageBytes> {
  const res = await request(withPath('read', path))
  const reported = headerSize(res)
  if (reported !== null && reported > limit) {
    await releaseBody(res)
    return { tooLarge: reported }
  }
  // A hub that did not say how long the body is leaves nothing to check before
  // reading it, so the answer is measured instead -- and the length handed back
  // is the one that was really received rather than a guess about what it might
  // have been. It is also the second chance at the bound: a file that grew
  // between the header and the body is caught here.
  const blob = await res.blob()
  if (blob.size > limit) return { tooLarge: blob.size }
  return { blob }
}

/** FileProbe is what the hub says about a file without being asked to send it. */
export interface FileProbe {
  /** binary is the hub's classification of the whole contents. */
  binary: boolean
  /** size is the length the hub would serve. */
  size: number
}

/**
 * probeFile asks the hub what a file is, without fetching it.
 *
 * A file whose *name* says binary, or says image at a size too large to render,
 * is not opened as text -- and the name is a guess. This is the hub's own answer
 * to the question that guess stands in for, so `notes.dat` holding UTF-8 text can
 * be opened in the editor instead of being refused a look on the strength of its
 * extension.
 *
 * It costs one request and no body: the read route answers a HEAD with the same
 * headers and nothing else, and the hub does the classifying it would have done
 * anyway. A caller that goes on to read the file pays for that twice, which is
 * the price of asking before deciding rather than deciding from a name.
 */
export async function probeFile(path: string): Promise<FileProbe> {
  const res = await request(withPath('read', path), { method: 'HEAD' })
  await releaseBody(res)
  return {
    binary: res.headers.get('X-File-Binary') === '1',
    // Zero when the hub did not say. A tab opened from this probe is one whose
    // size is only ever displayed, and the listing's size stands in for it until
    // the file is read for real.
    size: headerSize(res) ?? 0,
  }
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
 * Pass the stamp last observed to have the hub refuse a write that lost a race,
 * or null to force the overwrite. Both precisions of the stamp are sent: the hub
 * compares against the exact one when it has it, which is what keeps two edits
 * inside a single millisecond from looking like no change at all. The declared
 * size is a byte count, because that is what the hub compares it against.
 */
export async function writeFile(
  path: string,
  body: string,
  expected: Stamp | null,
): Promise<Stamp | null> {
  const query = new URLSearchParams({
    path,
    size: String(new TextEncoder().encode(body).length),
  })
  if (expected !== null) {
    query.set('expected_mtime', String(expected.millis))
    // Sent as the string it arrived as, never re-encoded through a number.
    if (expected.nanos !== null) query.set('expected_mtime_nanos', expected.nanos)
  }

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
  const data = (await res.json().catch(() => null)) as
    | { mtime?: unknown; mtime_nanos?: unknown }
    | null
    | undefined
  return optionalStamp(data?.mtime, data?.mtime_nanos)
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
