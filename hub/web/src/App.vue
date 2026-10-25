<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import {
  AuthError,
  clearToken,
  createSession,
  getHostSettings,
  getToken,
  isHeaderSafeToken,
  killSession,
  listHosts,
  listSessions,
  renameSession,
  setToken,
  type HostItem,
  type Session,
} from './api'
import { notify, type ToastLevel } from './toasts'
import {
  disposeTerminalSession,
  insertIntoTerminal,
  renameTerminalSession,
  type ConnState,
} from './terminal'
import { getConfig, resolveFontSize } from './config'
import { getPreference, setLocale, onLocaleChange, getLocale, t, type LanguagePreference, type SupportedLocale } from './i18n/index'
import { safeStorageGet, safeStorageSet, safeStorageRemove } from './storage'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'
import ToastStack from './components/ToastStack.vue'
import HelpModal from './components/HelpModal.vue'
import HostManagerModal from './components/HostManagerModal.vue'

// The file manager is loaded when it is first opened. It carries the editor,
// which is by far the largest dependency in the application and is of no use to
// a page that never opens the manager -- keeping it behind this boundary also
// keeps the Markdown parser out of the initial bundle, which is what HelpModal's
// own dynamic import was for.
const FileManagerOverlay = defineAsyncComponent(
  () => import('./components/files/FileManagerOverlay.vue'),
)

const token = ref(getToken() ?? '')
const tokenInput = ref('')
type AuthErrorState = { key: string; params?: Record<string, unknown> } | { raw: string } | null
const authError = ref<AuthErrorState>(null)
const authBusy = ref(false)
const currentLocale = ref<SupportedLocale>(getLocale())

const displayAuthError = computed(() => {
  void currentLocale.value
  if (!authError.value) return ''
  if ('key' in authError.value) {
    return t(authError.value.key, authError.value.params)
  }
  return authError.value.raw
})

function localizedConnState(s: ConnState | 'connecting' | 'disconnected'): string {
  switch (s) {
    case 'connecting':
      return t('terminal.stateConnecting')
    case 'connected':
      return t('terminal.stateConnected')
    case 'reconnecting':
      return t('terminal.stateReconnecting')
    case 'disconnected':
      return t('terminal.stateDisconnected')
    case 'ended':
      return t('terminal.stateEnded')
    default:
      return s
  }
}

const currentPreference = ref<LanguagePreference>(getPreference())
function onLanguageChange(): void {
  setLocale(currentPreference.value)
}

const RECENT_HOST_KEY = 'vtc:recent-host'
const availableHosts = ref<HostItem[]>([])
const currentHostId = ref<string | null>(null)
const activeHostRemoved = ref(false)
const hostDiscoveryLoading = ref(false)
const hostDiscoveryError = ref('')
const hasInitializedWorkspace = ref(false)

const displayHostDiscoveryError = computed(() => {
  void currentLocale.value
  if (!hostDiscoveryError.value) return ''
  return t('hosts.discoveryFailed', { error: hostDiscoveryError.value })
})

const currentHost = computed<HostItem | null>(() => {
  if (!currentHostId.value) return null
  return availableHosts.value.find((h) => h.id === currentHostId.value) ?? null
})
const currentHostName = computed(() => currentHost.value?.name || currentHost.value?.id || '')
const currentHostRevision = computed(() => currentHost.value?.revision ?? 0)
const currentHostConnectionRevision = computed(() => currentHost.value?.connectionRevision ?? 0)

function selectHost(id: string): void {
  currentHostId.value = id
  hasInitializedWorkspace.value = true
  onHostChange()
}

function handleActiveHostRemoved(removedId: string): void {
  if (currentHostId.value === removedId) {
    stopPolling()
    activeHostRemoved.value = true
    if (filesDirty.value) {
      fileManagerOpen.value = true
    }
    // Dropping panels for the removed host so no new input is sent
    for (const key of Object.keys(panelIds.value)) {
      const colon = key.indexOf(':')
      const host = colon >= 0 ? key.slice(0, colon) : 'local'
      const name = colon >= 0 ? key.slice(colon + 1) : key
      if (host === removedId) {
        disposeTerminalSession(name, host)
      }
    }
    const recent = safeStorageGet(RECENT_HOST_KEY)
    if (recent === removedId) {
      safeStorageRemove(RECENT_HOST_KEY)
    }
  }
}

async function loadAvailableHosts(isBootstrap = false): Promise<void> {
  if (!hasToken.value) return
  hostDiscoveryLoading.value = true
  hostDiscoveryError.value = ''
  try {
    const prevConnRev = currentHost.value?.connectionRevision
    const [list, settings] = await Promise.all([
      listHosts(),
      getHostSettings().catch(() => null),
    ])
    availableHosts.value = Array.isArray(list) ? list : []
    hostDiscoveryLoading.value = false
    const nextConnRev = currentHost.value?.connectionRevision

    if (availableHosts.value.length === 0) {
      if (currentHostId.value && !activeHostRemoved.value) {
        handleActiveHostRemoved(currentHostId.value)
      } else if (!activeHostRemoved.value) {
        currentHostId.value = null
      }
      return
    }

    if (isBootstrap || currentHostId.value === null) {
      // 1. Remembered recent host if still registered
      const recent = safeStorageGet(RECENT_HOST_KEY)
      if (recent && availableHosts.value.some((h) => h.id === recent)) {
        selectHost(recent)
        return
      }

      // 2. Registered default host if still registered
      if (
        settings?.defaultHostId &&
        availableHosts.value.some((h) => h.id === settings.defaultHostId)
      ) {
        selectHost(settings.defaultHostId)
        return
      }

      // 3. Otherwise present host selection without guessing
      currentHostId.value = null
      return
    }

    // Active use: check if active host was removed
    if (currentHostId.value && !availableHosts.value.some((h) => h.id === currentHostId.value)) {
      handleActiveHostRemoved(currentHostId.value)
      return
    }

    if (
      prevConnRev !== undefined &&
      nextConnRev !== undefined &&
      prevConnRev !== nextConnRev &&
      currentHostId.value &&
      !activeHostRemoved.value
    ) {
      selected.value = null
      void refresh()
    }
  } catch (err) {
    hostDiscoveryLoading.value = false
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    hostDiscoveryError.value = err instanceof Error ? err.message : String(err)
  }
}

