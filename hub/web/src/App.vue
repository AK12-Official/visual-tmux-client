<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import {
  AuthError,
  clearToken,
  createSession,
  getToken,
  isHeaderSafeToken,
  killSession,
  listSessions,
  renameSession,
  setToken,
  type Session,
} from './api'
import { notify } from './toasts'
import { disposeTerminalSession, renameTerminalSession, type ConnState } from './terminal'
import { getConfig, resolveFontSize } from './config'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'
import ToastStack from './components/ToastStack.vue'
import HelpModal from './components/HelpModal.vue'

const token = ref(getToken() ?? '')
const tokenInput = ref('')
const authError = ref('')
const authBusy = ref(false)

const sessions = ref<Session[]>([])
const listError = ref('')
const selected = ref<string | null>(null)

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
const panelIds = shallowRef<Record<string, number>>(nameMap())
let nextPanelId = 1

function select(name: string): void {
  if (panelIds.value[name] === undefined) {
    panelIds.value = nameMap(panelIds.value, { [name]: nextPanelId++ })
  }
  selected.value = name
}

const selectedPanelId = computed(() =>
  selected.value === null ? null : panelIds.value[selected.value] ?? null,
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
  const raw = localStorage.getItem(FONT_SIZE_KEY)
  const resolved = resolveFontSize(raw, cfg)
  if (raw !== null && String(resolved) !== raw) {
    try {
      localStorage.setItem(FONT_SIZE_KEY, String(resolved))
    } catch {
      /* ignore storage quota */
    }
  }
  return resolved
}

const fontSize = ref(loadFontSize())

function setFontSize(px: number): void {
  const cfg = getConfig().web.terminal
  const clamped = Math.min(cfg.max_font_size, Math.max(cfg.min_font_size, px))
  fontSize.value = clamped
  try {
    localStorage.setItem(FONT_SIZE_KEY, String(clamped))
  } catch {
    /* ignore storage quota */
  }
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
      notify('warning', 'The browser refused to enter fullscreen.')
    })
  }
}

// Close detaches the view, never the session: the terminal keeps streaming in
// the KeepAlive cache, so the session stays alive and can still show activity.
function closePanel(): void {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {
      /* already exited */
    })
  }
  selected.value = null
}

// --- Session activity indication ---
// Background terminals keep streaming (KeepAlive), so "which session just
// produced output" is observable for free: each activity event lights the
// session's card (or, for the viewed session, the header dot and the tab
// title) and a decay timer dims it ~2 s after output stops.

const activity = shallowRef<Record<string, boolean>>(nameMap())
const activityTimers = new Map<string, ReturnType<typeof setTimeout>>()

function onActivity(name: string): void {
  if (!activity.value[name]) {
    activity.value = nameMap(activity.value, { [name]: true })
  }
  const timer = activityTimers.get(name)
  if (timer) clearTimeout(timer)
  activityTimers.set(
    name,
    setTimeout(() => {
      activityTimers.delete(name)
      const next = nameMap(activity.value)
      delete next[name]
      activity.value = next
    }, getConfig().web.activity_decay),
  )
}

const selectedActive = computed(
  () => selected.value !== null && !!activity.value[selected.value],
)

// The tab-title mark is the only signal visible when the browser tab itself
// is backgrounded — the whole point of the prefix.
const BASE_TITLE = 'Visual Tmux Client'
watch(selectedActive, (live) => {
  document.title = live ? `● ${BASE_TITLE}` : BASE_TITLE
})

// --- Sidebar collapse (persisted) ---

const SIDEBAR_KEY = 'vtc:sidebar-collapsed'
const sidebarCollapsed = ref(localStorage.getItem(SIDEBAR_KEY) === '1')

function toggleSidebar(): void {
  sidebarCollapsed.value = !sidebarCollapsed.value
  try {
    localStorage.setItem(SIDEBAR_KEY, sidebarCollapsed.value ? '1' : '0')
  } catch {
    /* ignore storage quota */
  }
}

// --- In-app help ---

