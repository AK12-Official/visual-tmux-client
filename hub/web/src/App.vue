<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  AuthError,
  clearToken,
  createSession,
  getToken,
  killSession,
  listSessions,
  renameSession,
  setToken,
  type Session,
} from './api'
import { notify } from './toasts'
import type { ConnState } from './terminal'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'
import ToastStack from './components/ToastStack.vue'
import HelpModal from './components/HelpModal.vue'

const token = ref(getToken() ?? '')
const tokenInput = ref('')
const authError = ref('')

const sessions = ref<Session[]>([])
const listError = ref('')
const selected = ref<string | null>(null)
const connStates = ref<Record<string, ConnState>>({})

const hasToken = computed(() => !!token.value)

// Whether the selected session still exists server-side, from the same polled
// list that renders the sidebar. Drives the terminal's ended overlay: a
// detached-but-alive session offers Reconnect; a gone one states its end.
const selectedAlive = computed(() =>
  selected.value !== null && sessions.value.some((s) => s.name === selected.value),
)

// --- Terminal header: font size (persisted), fullscreen, close ---

const FONT_SIZE_KEY = 'vtc:font-size'
const FONT_MIN = 8
const FONT_MAX = 24
const FONT_DEFAULT = 13

function loadFontSize(): number {
  const v = Number.parseInt(localStorage.getItem(FONT_SIZE_KEY) ?? '', 10)
  return v >= FONT_MIN && v <= FONT_MAX ? v : FONT_DEFAULT
}

const fontSize = ref(loadFontSize())

function setFontSize(px: number): void {
  const clamped = Math.min(FONT_MAX, Math.max(FONT_MIN, px))
  fontSize.value = clamped
  localStorage.setItem(FONT_SIZE_KEY, String(clamped))
}

const mainEl = ref<HTMLElement | null>(null)

// Fullscreen targets the main region (header + terminal) so the controls stay
// reachable. Size re-negotiation needs no handler here: the terminal's
// ResizeObserver fires when the region's box changes.
function toggleFullscreen(): void {
  if (document.fullscreenElement) {
    void document.exitFullscreen()
  } else {
    void mainEl.value?.requestFullscreen()
  }
}

// Close detaches the view, never the session: the terminal keeps streaming in
// the KeepAlive cache, so the session stays alive and can still show activity.
function closePanel(): void {
  if (document.fullscreenElement) void document.exitFullscreen()
  selected.value = null
}

// --- Session activity indication ---
// Background terminals keep streaming (KeepAlive), so "which session just
// produced output" is observable for free: each activity event lights the
// session's card (or, for the viewed session, the header dot and the tab
// title) and a decay timer dims it ~2 s after output stops.

const ACTIVITY_DECAY_MS = 2000
const activity = ref<Record<string, boolean>>({})
const activityTimers = new Map<string, ReturnType<typeof setTimeout>>()