const sessions = ref<Session[]>([])
const listError = ref('')
const listStale = ref(false)
const selected = ref<string | null>(null)
const managerInitialHostId = ref<string | null>(null)
const cachedSessionsByHost = shallowRef<Record<string, Session[]>>(nameMap())

function openHostManagerFor(hostId: string | null): void {
  managerInitialHostId.value = hostId
  hostManagerOpen.value = true
}

// Every map keyed by session name uses this, and a session name is arbitrary
// user text: the hub accepts anything that is valid UTF-8 without `:`, `.`,
// control characters or full-width lookalikes — which includes `constructor`,
// `toString`, `valueOf`, `__proto__` and `hasOwnProperty`. On a plain `{}`
// those names resolve through Object.prototype, so a lookup for a session that
// has no entry returns an inherited function instead of undefined: the
// `=== undefined` panel-identity guard never fires (so the panel key becomes a
// function and a killed session's successor reuses its dead KeepAlive entry),
// `!!activity[name]` is permanently true, the state pill renders a function
// body, and `delete` cannot remove what was never an own property. A
// null-prototype object inherits no names at all.
//
// These are paired with shallowRef, not ref: a deep ref wraps the object in a
// reactive proxy, and that proxy answers `hasOwnProperty` (plus its internal
// `__v_raw` / `__v_isReactive` / `__v_isReadonly` flags) even on a
// null-prototype target — re-opening the same hole for those names. Every
// update below replaces the whole map, so shallow tracking is all that is
// needed.
function nameMap<T>(...sources: Array<Record<string, T> | undefined>): Record<string, T> {
  return Object.assign(Object.create(null) as Record<string, T>, ...sources)
}

const connStates = shallowRef<Record<string, ConnState>>(nameMap())

const hasToken = computed(() => !!token.value)

// --- Terminal panel identity ---
// KeepAlive caches one TerminalView per key, and nothing ever evicts an entry.
// Keying by session name would therefore strand a panel whenever its session is
// renamed: the name changes, the session does not, so the still-live panel
// (xterm instance, WebSocket, and the hub-side tmux client, pty and
// `tmux attach-session` process behind it) stays cached under a name that no
// longer exists while a second attachment mounts beside it. Keying by a stable
// per-session id that renames carry over keeps one panel per session, so a
// rename reuses the existing attachment instead of leaking it.
// Panel keys are scoped by host so same-named sessions on different hosts have
// separate panel identities in KeepAlive.
const panelIds = shallowRef<Record<string, number>>(nameMap())
let nextPanelId = 1

function panelKey(name: string, hostId = currentHostId.value): string {
  return `${hostId}:${name}`
}

function select(name: string): void {
  const key = panelKey(name)
  if (panelIds.value[key] === undefined) {
    panelIds.value = nameMap(panelIds.value, { [key]: nextPanelId++ })
  }
  selected.value = name
}

const selectedPanelId = computed(() =>
  selected.value === null ? null : panelIds.value[panelKey(selected.value)] ?? null,
)

// Whether the selected session still exists server-side, from the same polled
// list that renders the sidebar. Drives the terminal's ended overlay: a
// detached-but-alive session offers Reconnect; a gone one states its end.
const selectedAlive = computed(() =>
  selected.value !== null && sessions.value.some((s) => s.name === selected.value),
)

// --- Terminal header: font size (persisted), fullscreen, close ---

const FONT_SIZE_KEY = 'vtc:font-size'
const termConfig = computed(() => getConfig().web.terminal)

function loadFontSize(): number {
  const cfg = getConfig().web.terminal
  const raw = safeStorageGet(FONT_SIZE_KEY)
  const resolved = resolveFontSize(raw, cfg)
  if (raw !== null && String(resolved) !== raw) {
    safeStorageSet(FONT_SIZE_KEY, String(resolved))
  }
  return resolved
}

const fontSize = ref(loadFontSize())

function setFontSize(px: number): void {
  const cfg = getConfig().web.terminal
  const clamped = Math.min(cfg.max_font_size, Math.max(cfg.min_font_size, px))
  fontSize.value = clamped
  safeStorageSet(FONT_SIZE_KEY, String(clamped))
}

const mainEl = ref<HTMLElement | null>(null)

// Fullscreen targets the main region (header + terminal) so the controls stay
// reachable. Size re-negotiation needs no handler here: the terminal's
// ResizeObserver fires when the region's box changes.
// Both calls return promises that reject when the browser refuses (no user
// activation, a permissions policy, or exiting when not fullscreen); an
// unhandled rejection would trip the global error banner in main.ts.
function toggleFullscreen(): void {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {
      /* already exited */
    })
  } else {
    mainEl.value?.requestFullscreen().catch(() => {
      notify('warning', t('terminal.fullscreenRefusedNotice'))
    })
  }
}

// --- File manager ---
// The overlay is rendered inside the terminal region rather than over the whole
// application, so it cannot cover the sidebar or the toasts -- the notification
// containment fix (#6) established that rule for this app. Its state lives in
// the component and is discarded when it closes, which is why closing the panel
// warns first.
const fileManagerOpen = ref(false)
const filesDirty = ref(false)
const dirtyHostIds = ref<string[]>([])
const fileManagerRef = ref<{ discardHostDrafts: (id: string) => void } | null>(null)

function onDirtyChange(d: boolean, hosts?: string[]) {
  filesDirty.value = d
  dirtyHostIds.value = hosts ?? []
}

function onHostDeleted(id: string) {
  if (currentHostId.value === id) {
    handleActiveHostRemoved(id)
  }
  void loadAvailableHosts(false)
}

// The hub publishes whether it offers a file manager at all. A button that
// always answers "not found" invites a support question, so the entry point is
// left out rather than offered and refused.
const filesAvailable = computed(() => getConfig().files.enabled)

/** closeFileManager drops the overlay and the flag it reported.
 *
 * The flag is cleared here rather than left to the overlay: the watcher that
 * reports it stops when the component unmounts, so a manager closed while dirty
 * would leave `true` behind forever and the next panel close would warn about
 * unsaved changes that no longer exist. */
function closeFileManager(): void {
  fileManagerOpen.value = false
  filesDirty.value = false
  dirtyHostIds.value = []
}

/** toggleFileManager warns before discarding unsaved edits.
 *
 * The overlay's own close button asks for the same confirmation, so this is not
 * the only guard -- but the header button is a second way out, and closing
 * through it used to unmount the manager without a word. */