const helpOpen = ref(false)

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
  if (!hasToken.value) return
  const seq = ++refreshSeq
  try {
    const list = await listSessions()
    if (seq !== refreshSeq) return
    sessions.value = list
    listError.value = ''
    startPolling()

    // Prune dead panels for sessions that disappeared externally (e.g. killed via CLI).
    const liveNames = new Set(list.map((s) => s.name))
    const deadNames = new Set<string>()
    for (const name of Object.keys(panelIds.value)) {
      if (!liveNames.has(name) && selected.value !== name) deadNames.add(name)
    }
    for (const name of Object.keys(connStates.value)) {
      if (!liveNames.has(name) && selected.value !== name) deadNames.add(name)
    }
    for (const name of Object.keys(activity.value)) {
      if (!liveNames.has(name) && selected.value !== name) deadNames.add(name)
    }
    for (const name of deadNames) {
      dropPanel(name)
    }
  } catch (err) {
    if (seq !== refreshSeq) return
    if (err instanceof AuthError) {
      handleAuthFailure()
    } else {
      listError.value = err instanceof Error ? err.message : String(err)
    }
  }
}

function cleanupSessionState() {
  for (const name of Object.keys(panelIds.value)) {
    disposeTerminalSession(name)
  }
  panelIds.value = nameMap()
  connStates.value = nameMap()
  activity.value = nameMap()
  for (const timer of activityTimers.values()) clearTimeout(timer)
  activityTimers.clear()
}

function handleAuthFailure() {
  stopPolling()
  clearToken()
  token.value = ''
  selected.value = null
  sessions.value = []
  cleanupSessionState()
  authError.value = 'Authentication failed. Re-enter the token.'
}

// The main view opens only after the server has accepted the credential:
// submitting stores the token (authFetch reads it from storage), probes with
// a session listing, and bounces back to this prompt on any failure. A token
// that cannot be sent in an HTTP header (full-width IME characters look
// identical in the password box) is rejected without a round trip.
async function submitToken() {
  const t = tokenInput.value.trim()
  if (!t || authBusy.value) return
  if (!isHeaderSafeToken(t)) {
    authError.value =
      'The token contains characters that cannot be sent in an HTTP header — ' +
      'check for full-width characters (e.g. from an IME), which look identical but are not.'
    return
  }
  authBusy.value = true
  authError.value = ''
  try {
    setToken(t)
    const list = await listSessions()
    token.value = t
    tokenInput.value = ''
    sessions.value = list
    listError.value = ''
    startPolling()
  } catch (err) {
    clearToken()
    if (err instanceof AuthError) {
      authError.value = 'Authentication failed. Check the token and try again.'
    } else {
      authError.value = err instanceof Error ? err.message : String(err)
    }
  } finally {
    authBusy.value = false
  }
}

function logout() {
  stopPolling()
  clearToken()
  token.value = ''
  selected.value = null
  sessions.value = []
  cleanupSessionState()
}

// One-click creation: the hub assigns the default name (made unique by its
// suffix retry); renaming is how the user personalizes it afterwards.
const creating = ref(false)
async function onCreate() {
  if (creating.value) return
  creating.value = true
  try {
    const sess = await createSession()
    await refresh()
    select(sess.name)
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    notify('error', err instanceof Error ? err.message : String(err))
  } finally {
    creating.value = false
  }
}

async function onRename(oldName: string, newName: string) {
  try {
    await renameSession(oldName, newName)
    // Carry the panel identity across the rename, so the live attachment for
    // this session is reused rather than stranded in the KeepAlive cache under
    // a name that no longer exists (see panelIds).
    const id = panelIds.value[oldName]
    if (id !== undefined) {
      const next = nameMap(panelIds.value, { [newName]: id })
      delete next[oldName]
      panelIds.value = next
    }
    if (connStates.value[oldName] !== undefined) {
      const nextStates = nameMap(connStates.value, { [newName]: connStates.value[oldName] })
      delete nextStates[oldName]
      connStates.value = nextStates
    }
    if (activity.value[oldName] !== undefined) {
      const nextAct = nameMap(activity.value, { [newName]: activity.value[oldName] })
      delete nextAct[oldName]
      activity.value = nextAct
    }
    const timer = activityTimers.get(oldName)
    if (timer) {
      clearTimeout(timer)
      activityTimers.delete(oldName)
      activityTimers.set(
        newName,
        setTimeout(() => {
          activityTimers.delete(newName)
          const next = nameMap(activity.value)
          delete next[newName]
          activity.value = next
        }, getConfig().web.activity_decay),
      )
    }
    // Follow the rename: `selected` is the viewed terminal's identity — it
    // names the ws ticket and the header. Left on the old name, the polled
    // list no longer contains it, so a live session reads as ended and every
    // reconnect asks for a ticket the hub answers with session_not_found.
    if (selected.value === oldName) selected.value = newName
    // Retarget the attachment itself. For the viewed panel the `session` prop
    // change would reach it, but a panel parked in the KeepAlive cache never
    // re-renders, so no prop can: without this its terminal keeps the old name
    // and its next reconnect asks the hub for a session that no longer exists.
    renameTerminalSession(oldName, newName)
    await refresh()
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    notify('error', err instanceof Error ? err.message : String(err))
  }
}