function onActivity(name: string): void {
  if (!activity.value[name]) {
    activity.value = { ...activity.value, [name]: true }
  }
  const timer = activityTimers.get(name)
  if (timer) clearTimeout(timer)
  activityTimers.set(
    name,
    setTimeout(() => {
      activityTimers.delete(name)
      const next = { ...activity.value }
      delete next[name]
      activity.value = next
    }, ACTIVITY_DECAY_MS),
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
  localStorage.setItem(SIDEBAR_KEY, sidebarCollapsed.value ? '1' : '0')
}

// --- In-app help ---

const helpOpen = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(() => void refresh(), 5000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function refresh() {
  if (!hasToken.value) return
  try {
    sessions.value = await listSessions()
    listError.value = ''
    startPolling()
  } catch (err) {
    if (err instanceof AuthError) {
      handleAuthFailure()
    } else {
      listError.value = err instanceof Error ? err.message : String(err)
    }
  }
}

function handleAuthFailure() {
  stopPolling()
  clearToken()
  token.value = ''
  selected.value = null
  sessions.value = []
  authError.value = 'Authentication failed. Re-enter the token.'
}

function submitToken() {
  const t = tokenInput.value.trim()
  if (!t) return
  setToken(t)
  token.value = t
  tokenInput.value = ''
  authError.value = ''
  void refresh()
}

function logout() {
  stopPolling()
  clearToken()
  token.value = ''
  selected.value = null
  sessions.value = []
}

// One-click creation: the hub assigns the default name (made unique by its
// suffix retry); renaming is how the user personalizes it afterwards.
async function onCreate() {
  try {
    await createSession()
    await refresh()
  } catch (err) {
    notify('error', err instanceof Error ? err.message : String(err))
  }
}

async function onRename(oldName: string, newName: string) {
  try {
    await renameSession(oldName, newName)
    await refresh()
  } catch (err) {
    notify('error', err instanceof Error ? err.message : String(err))
  }
}

async function onKill(name: string) {
  try {
    await killSession(name)
    if (selected.value === name) selected.value = null
    await refresh()
  } catch (err) {
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
    } catch {
      failed.push(name)
    }
  }
  if (failed.length > 0) {
    notify('error', `Failed to kill: ${failed.join(', ')}`)
  }
  await refresh()
}

function onState(session: string, state: ConnState) {
  connStates.value = { ...connStates.value, [session]: state }
}

function onNotice(message: string, level: 'error' | 'warning' = 'error') {
  notify(level, message)
}

onMounted(() => {
  if (hasToken.value) void refresh()
})

onBeforeUnmount(() => {
  stopPolling()
  for (const timer of activityTimers.values()) clearTimeout(timer)
  activityTimers.clear()
})
</script>

<template>
  <div class="app">
    <div v-if="!hasToken" class="app__auth">
      <div class="auth-card">
        <h1 class="auth-card__title">Visual Tmux Client</h1>
        <p class="auth-card__hint">Enter the access token to connect.</p>
        <input
          v-model="tokenInput"
          type="password"
          class="auth-card__input"
          placeholder="token"
          autocomplete="off"
          @keyup.enter="submitToken"
        />
        <button class="auth-card__btn" @click="submitToken">Connect</button>
        <p v-if="authError" class="auth-card__error">{{ authError }}</p>
      </div>
    </div>

    <template v-else>
      <header class="app__bar">
        <span class="app__title">Visual Tmux Client</span>
        <button class="app__logout" @click="logout">Disconnect</button>
      </header>

      <div class="app__body">
        <aside class="app__sidebar" :class="{ 'app__sidebar--collapsed': sidebarCollapsed }">
          <template v-if="!sidebarCollapsed">
            <button
              class="app__sidebar-toggle"
              title="collapse sidebar"
              @click="toggleSidebar"
            >«</button>
            <div class="app__sidebar-body">
              <SessionList
                :sessions="sessions"
                :error="listError"
                :selected="selected"
                :active="activity"
                @select="selected = $event"
                @create="onCreate"
                @rename="onRename"
                @kill="onKill"
                @bulk-kill="onBulkKill"
                @retry="refresh"
              />
            </div>
            <button class="app__help-btn" title="tmux 使用指南" @click="helpOpen = true">
              ? Help
            </button>
          </template>
          <div v-else class="app__rail">
            <button class="app__rail-btn" title="expand sidebar" @click="toggleSidebar">»</button>
            <button class="app__rail-btn" title="new session" @click="onCreate">+</button>
            <button class="app__rail-btn" title="help" @click="helpOpen = true">?</button>
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
            >{{ connStates[selected] ?? 'connecting' }}</span>
            <span class="app__term-spacer"></span>
            <button
              class="app__term-btn"
              title="decrease font size"
              @click="setFontSize(fontSize - 1)"
            >A−</button>
            <button
              class="app__term-btn"
              title="increase font size"
              @click="setFontSize(fontSize + 1)"
            >A+</button>
            <button
              class="app__term-btn"
              title="toggle fullscreen"
              @click="toggleFullscreen"
            >⛶</button>
            <button
              class="app__term-btn"
              title="close panel (the session keeps running)"
              @click="closePanel"
            >✕</button>
          </header>
          <div v-if="!selected" class="app__placeholder">Select a session.</div>
          <KeepAlive>
            <TerminalView
              v-if="selected"
              :key="selected"
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
.auth-card__error {
  margin: 0;
  color: var(--th-danger);
  font-size: 0.8rem;
}
</style>