function toggleFileManager(): void {
  if (!filesAvailable.value) return
  if (!fileManagerOpen.value) {
    fileManagerOpen.value = true
    return
  }
  if (
    filesDirty.value &&
    !window.confirm(t('files.closeManagerUnsavedPrompt'))
  ) {
    return
  }
  closeFileManager()
}

/** insertPathIntoTerminal writes text the file manager prepared. The manager has
 * already quoted it and made sure it carries no line terminator; all that is left
 * is to find a live attachment, and to say so when there is none rather than
 * letting the insertion look like it worked. */
function insertPathIntoTerminal(text: string, sourceHostId = currentHostId.value): void {
  if (!currentHostId.value || sourceHostId !== currentHostId.value) {
    notify(
      'warning',
      t('terminal.crossHostPathRefusal', {
        sourceHost: sourceHostId,
        targetHost: currentHostId.value,
      }),
    )
    return
  }
  if (selected.value === null || !insertIntoTerminal(selected.value, text, currentHostId.value)) {
    notify('warning', t('terminal.noLiveTerminalNotice'))
  }
}

// Close detaches the view, never the session: the terminal keeps streaming in
// the KeepAlive cache, so the session stays alive and can still show activity.
function closePanel(): void {
  if (filesDirty.value) {
    if (!window.confirm(t('files.closeManagerUnsavedPrompt'))) return
  }
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {
      /* already exited */
    })
  }
  closeFileManager()
  selected.value = null
}

// --- Session activity indication ---
// Background terminals keep streaming (KeepAlive), so "which session just
// produced output" is observable for free: each activity event lights the
// session's card (or, for the viewed session, the header dot and the tab
// title) and a decay timer dims it ~2 s after output stops.

const activity = shallowRef<Record<string, boolean>>(nameMap())
const activityTimers = new Map<string, ReturnType<typeof setTimeout>>()

function onActivity(name: string, hostId = currentHostId.value): void {
  const key = panelKey(name, hostId)
  if (!activity.value[key]) {
    activity.value = nameMap(activity.value, { [key]: true })
  }
  const timer = activityTimers.get(key)
  if (timer) clearTimeout(timer)
  activityTimers.set(
    key,
    setTimeout(() => {
      activityTimers.delete(key)
      const next = nameMap(activity.value)
      delete next[key]
      activity.value = next
    }, getConfig().web.activity_decay),
  )
}

const currentHostActivity = computed(() => {
  const result = nameMap<boolean>()
  if (!currentHostId.value) return result
  const prefix = `${currentHostId.value}:`
  for (const [key, val] of Object.entries(activity.value)) {
    if (key.startsWith(prefix) && val) {
      result[key.slice(prefix.length)] = true
    }
  }
  return result
})

const selectedActive = computed(
  () => selected.value !== null && currentHostId.value !== null && !!activity.value[panelKey(selected.value, currentHostId.value)],
)

// The tab-title mark is the only signal visible when the browser tab itself
// is backgrounded — the whole point of the prefix.
const BASE_TITLE = 'Visual Tmux Client'
watch(selectedActive, (live) => {
  document.title = live ? `● ${BASE_TITLE}` : BASE_TITLE
})

// --- Sidebar collapse (persisted) ---

const SIDEBAR_KEY = 'vtc:sidebar-collapsed'
const sidebarCollapsed = ref(safeStorageGet(SIDEBAR_KEY) === '1')

function toggleSidebar(): void {
  sidebarCollapsed.value = !sidebarCollapsed.value
  safeStorageSet(SIDEBAR_KEY, sidebarCollapsed.value ? '1' : '0')
}

// --- In-app help ---

const helpOpen = ref(false)
const hostManagerOpen = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(() => void refresh(), getConfig().web.session_poll_interval)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

let refreshSeq = 0

async function refresh() {
  if (!hasToken.value || !currentHostId.value || activeHostRemoved.value) return
  const seq = ++refreshSeq
  const targetHostId = currentHostId.value
  const targetConnectionRevision = currentHost.value?.connectionRevision
  try {
    const list = await listSessions(targetHostId)
    if (seq !== refreshSeq) return
    if (currentHostId.value !== targetHostId || activeHostRemoved.value) return
    if (
      targetConnectionRevision !== undefined &&
      currentHost.value?.connectionRevision !== undefined &&
      currentHost.value.connectionRevision !== targetConnectionRevision
    ) {
      // Stale response from a superseded host connection revision dropped
      return
    }
    cachedSessionsByHost.value = nameMap(cachedSessionsByHost.value, {
      [targetHostId]: list,
    })
    sessions.value = list
    listError.value = ''
    listStale.value = false
    startPolling()

    // Prune dead panels for sessions that disappeared externally (e.g. killed via CLI).
    const liveNames = new Set(list.map((s) => s.name))
    const deadKeys = new Set<string>()
    for (const key of Object.keys(panelIds.value)) {
      const colon = key.indexOf(':')
      const host = colon >= 0 ? key.slice(0, colon) : 'local'
      const name = colon >= 0 ? key.slice(colon + 1) : key
      if (host === targetHostId && !liveNames.has(name) && selected.value !== name) {
        deadKeys.add(key)
      }
    }
    for (const key of deadKeys) {
      const colon = key.indexOf(':')
      const host = colon >= 0 ? key.slice(0, colon) : 'local'
      const name = colon >= 0 ? key.slice(colon + 1) : key
      dropPanel(name, host)
    }
  } catch (err) {
    if (seq !== refreshSeq) return
    if (currentHostId.value !== targetHostId || activeHostRemoved.value) return
    if (
      targetConnectionRevision !== undefined &&
      currentHost.value?.connectionRevision !== undefined &&
      currentHost.value.connectionRevision !== targetConnectionRevision
    ) {
      return
    }

    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }

    const msg = err instanceof Error ? err.message : String(err)
    listError.value = msg
    listStale.value = true

    // Stop automatic polling on host-level failures to offer host repair
    if (
      msg.startsWith('host_unavailable') ||
      msg.startsWith('host_not_found') ||
      msg.startsWith('host_revision_conflict') ||
      msg.includes('ssh authentication failed') ||
      msg.includes('untrusted_host_key')
    ) {
      stopPolling()
    }
  }
}