// Killing a session retires its panel identity, so a later session that
// happens to reuse the name gets a fresh panel rather than the dead one still
// sitting in the KeepAlive cache (which would greet a brand-new session with an
// "ended" overlay). Retiring the key makes the cached panel unreachable but not
// gone — KeepAlive only unmounts on eviction, and nothing evicts here — so its
// terminal is disposed explicitly, or its xterm instance, scrollback and
// ResizeObserver would be retained for the life of the page.
function dropPanel(name: string): void {
  if (panelIds.value[name] !== undefined) {
    const next = nameMap(panelIds.value)
    delete next[name]
    panelIds.value = next
  }
  if (connStates.value[name] !== undefined) {
    const nextStates = nameMap(connStates.value)
    delete nextStates[name]
    connStates.value = nextStates
  }
  if (activity.value[name] !== undefined) {
    const nextAct = nameMap(activity.value)
    delete nextAct[name]
    activity.value = nextAct
  }
  const timer = activityTimers.get(name)
  if (timer) {
    clearTimeout(timer)
    activityTimers.delete(name)
  }
  disposeTerminalSession(name)
}

async function onKill(name: string) {
  try {
    await killSession(name)
    if (selected.value === name) selected.value = null
    dropPanel(name)
    await refresh()
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
      return
    }
    notify('error', err instanceof Error ? err.message : String(err))
  }
}

// Bulk kill issues one DELETE per session sequentially and names the sessions
// that failed, so a mid-batch failure never hides behind the successes.
async function onBulkKill(names: string[]) {
  const failed: string[] = []
  for (const name of names) {
    try {
      await killSession(name)
      if (selected.value === name) selected.value = null
      dropPanel(name)
    } catch (err) {
      if (err instanceof AuthError) {
        handleAuthFailure()
        return
      }
      failed.push(name)
    }
  }
  if (failed.length > 0) {
    notify('error', `Failed to kill: ${failed.join(', ')}`)
  }
  await refresh()
}

function onState(session: string, state: ConnState) {
  connStates.value = nameMap(connStates.value, { [session]: state })
}

function onNotice(message: string, level: 'error' | 'warning' = 'error') {
  notify(level, message)
}

function onOnline() {
  if (hasToken.value) {
    void refresh()
  }
}

function onOffline() {
  notify('warning', 'Network connection offline')
}

onMounted(() => {
  if (hasToken.value) void refresh()
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOffline)
})

