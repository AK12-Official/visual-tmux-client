// Deliberately imports no Vue/i18n/application module: preference hydration must
// happen before their module-level reads. Desktop actions use HTTP, not Wails IPC.
import { setPlatformStorage } from './storage'

export const DESKTOP_TOKEN_KEY = 'vtc:desktop-token'
const BROWSER_TOKEN_KEY = 'visual-tmux-client-token'
type Snapshot = { version: number; revision: number; values: Record<string, string>; warning?: string }
let desktopToken: string | null = null
let queue: Promise<void> = Promise.resolve()
let persistenceError: Error | null = null

export function isDesktop(): boolean { return desktopToken !== null }

export async function desktopRequest<T>(name: string, payload: unknown, fetchFn: typeof fetch = fetch): Promise<T> {
  const response = await fetchFn(`/api/desktop/${name}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', ...(desktopToken ? { Authorization: `Bearer ${desktopToken}` } : {}) },
    body: JSON.stringify(payload),
  })
  if (!response.ok) throw new Error(`Desktop ${name}: ${response.status} ${await response.text()}`)
  return await response.json() as T
}

export function desktopWarning(message: string): void {
  const el = typeof document !== 'undefined' ? document.getElementById('global-error') : null
  if (el) { el.textContent = message; el.style.display = 'block' }
}

export async function flushDesktopPreferences(): Promise<void> {
  await queue
  if (persistenceError) throw persistenceError
}

// Storage remains synchronous to preserve existing locale/font/order consumers.
export function preferenceStorage(snapshot: Snapshot, session: Storage, fetchFn: typeof fetch): Storage {
  let revision = snapshot.revision
  const values = new Map(Object.entries(snapshot.values))
  const persist = () => {
    const captured = Object.fromEntries(values)
    queue = queue.then(async () => {
      const saved = await desktopRequest<Snapshot>('preferences-write', { version: 1, revision, values: captured }, fetchFn)
      revision = saved.revision
      persistenceError = null
    }).catch((error: unknown) => {
      persistenceError = error instanceof Error ? error : new Error(String(error))
      desktopWarning(`Preferences were not saved: ${persistenceError.message}`)
    })
  }
  return {
    get length() { return values.size },
    key: (index) => [...values.keys()][index] ?? null,
    getItem: (key) => key === BROWSER_TOKEN_KEY ? desktopToken : values.get(key) ?? null,
    setItem(key, value) {
      if (key === BROWSER_TOKEN_KEY) { desktopToken = value; session.setItem(DESKTOP_TOKEN_KEY, value); return }
      if (values.get(key) === value) return
      values.set(key, value); persist()
    },
    removeItem(key) {
      if (key === BROWSER_TOKEN_KEY) { desktopToken = null; session.removeItem(DESKTOP_TOKEN_KEY); return }
      if (values.delete(key)) persist()
    },
    clear() { values.clear(); persist() },
  }
}

// Return false for the ordinary web entry. Query/fragment markers are detection
// only: the backend still requires a valid one-use code or current credential.
export async function initializeDesktop(
  location: Pick<Location, 'search' | 'hash' | 'pathname'> = window.location,
  history: Pick<History, 'replaceState'> = window.history,
  session?: Storage,
  fetchFn: typeof fetch = fetch,
): Promise<boolean> {
  if (new URLSearchParams(location.search).get('desktop') !== '1') return false
  const desktopSession = session ?? window.sessionStorage
  const code = new URLSearchParams(location.hash.slice(1)).get('launch')
  history.replaceState(null, '', `${location.pathname}${location.search}`)
  if (code) {
    const result = await desktopRequest<{ token: string }>('bootstrap', { code }, fetchFn)
    if (typeof result.token !== 'string' || !result.token) throw new Error('Invalid desktop authorization response')
    desktopToken = result.token
    desktopSession.setItem(DESKTOP_TOKEN_KEY, desktopToken)
  } else {
    desktopToken = desktopSession.getItem(DESKTOP_TOKEN_KEY)
  }
  if (!desktopToken) throw new Error('Desktop authorization unavailable. Reopen the application to retry.')
  try { window.localStorage.removeItem(BROWSER_TOKEN_KEY) } catch { /* desktop credential remains document-scoped */ }
  let snapshot: Snapshot
  try {
    snapshot = await desktopRequest<Snapshot>('preferences-read', {}, fetchFn)
  } catch (error) {
    throw new Error('Desktop initialization failed. Use Application > Reload workspace or reopen the application to retry.', { cause: error })
  }
  if (snapshot.version !== 1 || !Number.isSafeInteger(snapshot.revision) || !snapshot.values ||
      typeof snapshot.values !== 'object' || Array.isArray(snapshot.values) || Object.values(snapshot.values).some(v => typeof v !== 'string')) {
    throw new Error('Invalid desktop preferences response')
  }
  setPlatformStorage(preferenceStorage(snapshot, desktopSession, fetchFn))
  if (snapshot.warning) desktopWarning(snapshot.warning)
  return true
}