function cleanupSessionState() {
  for (const key of Object.keys(panelIds.value)) {
    const colon = key.indexOf(':')
    const host = colon >= 0 ? key.slice(0, colon) : 'local'
    const name = colon >= 0 ? key.slice(colon + 1) : key
    disposeTerminalSession(name, host)
  }
  panelIds.value = nameMap()
  connStates.value = nameMap()
  activity.value = nameMap()
  cachedSessionsByHost.value = nameMap()
  listStale.value = false
  for (const timer of activityTimers.values()) clearTimeout(timer)
  activityTimers.clear()
}

function handleAuthFailure() {
  stopPolling()
  clearToken()
  token.value = ''
  currentHostId.value = null
  availableHosts.value = []
  activeHostRemoved.value = false
  hasInitializedWorkspace.value = false
  // The credential is gone, so nothing could be saved anyway; asking about
  // unsaved edits here would be a question with only one answer.
  closeFileManager()
  selected.value = null
  sessions.value = []
  cleanupSessionState()
  authError.value = { key: 'auth.authFailed' }
}

// The main view opens only after the server has accepted the credential:
// submitting stores the token (authFetch reads it from storage), probes with
// a session listing, and bounces back to this prompt on any failure. A token
// that cannot be sent in an HTTP header (full-width IME characters look
// identical in the password box) is rejected without a round trip.
async function submitToken() {
  const tok = tokenInput.value.trim()
  if (!tok || authBusy.value) return
  if (!isHeaderSafeToken(tok)) {
    authError.value = { key: 'auth.headerUnsafeToken' }
    return
  }
  authBusy.value = true
  authError.value = null
  try {
    setToken(tok)
    token.value = tok
    tokenInput.value = ''
    await loadAvailableHosts(true)
  } catch (err) {
    clearToken()
    token.value = ''
    if (err instanceof AuthError) {
      authError.value = { key: 'auth.authFailed' }
    } else {
      authError.value = { raw: err instanceof Error ? err.message : String(err) }
    }
  } finally {
    authBusy.value = false
  }
}

function logout() {
  stopPolling()
  clearToken()
  token.value = ''
  currentHostId.value = null
  availableHosts.value = []
  activeHostRemoved.value = false
  hasInitializedWorkspace.value = false
  // As with an auth failure: the token is gone, so there is nothing to ask.
  closeFileManager()
  selected.value = null
  sessions.value = []
  cleanupSessionState()
}

function onHostChange(): void {
  if (!currentHostId.value) return
  activeHostRemoved.value = false
  safeStorageSet(RECENT_HOST_KEY, currentHostId.value)
  selected.value = null
  listError.value = ''
  listStale.value = false
  sessions.value = cachedSessionsByHost.value[currentHostId.value] || []
  void refresh()
}

function onSelectHostFromModal(id: string): void {
  hostManagerOpen.value = false
  if (currentHostId.value !== id || activeHostRemoved.value) {
    selectHost(id)
  }
}

function openHostManagerAdd(): void {
  managerInitialHostId.value = null
  hostManagerOpen.value = true
}

function formatSessionError(msg: string, action: 'create' | 'rename' | 'kill'): string {
  if (msg.includes('name_in_use')) {
    return t('sessions.nameInUse')
  }
  if (msg.includes('session_not_found')) {
    return t('sessions.sessionNotFound')
  }
  if (msg.includes('tmux_unavailable')) {
    return t('errors.tmux_unavailable')
  }
  if (msg.includes('host_not_found')) {
    return t('terminal.hostNotFoundNotice')
  }
  if (msg.includes('host_unavailable')) {
    return t('errors.host_unavailable')
  }
  if (msg.includes('host_unsupported')) {
    return t('errors.host_unsupported')
  }
  if (msg.includes('invalid_request')) {
    return t('errors.invalid_request')
  }
  return t(`sessions.${action}FailedWithDiagnostic`, { diagnostic: msg })
}

// One-click creation: the hub assigns the default name (made unique by its
// suffix retry); renaming is how the user personalizes it afterwards.
const creating = ref(false)
async function onCreate() {
  if (creating.value || !currentHostId.value || activeHostRemoved.value) return
  const targetHostId = currentHostId.value
  const targetHostName = currentHostName.value
  const targetConnectionRev = currentHost.value?.connectionRevision
  creating.value = true
  try {
    const sess = await createSession(undefined, targetHostId, targetConnectionRev)
    if (
      currentHostId.value === targetHostId &&
      currentHost.value?.connectionRevision === targetConnectionRev &&
      !activeHostRemoved.value
    ) {
      await refresh()
      if (
        currentHostId.value === targetHostId &&
        currentHost.value?.connectionRevision === targetConnectionRev &&
        !activeHostRemoved.value
      ) {
        select(sess.name)
      }
    }
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('host_revision_conflict')) {
      notify('error', t('sessions.createConflict', { host: targetHostName }))
      return
    }
    if (msg.includes('outcome_unknown')) {
      notify('warning', t('sessions.createUncertain', { host: targetHostName }))
      return
    }
    notify('error', formatSessionError(msg, 'create'))
  } finally {
    creating.value = false
  }
}

