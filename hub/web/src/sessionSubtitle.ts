// Formats the sidebar subtitle for a session's representative pane.
//
// Kept free of any DOM or framework import so it can be unit-tested in this
// project's DOM-less test environment (node --test with no jsdom).

import type { PaneSummary } from './api'

/** Marks the window name when that window is the session's active one. */
const ACTIVE_WINDOW_MARK = '*'

/**
 * formatPaneSubtitle renders the subtitle shown beneath a session's name, or
 * null when there is nothing to show.
 *
 * Precedence, matching the reference implementation:
 *   1. `windowName` (with an asterisk when the window is active), followed by
 *      `: ` and the pane title when a title is present
 *   2. `windowName` alone, when there is no title (still marked with the
 *      asterisk if the window is active)
 *   3. the command currently running in the pane
 *
 * Returning null rather than an empty string lets callers omit the line
 * entirely, so a row never renders an empty element or a placeholder.
 */
export function formatPaneSubtitle(pane: PaneSummary | null | undefined): string | null {
  if (!pane) return null

  const windowName = (pane.window_name ?? '').trim()
  if (windowName) {
    const label = `${windowName}${pane.window_active ? ACTIVE_WINDOW_MARK : ''}`
    const title = (pane.title ?? '').trim()
    return title ? `${label}: ${title}` : label
  }

  const command = (pane.current_command ?? '').trim()
  return command || null
}

/**
 * buildSubtitleMap derives each row's subtitle from its session's pane summary,
 * keyed by session name. Sessions with nothing to show are omitted rather than
 * mapped to an empty string, so a row renders no element at all.
 *
 * The result has a null prototype deliberately. Session names are
 * user-controlled (any name tmux accepts, including `constructor`, `toString`
 * or `__proto__`), and on a plain object literal those resolve to inherited
 * Object.prototype members: a row with no summary would test as truthy and
 * render that member's source as its subtitle, and `__proto__` would silently
 * swallow the assignment so a real subtitle never appeared. App.vue's nameMap()
 * guards the same hazard for the maps it keeps.
 */
export function buildSubtitleMap(
  sessions: ReadonlyArray<{ name: string; pane?: PaneSummary | null }>,
): Record<string, string> {
  const out: Record<string, string> = Object.create(null)
  for (const session of sessions) {
    const text = formatPaneSubtitle(session.pane)
    if (text) out[session.name] = text
  }
  return out
}
