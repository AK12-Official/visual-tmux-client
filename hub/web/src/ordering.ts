// Manual session ordering, persisted per browser in localStorage (a hub-side
// preferences API is deliberately out of scope this release). Session names
// are the keys: renames rewrite them in place, kills prune them, and sessions
// never seen before simply append in server order.
import { safeStorageGet, safeStorageSet, safeStorageRemove } from './storage'

export type OrderMode = 'default' | 'manual'

export interface OrderState {
  mode: OrderMode
  /** Display sequence of known session names; see applyOrder. */
  order: string[]
  /** Pinned session names. Pinning is a flag: pinned sessions render as a
   * group ahead of unpinned ones, in their order[] relative order. */
  pinned: string[]
}

const LEGACY_ORDER_KEY = 'vtc:order'
const HOST_ORDER_PREFIX = 'vtc:order:'

function parseOrderState(raw: string | null): OrderState | null {
  if (!raw) return null
  try {
    const v = JSON.parse(raw) as unknown
    if (v && typeof v === 'object') {
      const parsed = v as Partial<OrderState>
      if (
        (parsed.mode === 'default' || parsed.mode === 'manual') &&
        Array.isArray(parsed.order) &&
        Array.isArray(parsed.pinned)
      ) {
        return {
          mode: parsed.mode,
          order: [...new Set(parsed.order.filter((n): n is string => typeof n === 'string' && n.length > 0))],
          pinned: [...new Set(parsed.pinned.filter((n): n is string => typeof n === 'string' && n.length > 0))],
        }
      }
    }
  } catch {
    // Malformed JSON falls back
  }
  return null
}

export function loadOrder(hostId = 'local'): OrderState {
  try {
    const hostKey = HOST_ORDER_PREFIX + hostId
    const hostRaw = safeStorageGet(hostKey)
    if (hostRaw !== null) {
      const parsed = parseOrderState(hostRaw)
      if (parsed) return parsed
      return { mode: 'default', order: [], pinned: [] }
    }

    // One-time migration for legacy pre-multi-host preferences:
    // migrate to local host only, and never apply to remote hosts.
    if (hostId === 'local') {
      const legacyRaw = safeStorageGet(LEGACY_ORDER_KEY)
      if (legacyRaw !== null) {
        const legacyParsed = parseOrderState(legacyRaw)
        if (legacyParsed) {
          saveOrder(legacyParsed, 'local')
          safeStorageRemove(LEGACY_ORDER_KEY)
          return legacyParsed
        }
      }
    }
  } catch {
    // A corrupted entry falls back to defaults rather than breaking the list.
  }
  return { mode: 'default', order: [], pinned: [] }
}

export function saveOrder(state: OrderState, hostId = 'local'): void {
  try {
    const hostKey = HOST_ORDER_PREFIX + hostId
    safeStorageSet(hostKey, JSON.stringify(state))
    if (hostId === 'local') {
      // Clear legacy key to guarantee migration never runs again or overwrites later edits
      safeStorageRemove(LEGACY_ORDER_KEY)
    }
  } catch {
    /* ignore storage quota / restricted storage */
  }
}

/** applyOrder sorts sessions for display in manual mode: pinned sessions
 * first, then the saved order, then unseen sessions appended in the order
 * given. Default mode passes the server's order through unchanged. */
export function applyOrder<T extends { name: string }>(sessions: T[], state: OrderState): T[] {
  if (state.mode !== 'manual') return sessions
  const pos = new Map<string, number>()
  state.order.forEach((n, i) => pos.set(n, i))
  const pinned = new Set(state.pinned)
  const rank = (s: T): [number, number] => [
    pinned.has(s.name) ? 0 : 1,
    pos.has(s.name) ? (pos.get(s.name) as number) : Number.MAX_SAFE_INTEGER,
  ]
  // Array.prototype.sort is stable, so unseen sessions (equal ranks) keep
  // their server order behind the known ones.
  return [...sessions].sort((a, b) => {
    const [pa, oa] = rank(a)
    const [pb, ob] = rank(b)
    return pa - pb || oa - ob
  })
}