async function onRename(oldName: string, newName: string) {
  if (!currentHostId.value || activeHostRemoved.value) return
  const targetHostId = currentHostId.value
  const targetHostName = currentHostName.value
  const targetConnectionRev = currentHost.value?.connectionRevision
  try {
    await renameSession(oldName, newName, targetHostId, targetConnectionRev)
    if (
      currentHostId.value === targetHostId &&
      currentHost.value?.connectionRevision === targetConnectionRev &&
      !activeHostRemoved.value
    ) {
      // Carry the panel identity across the rename, so the live attachment for
      // this session is reused rather than stranded in the KeepAlive cache under
      // a name that no longer exists (see panelIds).
      const oldKey = panelKey(oldName, targetHostId)
      const newKey = panelKey(newName, targetHostId)
      const id = panelIds.value[oldKey]
      if (id !== undefined) {
        const next = nameMap(panelIds.value, { [newKey]: id })
        delete next[oldKey]
        panelIds.value = next
      }
      if (connStates.value[oldKey] !== undefined) {
        const nextStates = nameMap(connStates.value, { [newKey]: connStates.value[oldKey] })
        delete nextStates[oldKey]
        connStates.value = nextStates
      }
      if (activity.value[oldKey] !== undefined) {
        const nextAct = nameMap(activity.value, { [newKey]: activity.value[oldKey] })
        delete nextAct[oldKey]
        activity.value = nextAct
      }
      const timer = activityTimers.get(oldKey)
      if (timer) {
        clearTimeout(timer)
        activityTimers.delete(oldKey)
        activityTimers.set(
          newKey,
          setTimeout(() => {
            activityTimers.delete(newKey)
            const next = nameMap(activity.value)
            delete next[newKey]
            activity.value = next
          }, getConfig().web.activity_decay),
        )
      }
      // Follow the rename: `selected` is the viewed terminal's identity — it
      // names the ws ticket and the header. Left on the old name, the polled
      // list no longer contains it, so a live session reads as ended and every
      // reconnect asks for a ticket the hub answers with session_not_found.
      if (currentHostId.value === targetHostId && selected.value === oldName) {
        selected.value = newName
      }
      // Retarget the attachment itself. For the viewed panel the `session` prop
      // change would reach it, but a panel parked in the KeepAlive cache never
      // re-renders, so no prop can: without this its terminal keeps the old name
      // and its next reconnect asks the hub for a session that no longer exists.
      renameTerminalSession(oldName, newName, targetHostId)
      await refresh()
    }
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('host_revision_conflict')) {
      notify('error', t('sessions.renameConflict', { host: targetHostName }))
      return
    }
    if (msg.includes('outcome_unknown')) {
      notify('warning', t('sessions.renameUncertain', { host: targetHostName }))
      return
    }
    notify('error', formatSessionError(msg, 'rename'))
  }
}

// Killing a session retires its panel identity, so a later session that
// happens to reuse the name gets a fresh panel rather than the dead one still
// sitting in the KeepAlive cache (which would greet a brand-new session with an
// "ended" overlay). Retiring the key makes the cached panel unreachable but not
// gone — KeepAlive only unmounts on eviction, and nothing evicts here — so its
// terminal is disposed explicitly, or its xterm instance, scrollback and
// ResizeObserver would be retained for the life of the page.
function dropPanel(name: string, hostId = currentHostId.value ?? 'local'): void {
  const key = panelKey(name, hostId)
  if (panelIds.value[key] !== undefined) {
    const next = nameMap(panelIds.value)
    delete next[key]
    panelIds.value = next
  }
  if (connStates.value[key] !== undefined) {
    const nextStates = nameMap(connStates.value)
    delete nextStates[key]
    connStates.value = nextStates
  }
  if (activity.value[key] !== undefined) {
    const nextAct = nameMap(activity.value)
    delete nextAct[key]
    activity.value = nextAct
  }
  const timer = activityTimers.get(key)
  if (timer) {
    clearTimeout(timer)
    activityTimers.delete(key)
  }
  disposeTerminalSession(name, hostId)
}

async function onKill(name: string) {
  if (!currentHostId.value || activeHostRemoved.value) return
  const targetHostId = currentHostId.value
  const targetHostName = currentHostName.value
  const targetConnectionRev = currentHost.value?.connectionRevision
  try {
    await killSession(name, targetHostId, targetConnectionRev)
    dropPanel(name, targetHostId)
    if (
      currentHostId.value === targetHostId &&
      currentHost.value?.connectionRevision === targetConnectionRev &&
      !activeHostRemoved.value
    ) {
      if (selected.value === name) {
        selected.value = null
      }
      await refresh()
    }
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('host_revision_conflict')) {
      notify('error', t('sessions.killConflict', { host: targetHostName }))
      return
    }
    if (msg.includes('outcome_unknown')) {
      notify('warning', t('sessions.killUncertain', { host: targetHostName }))
      return
    }
    notify('error', formatSessionError(msg, 'kill'))
  }
}

// Bulk kill issues one DELETE per session sequentially and names the sessions
// that failed, so a mid-batch failure never hides behind the successes.
async function onBulkKill(names: string[]) {
  if (!currentHostId.value || activeHostRemoved.value) return
  const targetHostId = currentHostId.value
  const targetHostName = currentHostName.value
  const targetConnectionRev = currentHost.value?.connectionRevision
  const failed: string[] = []
  for (const name of names) {
    if (activeHostRemoved.value) return
    const currentConnRev = availableHosts.value.find((h) => h.id === targetHostId)?.connectionRevision
    if (
      targetConnectionRev !== undefined &&
      currentConnRev !== undefined &&
      currentConnRev !== targetConnectionRev
    ) {
      notify('warning', t('sessions.bulkKillHostConflict'))
      break
    }
    try {
      await killSession(name, targetHostId, targetConnectionRev)
      if (
        currentHostId.value === targetHostId &&
        currentHost.value?.connectionRevision === targetConnectionRev &&
        !activeHostRemoved.value
      ) {
        if (selected.value === name) {
          selected.value = null
        }
      }
      dropPanel(name, targetHostId)
    } catch (err) {
      if (err instanceof AuthError) {
        handleAuthFailure()
        return
      }
      const msg = err instanceof Error ? err.message : String(err)
      if (msg.includes('host_revision_conflict')) {
        notify('error', t('sessions.bulkKillItemConflict', { name, host: targetHostName }))
        break
      }
      if (msg.includes('outcome_unknown')) {
        notify('warning', t('sessions.bulkKillItemUncertain', { name, host: targetHostName }))
      }
      failed.push(name)
    }
  }
  if (failed.length > 0) {
    notify('error', t('sessions.bulkKillFailed', { names: failed.join(', ') }))
  }
  if (
    currentHostId.value === targetHostId &&
    currentHost.value?.connectionRevision === targetConnectionRev &&
    !activeHostRemoved.value
  ) {
    await refresh()
  }
}

function onState(session: string, state: ConnState, hostId = currentHostId.value ?? 'local') {
  const key = panelKey(session, hostId)
  connStates.value = nameMap(connStates.value, { [key]: state })
}

function onNotice(message: string, level: ToastLevel = 'error') {
  notify(level, message)
}

function onOnline() {
  if (hasToken.value && currentHostId.value) {
    void refresh()
  }
}

function onOffline() {
  notify('warning', t('terminal.networkOffline'))
}

let unlistenLocale: (() => void) | null = null

onMounted(async () => {
  unlistenLocale = onLocaleChange((locale, pref) => {
    currentPreference.value = pref
    currentLocale.value = locale
  })
  if (hasToken.value) {
    await loadAvailableHosts(true)
  }
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOffline)
})

