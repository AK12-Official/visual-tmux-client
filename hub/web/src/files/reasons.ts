// What the hub's wire codes mean, in words a person can act on.
//
// The browser matches on the code, so a code with no entry here is not an error
// the user never sees -- it is one they see as itself: `Could not rename x:
// cross_root_move`. That is why this table lives in a module of its own rather
// than beside the code that uses it, and why reasons.test.ts reads the hub's
// error mapper and holds the two against each other: a list on this side would
// only be a second thing to forget, and the two sides are different languages.
// A generated artifact would hold them together as well, at the cost of a build
// step and a checked-in file for eleven strings.
//
// `mtime_unavailable` is the exception, and the reason the table is not simply
// derived from the hub's: it is raised by this client (files/api.ts,
// requireMtime), not by any response.

export const REASONS: Record<string, string> = {
  path_not_allowed: 'that path is outside the directories this hub may open',
  cross_root_move: 'entries cannot be moved between two configured roots',
  not_found: 'it is no longer there',
  host_not_found: 'that path is not on this machine',
  permission_denied: 'the hub is not allowed to read or write it',
  file_too_large: 'it is larger than the size limit for one file',
  conflict: 'it changed on disk since it was read',
  invalid_path: 'the hub cannot use that as a path',
  invalid_body: 'the request did not match the file it described',
  invalid_request: 'the request was not one the hub could read',
  dir_not_empty: 'the directory still contains something',
  write_failed: 'the hub could not complete the write',
  mtime_unavailable: 'the hub did not say when the file last changed',
}

/**
 * reasonFor explains a refusal, or reports the code itself when it has none.
 *
 * A file over the limit names the limit, which is what the specification asks the
 * browser to report rather than the refusal. `limit` is passed in rather than
 * read from the configuration here, so this stays a pure function of the code:
 * a refusal must still be reportable before the configuration has loaded, which
 * is a caller's problem and not this module's.
 */
export function reasonFor(code: string, limit: number, format: (bytes: number) => string): string {
  if (code === 'file_too_large' && limit > 0) {
    return `it is larger than this hub's ${format(limit)} limit for one file`
  }
  return REASONS[code] ?? code
}
