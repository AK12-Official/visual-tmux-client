<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
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
import type { ConnState } from './terminal'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'

const token = ref(getToken() ?? '')
const tokenInput = ref('')
const authError = ref('')

const sessions = ref<Session[]>([])
const listError = ref('')
const selected = ref<string | null>(null)
const connStates = ref<Record<string, ConnState>>({})
const notice = ref('')

const hasToken = computed(() => !!token.value)

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

async function onCreate(name: string) {
  try {
    await createSession(name)
    await refresh()
  } catch (err) {
    notice.value = err instanceof Error ? err.message : String(err)
  }
}

async function onRename(oldName: string, newName: string) {
  try {
    await renameSession(oldName, newName)
    await refresh()
  } catch (err) {
    notice.value = err instanceof Error ? err.message : String(err)
  }
}

async function onKill(name: string) {
  try {
    await killSession(name)
    if (selected.value === name) selected.value = null
    await refresh()
  } catch (err) {
    notice.value = err instanceof Error ? err.message : String(err)
  }
}

function onState(session: string, state: ConnState) {
  connStates.value = { ...connStates.value, [session]: state }
}

function onNotice(message: string) {
  notice.value = message
}

onMounted(() => {
  if (hasToken.value) void refresh()
})

onBeforeUnmount(() => {
  stopPolling()
})
</script>

<template>
  <div class="app">
    <div v-if="!hasToken" class="app__auth">
      <div class="auth-card">
        <h1 class="auth-card__title">tmux-hub</h1>
        <p class="auth-card__hint">Enter the hub token to connect.</p>
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
        <span class="app__title">tmux-hub</span>
        <span
          v-if="selected"
          class="app__state"
          :class="`app__state--${connStates[selected] ?? 'connecting'}`"
        >{{ connStates[selected] ?? 'connecting' }}</span>
        <button class="app__logout" @click="logout">Disconnect</button>
      </header>

      <div class="app__body">
        <aside class="app__sidebar">
          <SessionList
            :sessions="sessions"
            :error="listError"
            :selected="selected"
            @select="selected = $event"
            @create="onCreate"
            @rename="onRename"
            @kill="onKill"
            @retry="refresh"
          />
        </aside>

        <main class="app__main">
          <div v-if="!selected" class="app__placeholder">Select a session.</div>
          <KeepAlive>
            <TerminalView
              v-if="selected"
              :key="selected"
              :session="selected"
              @state="onState"
              @notice="onNotice"
            />
          </KeepAlive>
          <div v-if="notice" class="app__notice">
            <span>{{ notice }}</span>
            <button class="app__notice-btn" @click="notice = ''">✕</button>
          </div>
        </main>
      </div>
    </template>
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
.app__state {
  font-size: 0.75rem;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  border: 1px solid var(--th-border);
  color: var(--th-text-mid);
}
.app__state--connected {
  color: var(--th-green, #4ade80);
  border-color: var(--th-green, #4ade80);
}
.app__state--reconnecting {
  color: var(--th-accent);
  border-color: var(--th-accent);
}
.app__state--ended {
  color: var(--th-danger);
  border-color: var(--th-danger);
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
  overflow: auto;
}
.app__main {
  flex: 1;
  min-width: 0;
  position: relative;
  display: flex;
  flex-direction: column;
}
.app__placeholder {
  padding: 1rem;
  color: var(--th-text-lo);
  font-size: 0.9rem;
}
.app__notice {
  position: absolute;
  bottom: 0.75rem;
  left: 0.75rem;
  right: 0.75rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.5rem 0.75rem;
  font-size: 0.85rem;
}
.app__notice-btn {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--th-text-mid);
  cursor: pointer;
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