onBeforeUnmount(() => {
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
        <h1 class="auth-card__title">Visual Tmux Client</h1>
        <p class="auth-card__hint">Enter the access token to connect.</p>
        <label class="sr-only" for="access-token">Access token</label>
        <input
          id="access-token"
          v-model="tokenInput"
          type="password"
          class="auth-card__input"
          placeholder="token"
          autocomplete="off"
          :disabled="authBusy"
          @keyup.enter="submitToken"
        />
        <button class="auth-card__btn" type="button" :disabled="authBusy" @click="submitToken">
          {{ authBusy ? 'Connecting…' : 'Connect' }}
        </button>
        <p v-if="authError" class="auth-card__error" role="alert" aria-live="assertive">{{ authError }}</p>
      </div>
      <ToastStack />
    </div>

    <template v-else>
      <header class="app__bar">
        <span class="app__title">Visual Tmux Client</span>
        <button class="app__logout" type="button" aria-label="Disconnect and clear credentials" @click="logout">Disconnect</button>
      </header>

      <div class="app__body">
        <aside class="app__sidebar" :class="{ 'app__sidebar--collapsed': sidebarCollapsed }">
          <template v-if="!sidebarCollapsed">
            <button
              class="app__sidebar-toggle"
              type="button"
              title="collapse sidebar"
              aria-label="Collapse session sidebar"
              :aria-expanded="!sidebarCollapsed"
              @click="toggleSidebar"
            ><span aria-hidden="true">«</span></button>
            <div class="app__sidebar-body">
              <SessionList
                :sessions="sessions"
                :error="listError"
                :selected="selected"
                :active="activity"
                :creating="creating"
                @select="select($event)"
                @create="onCreate"
                @rename="onRename"
                @kill="onKill"
                @bulk-kill="onBulkKill"
                @retry="refresh"
              />
            </div>
            <button class="app__help-btn" type="button" title="tmux 使用指南" @click="helpOpen = true">
              ? Help
            </button>
          </template>
          <div v-else class="app__rail">
            <button
              class="app__rail-btn"
              type="button"
              title="expand sidebar"
              aria-label="Expand session sidebar"
              :aria-expanded="!sidebarCollapsed"
              @click="toggleSidebar"
            ><span aria-hidden="true">»</span></button>
            <button class="app__rail-btn" type="button" title="new session" aria-label="Create new session" :disabled="creating" @click="onCreate">
              <span aria-hidden="true">+</span>
            </button>
            <button class="app__rail-btn" type="button" title="help" aria-label="Open tmux help" @click="helpOpen = true">
              <span aria-hidden="true">?</span>
            </button>
          </div>
        </aside>

        <main ref="mainEl" class="app__main">
          <header v-if="selected" class="app__term-header">
            <span
              class="app__term-dot"
              :class="{ 'app__term-dot--live': selectedActive }"
              :title="selectedActive ? 'producing output' : 'idle'"
              aria-hidden="true"
            ></span>
            <span class="app__term-title">{{ selected }}</span>
            <span
              class="app__term-state"
              :class="`app__term-state--${connStates[selected] ?? 'connecting'}`"
              role="status"
              aria-live="polite"
            >{{ connStates[selected] ?? 'connecting' }}</span>
            <span class="app__term-spacer"></span>
            <button
              class="app__term-btn"
              type="button"
              title="decrease font size"
              aria-label="Decrease terminal font size"
              :disabled="fontSize <= termConfig.min_font_size"
              @click="setFontSize(fontSize - 1)"
            >A−</button>
            <span class="app__term-fontsize" aria-live="polite" aria-label="Current font size">{{ fontSize }} px</span>
            <button
              class="app__term-btn"
              type="button"
              title="increase font size"
              aria-label="Increase terminal font size"
              :disabled="fontSize >= termConfig.max_font_size"
              @click="setFontSize(fontSize + 1)"
            >A+</button>
            <button
              class="app__term-btn"
              type="button"
              title="toggle fullscreen"
              aria-label="Toggle terminal fullscreen"
              @click="toggleFullscreen"
            ><span aria-hidden="true">⛶</span></button>
            <button
              class="app__term-btn"
              type="button"
              title="close panel (the session keeps running)"
              aria-label="Close terminal panel; session keeps running"
              @click="closePanel"
            ><span aria-hidden="true">✕</span></button>
          </header>
          <div v-if="!selected" class="app__placeholder">Select a session.</div>
          <KeepAlive>
            <TerminalView
              v-if="selected && selectedPanelId !== null"
              :key="selectedPanelId"
              :session="selected"
              :alive="selectedAlive"
              :font-size="fontSize"
              @state="onState"
              @notice="onNotice"
              @activity="onActivity"
            />
          </KeepAlive>
          <ToastStack />
        </main>
      </div>
    </template>

    <HelpModal :open="helpOpen" @close="helpOpen = false" />
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
.app__logout {
  margin-left: auto;
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
</style>