onBeforeUnmount(() => {
  unlistenLocale?.()
  stopPolling()
  window.removeEventListener('online', onOnline)
  window.removeEventListener('offline', onOffline)
  cleanupSessionState()
})
</script>

<template>
  <div class="app">
    <div v-if="!hasToken" class="app__auth">
      <div class="auth-card">
        <h1 class="auth-card__title">{{ t('auth.title') }}</h1>
        <p class="auth-card__hint">{{ t('auth.subtitle') }}</p>
        <label class="sr-only" for="access-token">{{ t('auth.tokenPlaceholder') }}</label>
        <input
          id="access-token"
          v-model="tokenInput"
          type="password"
          class="auth-card__input"
          :placeholder="t('auth.tokenPlaceholder')"
          autocomplete="off"
          :disabled="authBusy"
          @keyup.enter="submitToken"
        />
        <button class="auth-card__btn" type="button" :disabled="authBusy" @click="submitToken">
          {{ authBusy ? t('auth.signingIn') : t('auth.signIn') }}
        </button>
        <p v-if="displayAuthError" class="auth-card__error" role="alert" aria-live="assertive">{{ displayAuthError }}</p>
        <div class="auth-card__lang-row">
          <label class="sr-only" for="login-lang-select">{{ t('common.language') }}</label>
          <select
            id="login-lang-select"
            v-model="currentPreference"
            class="auth-card__lang-select"
            :aria-label="t('common.language')"
            @change="onLanguageChange"
          >
            <option value="auto">{{ t('common.autoLanguage') }}</option>
            <option value="en">English</option>
            <option value="zh-CN">简体中文</option>
          </select>
        </div>
      </div>
      <ToastStack />
    </div>

    <template v-else>
      <header class="app__bar">
        <span class="app__title">{{ t('auth.title') }}</span>
        <div class="app__host-select-wrapper">
          <label for="host-select" class="sr-only">{{ t('hosts.selectHost') }}</label>
          <select
            id="host-select"
            v-model="currentHostId"
            class="app__host-select"
            :aria-label="t('hosts.hosts')"
            @change="onHostChange"
          >
            <option v-if="currentHostId === null" :value="null" disabled>{{ t('hosts.selectHost') }}</option>
            <option v-for="h in availableHosts" :key="h.id" :value="h.id">
              {{ h.name || h.id }}{{ h.address ? ` (${h.address})` : '' }}
            </option>
          </select>
        </div>
        <div class="app__lang-select-wrapper">
          <label for="header-lang-select" class="sr-only">{{ t('common.language') }}</label>
          <select
            id="header-lang-select"
            v-model="currentPreference"
            class="app__lang-select"
            :aria-label="t('common.language')"
            @change="onLanguageChange"
          >
            <option value="auto">{{ t('common.autoLanguage') }}</option>
            <option value="en">English</option>
            <option value="zh-CN">简体中文</option>
          </select>
        </div>
        <div class="app__bar-actions">
          <button class="app__bar-btn" type="button" :aria-label="t('hosts.manage')" @click="hostManagerOpen = true">{{ t('hosts.hosts') }}</button>
          <button class="app__logout" type="button" :aria-label="t('auth.signOut')" @click="logout">{{ t('auth.signOut') }}</button>
        </div>
      </header>

      <div v-if="hostDiscoveryError && !hasInitializedWorkspace" class="app__discovery-state app__discovery-state--error" role="alert">
        <p class="app__discovery-msg">{{ displayHostDiscoveryError }}</p>
        <button class="app__discovery-btn" type="button" @click="loadAvailableHosts(true)">{{ t('common.retry') }}</button>
      </div>

      <div v-else-if="!hostDiscoveryLoading && availableHosts.length === 0 && !activeHostRemoved && !hasInitializedWorkspace" class="app__discovery-state app__discovery-state--empty">
        <p class="app__discovery-msg">{{ t('hosts.noHosts') }}</p>
        <button class="app__discovery-btn" type="button" @click="openHostManagerAdd">{{ t('hosts.addHost') }}</button>
      </div>

      <div v-else-if="!currentHostId && !activeHostRemoved && !hasInitializedWorkspace" class="app__discovery-state app__discovery-state--prompt">
        <h2 class="app__discovery-title">{{ t('hosts.selectHost') }}</h2>
        <p class="app__discovery-msg">{{ t('hosts.chooseHost') }}</p>
        <div class="app__prompt-hosts">
          <button
            v-for="h in availableHosts"
            :key="h.id"
            class="app__host-card-btn"
            type="button"
            @click="selectHost(h.id)"
          >
            <span class="app__host-card-name">{{ h.name || h.id }}</span>
            <span v-if="h.address" class="app__host-card-addr">{{ h.address }}</span>
          </button>
        </div>
      </div>

      <div v-else class="app__body">
        <div v-if="hostDiscoveryError" class="app__discovery-state app__discovery-state--error app__discovery-banner" role="alert">
          <p class="app__discovery-msg">{{ displayHostDiscoveryError }}</p>
          <button class="app__discovery-btn" type="button" @click="loadAvailableHosts(false)">{{ t('common.retry') }}</button>
        </div>
        <div v-if="activeHostRemoved" class="app__removed-banner" role="alert">
          {{ availableHosts.length === 0 ? t('hosts.activeHostRemovedAdd') : t('hosts.activeHostRemovedSelect') }}
        </div>
        <aside class="app__sidebar" :class="{ 'app__sidebar--collapsed': sidebarCollapsed }">
          <template v-if="!sidebarCollapsed">
            <button
              class="app__sidebar-toggle"
              type="button"
              :title="t('sessions.collapseSidebar')"
              :aria-label="t('sessions.collapseSidebar')"
              :aria-expanded="!sidebarCollapsed"
              @click="toggleSidebar"
            ><span aria-hidden="true">«</span></button>
            <div class="app__sidebar-body">
              <SessionList
                :sessions="sessions"
                :error="listError"
                :stale="listStale"
                :selected="selected"
                :active="currentHostActivity"
                :creating="creating"
                :host-id="currentHostId ?? undefined"
                :host-name="currentHostName"
                @select="select($event)"
                @create="onCreate"
                @rename="onRename"
                @kill="onKill"
                @bulk-kill="onBulkKill"
                @retry="refresh"
                @repair="openHostManagerFor(currentHostId)"
              />
            </div>
            <button class="app__help-btn" type="button" :title="t('help.guideTitle')" :aria-label="t('help.openHelp')" @click="helpOpen = true">
              ? {{ t('help.helpBtn') }}
            </button>
          </template>
          <div v-else class="app__rail">
            <button
              class="app__rail-btn"
              type="button"
              :title="t('sessions.expandSidebar')"
              :aria-label="t('sessions.expandSidebar')"
              :aria-expanded="!sidebarCollapsed"
              @click="toggleSidebar"
            ><span aria-hidden="true">»</span></button>
            <button class="app__rail-btn" type="button" :title="t('sessions.newSession')" :aria-label="t('sessions.newSession')" :disabled="creating" @click="onCreate">
              <span aria-hidden="true">+</span>
            </button>
            <button class="app__rail-btn" type="button" :title="t('help.openHelp')" :aria-label="t('help.openHelp')" @click="helpOpen = true">
              <span aria-hidden="true">?</span>
            </button>
          </div>
        </aside>

        <main ref="mainEl" class="app__main">
          <header v-if="selected" class="app__term-header">
            <span
              class="app__term-dot"
              :class="{ 'app__term-dot--live': selectedActive }"
              :title="selectedActive ? t('terminal.producingOutput') : t('terminal.idle')"
              aria-hidden="true"
            ></span>
            <span class="app__term-host" :title="t('terminal.hostTitle', { name: currentHostName, id: currentHostId })">{{ currentHostName }}</span>
            <span class="app__term-title">{{ selected }}</span>
            <span
              class="app__term-state"
              :class="`app__term-state--${connStates[panelKey(selected)] ?? 'connecting'}`"
              role="status"
              aria-live="polite"
            >{{ localizedConnState(connStates[panelKey(selected)] ?? 'connecting') }}</span>
            <span class="app__term-spacer"></span>
            <button
              class="app__term-btn"
              type="button"
              :title="t('terminal.decreaseFontSize')"
              :aria-label="t('terminal.decreaseFontSize')"
              :disabled="fontSize <= termConfig.min_font_size"
              @click="setFontSize(fontSize - 1)"
            >A−</button>
            <span class="app__term-fontsize" aria-live="polite" :aria-label="t('terminal.currentFontSize')">{{ fontSize }} px</span>
            <button
              class="app__term-btn"
              type="button"
              :title="t('terminal.increaseFontSize')"
              :aria-label="t('terminal.increaseFontSize')"
              :disabled="fontSize >= termConfig.max_font_size"
              @click="setFontSize(fontSize + 1)"
            >A+</button>
            <button
              class="app__term-btn"
              type="button"
              :title="t('terminal.toggleFullscreen')"
              :aria-label="t('terminal.toggleFullscreen')"
              @click="toggleFullscreen"
            ><span aria-hidden="true">⛶</span></button>
            <button
              v-if="filesAvailable"
              class="app__term-btn"
              type="button"
              :title="t('files.browseFiles')"
              :aria-label="t('files.openFiles')"
              :aria-pressed="fileManagerOpen"
              @click="toggleFileManager"
            >{{ t('files.title') }}</button>
            <button
              class="app__term-btn"
              type="button"
              :title="t('terminal.closePanelTitle')"
              :aria-label="t('terminal.closePanelAria')"
              @click="closePanel"
            ><span aria-hidden="true">✕</span></button>
          </header>
          <div v-if="!selected" class="app__placeholder">{{ t('sessions.selectSessionPlaceholder') }}</div>
          <KeepAlive>
            <TerminalView
              v-if="selected && selectedPanelId !== null"
              :key="selectedPanelId"
              :session="selected"
              :alive="selectedAlive"
              :font-size="fontSize"
              :host-id="currentHostId ?? undefined"
              @state="onState"
              @notice="onNotice"
              @activity="onActivity"
            />
          </KeepAlive>
          <div v-if="fileManagerOpen" class="app__files">
            <FileManagerOverlay
              ref="fileManagerRef"
              :session="selected"
              :host-id="currentHostId ?? undefined"
              :host-name="currentHostName"
              :host-revision="currentHostRevision"
              :available-host-ids="availableHosts.map((h) => h.id)"
              :available-hosts="availableHosts"
              @close="closeFileManager"
              @notice="onNotice"
              @insert-path="insertPathIntoTerminal"
              @dirty-change="onDirtyChange"
            />
          </div>
          <ToastStack />
        </main>
      </div>
    </template>

    <HelpModal :open="helpOpen" @close="helpOpen = false" />
    <HostManagerModal
      :open="hostManagerOpen"
      :initial-host-id="managerInitialHostId"
      :dirty-hosts="dirtyHostIds"
      @close="hostManagerOpen = false; managerInitialHostId = null"
      @hosts-changed="loadAvailableHosts"
      @host-deleted="onHostDeleted"
      @select-host="onSelectHostFromModal"
    />
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.app__auth {
  position: relative;
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}
.app__bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 0.75rem;
  background: var(--th-surface);
  border-bottom: 1px solid var(--th-border);
}
.app__title {
  font-weight: 600;
  font-size: 0.95rem;
}
.app__host-select-wrapper {
  display: flex;
  align-items: center;
}
.app__host-select {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.25rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.app__host-select:focus {
  border-color: var(--th-focus);
  outline: none;
}
.app__lang-select-wrapper {
  display: flex;
  align-items: center;
}
.app__lang-select {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.25rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.app__lang-select:focus {
  border-color: var(--th-focus);
  outline: none;
}
.app__bar-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.app__bar-btn {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.6rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.app__bar-btn:hover {
  border-color: var(--th-text-lo);
}
.app__logout {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.6rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.app__logout:hover {
  border-color: var(--th-text-lo);
}
/* Terminal header: session identity on the left, controls on the right. */
.app__term-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0.6rem;
  background: var(--th-surface);
  border-bottom: 1px solid var(--th-border);
}
.app__term-dot {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--th-text-lo);
}
/* The viewed session's activity is a breathing dot, deliberately different
   from the background sessions' card-border highlight. */
.app__term-dot--live {
  background: var(--th-green);
  animation: app-term-dot-breathe 1.2s ease-in-out infinite;
}
@keyframes app-term-dot-breathe {
  0%,
  100% {
    opacity: 0.35;
  }
  50% {
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .app__term-dot--live {
    animation: none;
    opacity: 1;
  }

  .app__sidebar {
    transition: none;
  }
}
.app__term-host {
  font-size: 0.75rem;
  font-weight: 500;
  padding: 0.15rem 0.4rem;
  background: var(--th-raised);
  color: var(--th-text-lo);
  border: 1px solid var(--th-border);
  border-radius: 3px;
  white-space: nowrap;
}
.app__term-title {
  font-family: var(--app-code-font);
  font-size: 0.85rem;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 40%;
}
.app__term-state {
  font-size: 0.75rem;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  border: 1px solid var(--th-border);
  color: var(--th-text-mid);
  white-space: nowrap;
}
.app__term-state--connected {
  color: var(--th-green, #4ade80);
  border-color: var(--th-green, #4ade80);
}
.app__term-state--reconnecting {
  color: var(--th-accent);
  border-color: var(--th-accent);
}
.app__term-state--ended {
  color: var(--th-danger);
  border-color: var(--th-danger);
}
.app__term-spacer {
  flex: 1;
}
.app__term-btn {
  background: var(--th-raised);
  color: var(--th-text-mid);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.25rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
  white-space: nowrap;
}
.app__term-btn:hover {
  color: var(--th-text-hi);
  border-color: var(--th-text-lo);
}
.app__term-btn:disabled {
  opacity: 0.35;
  cursor: default;
  border-color: var(--th-border);
  color: var(--th-text-lo);
}
.app__term-fontsize {
  font-size: 0.72rem;
  color: var(--th-text-lo);
  min-width: 3.2em;
  text-align: center;
  white-space: nowrap;
  user-select: none;
}
.app__body {
  flex: 1;
  display: flex;
  min-height: 0;
}
.app__sidebar {
  width: 260px;
  flex: 0 0 260px;
  background: var(--th-surface);
  border-right: 1px solid var(--th-border);
  padding: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-height: 0;
  transition: width 0.15s ease, flex-basis 0.15s ease;
}
.app__sidebar--collapsed {
  width: 44px;
  flex-basis: 44px;
  padding: 0.5rem 0.25rem;
  gap: 0.4rem;
}
.app__sidebar-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.app__help-btn {
  background: var(--th-raised);
  color: var(--th-text-mid);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.35rem 0.6rem;
  font-size: 0.8rem;
  cursor: pointer;
  white-space: nowrap;
}
.app__help-btn:hover {
  color: var(--th-text-hi);
  border-color: var(--th-accent);
}
.app__sidebar-toggle {
  align-self: flex-end;
  background: none;
  border: 1px solid var(--th-border);
  border-radius: 4px;
  color: var(--th-text-mid);
  font-size: 0.75rem;
  padding: 0.1rem 0.35rem;
  cursor: pointer;
}
.app__sidebar-toggle:hover {
  color: var(--th-text-hi);
  border-color: var(--th-text-lo);
}
.app__rail {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.app__rail-btn {
  background: var(--th-raised);
  color: var(--th-text-mid);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  font-size: 0.9rem;
  padding: 0.35rem 0;
  cursor: pointer;
}
.app__rail-btn:hover {
  color: var(--th-text-hi);
  border-color: var(--th-text-lo);
}
.app__main {
  flex: 1;
  min-width: 0;
  position: relative;
  display: flex;
  flex-direction: column;
}
/* In fullscreen the main region covers the screen, so it needs its own
   background instead of showing through to the page. */
.app__main:fullscreen {
  background: var(--app-bg);
}
/* The file manager covers the terminal area only. `.app__main` is already a
   positioning context, so this cannot escape over the sidebar. */
.app__files {
  position: absolute;
  inset: 0;
  z-index: 20;
}
.app__placeholder {
  padding: 1rem;
  color: var(--th-text-lo);
  font-size: 0.9rem;
}
</style>

<style>
.auth-card {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  width: 280px;
  padding: 1.5rem;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 8px;
}
.auth-card__title {
  margin: 0;
  font-size: 1.2rem;
}
.auth-card__hint {
  margin: 0;
  color: var(--th-text-lo);
  font-size: 0.85rem;
}
.auth-card__input {
  background: var(--app-bg);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: var(--app-code-font);
  font-size: 0.85rem;
}
.auth-card__btn {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-size: 0.9rem;
  cursor: pointer;
}
.auth-card__btn:hover {
  border-color: var(--th-text-lo);
}
.auth-card__btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.auth-card__error {
  margin: 0;
  color: var(--th-danger);
  font-size: 0.8rem;
}
.auth-card__lang-row {
  margin-top: 0.5rem;
  display: flex;
  justify-content: flex-end;
}
.auth-card__lang-select {
  background: var(--app-bg, #0a0a0a);
  color: var(--th-text-mid, #a3a3a3);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.25rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.auth-card__lang-select:focus {
  border-color: var(--th-focus);
  outline: none;
}
.app__discovery-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 2rem;
  text-align: center;
}
.app__discovery-title {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--th-text-hi);
}
.app__discovery-msg {
  margin: 0;
  color: var(--th-text-lo);
  font-size: 0.95rem;
  max-width: 480px;
}
.app__discovery-state--error .app__discovery-msg {
  color: var(--th-danger);
}
.app__discovery-btn {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.5rem 1rem;
  font-size: 0.9rem;
  cursor: pointer;
}
.app__discovery-btn:hover {
  border-color: var(--th-text-lo);
}
.app__prompt-hosts {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  justify-content: center;
  max-width: 500px;
}
.app__host-card-btn {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.25rem;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.75rem 1rem;
  cursor: pointer;
  min-width: 140px;
  text-align: left;
}
.app__host-card-btn:hover {
  border-color: var(--th-focus);
  background: var(--th-raised);
}
.app__host-card-name {
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--th-text-hi);
}
.app__host-card-addr {
  font-size: 0.75rem;
  color: var(--th-text-lo);
}
.app__removed-banner {
  background: rgba(239, 68, 68, 0.15);
  color: var(--th-danger);
  border-bottom: 1px solid var(--th-danger);
  padding: 0.5rem 1rem;
  font-size: 0.85rem;
  text-align: center;
  font-weight: 500;
}
.app__discovery-banner {
  flex: none;
  padding: 0.5rem 1rem;
  flex-direction: row;
  justify-content: center;
  gap: 1rem;
  background: rgba(239, 68, 68, 0.15);
  border-bottom: 1px solid var(--th-danger);
}
</style>
