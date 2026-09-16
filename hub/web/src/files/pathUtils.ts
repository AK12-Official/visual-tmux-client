// POSIX path helpers for the file manager. Paths here are always absolute and
// always use "/", because that is what the hub requires; the browser never sees
// a path in any other shape.

/** stripTrailingSlashes removes trailing separators, keeping the root's own. */
function stripTrailingSlashes(path: string): string {
  let end = path.length
  while (end > 1 && path[end - 1] === '/') end--
  return path.slice(0, end)
}

export function isRoot(path: string): boolean {
  return stripTrailingSlashes(path) === '/'
}

export function basename(path: string): string {
  const trimmed = stripTrailingSlashes(path)
  const index = trimmed.lastIndexOf('/')
  return index === -1 ? trimmed : trimmed.slice(index + 1)
}

/**
 * dirname returns the parent directory. The root is its own parent, which is
 * what stops a "go up" action from stepping above the top of the filesystem.
 */
export function dirname(path: string): string {
  const trimmed = stripTrailingSlashes(path)
  const index = trimmed.lastIndexOf('/')
  if (index <= 0) return '/'
  return trimmed.slice(0, index)
}

export function joinPath(parent: string, name: string): string {
  const trimmed = stripTrailingSlashes(parent)
  return trimmed === '/' ? `/${name}` : `${trimmed}/${name}`
}

/** splitPath returns the parent directory and the final name together. */
export function splitPath(path: string): { parent: string; name: string } {
  return { parent: dirname(path), name: basename(path) }
}

// SAFE_PATH_RE matches a path a shell passes through unchanged. Anything else
// needs quoting, because the character would otherwise be expanded, split on, or
// treated as syntax.
const SAFE_PATH_RE = /^[A-Za-z0-9_@%+=:,./-]+$/

/**
 * quoteForShell returns the text to insert into a terminal input line for a
 * path, or null when the path cannot be inserted safely.
 *
 * The result never contains a line terminator: inserting a path must never be
 * able to run a command on its own. A path that contains one cannot be written
 * on a single input line at all -- escaping it into a two-line fragment would
 * defeat the point -- so it is refused and the caller says so rather than
 * inserting something that would execute.
 */
export function quoteForShell(path: string): string | null {
  if (path === '' || /[\n\r]/.test(path)) return null
  if (SAFE_PATH_RE.test(path)) return path
  return `'${path.split("'").join("'\\''")}'`
}
