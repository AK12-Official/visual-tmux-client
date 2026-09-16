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

// The two ends of the control range this refuses: C0 (including NUL, ESC, and
// the line terminators) and DEL.
const FIRST_CONTROL = 0x20
const DELETE_CHARACTER = 0x7f

/**
 * hasControlCharacter reports whether a path carries a byte the terminal would
 * act on rather than display.
 *
 * The shell quoting below protects the shell and says nothing about the terminal
 * layer, which reads the bytes first: an ESC begins an escape sequence, Ctrl-C
 * is VINTR, and so on. Written as a code-point test rather than a regular
 * expression because a character class for this range has to spell out control
 * characters, and a literal one is invisible in the source.
 */
function hasControlCharacter(path: string): boolean {
  for (let i = 0; i < path.length; i++) {
    const code = path.charCodeAt(i)
    if (code < FIRST_CONTROL || code === DELETE_CHARACTER) return true
  }
  return false
}

/**
 * quoteForShell returns the text to insert into a terminal input line for a
 * path, or null when the path cannot be inserted safely.
 *
 * The result never contains a control character, and in particular never a line
 * terminator: inserting a path must never be able to run a command on its own,
 * and the insertion reaches the pane's pty as raw bytes, where a control byte is
 * a keystroke rather than text. A path carrying one cannot be written on a
 * single input line at all -- escaping it across lines would defeat the point,
 * and leaving the byte in would let the terminal act on it -- so it is refused
 * and the caller says so rather than inserting something that would execute.
 */
export function quoteForShell(path: string): string | null {
  if (path === '' || hasControlCharacter(path)) return null
  if (SAFE_PATH_RE.test(path)) return path
  return `'${path.split("'").join("'\\''")}'`
}
