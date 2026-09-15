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
 *   2. the command currently running in the pane
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
