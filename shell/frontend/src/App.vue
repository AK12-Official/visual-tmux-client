<script lang="ts" setup>
// Product shell entry point. Task groups 7-10 build out this file
// incrementally: 7.x wires the navigation tree to the engine's domain
// model; 8.x adds the geometry-accurate pane grid; 9.x/10.x add input
// forwarding, focus, and management actions on top.
//
// Connection target: the shell connects to a single local tmux host
// (multi-host is explicitly deferred, see design.md). The socket-name
// field defaults to blank, which `tmuxconn.Connect` treats as tmux's
// default socket -- but for *verification* of this app during
// development, always type in a dedicated `-L <name>` socket rather than
// the default one, since the agent/tooling driving verification may
// itself be running inside a default-socket tmux session that must not
// be disturbed.
import { ref } from 'vue'
import {
  Connect,
  CreateSession,
  Disconnect,
  KillPane,
  KillSession,
  KillWindow,
  NewWindow,
  RenameSession,
  RenameWindow,
  Sessions,
  SplitPane,
} from '../wailsjs/go/main/App'
import { domain } from '../wailsjs/go/models'
import { EventsOn } from '../wailsjs/runtime/runtime'
import NavigationTree from './components/NavigationTree.vue'
import PaneGrid from './components/PaneGrid.vue'

const socketName = ref('')
const connected = ref(false)
const connecting = ref(false)
const errorMessage = ref('')
const sessions = ref<domain.Session[]>([])
const selectedWindowId = ref<string | null>(null)

// Lifecycle event types (engine/eventbus's LifecycleEventType string
// values) that mean the domain model snapshot the nav tree and pane grid
// render from may be stale. `pane.focus-changed` (task 9.2) is included
// so that a `select-pane` triggered externally (e.g. a bare `tmux
// select-pane` on the underlying server) updates the active-pane
// highlighting the same way a click-to-focus inside this shell does --
// both just flow through this same refresh() round-trip. `host.*` is
// left out: it's connection state, already handled by Connect/Disconnect
// here.
const TREE_RELEVANT_LIFECYCLE_EVENTS = new Set([
  'session.discovered',
  'session.closed',
  'session.renamed',
  'window.layout-changed',
  'window.renamed',
  'pane.died',
  'pane.focus-changed',
])

interface LifecyclePayload {
  type: string
  payload: unknown
}

let unsubscribeLifecycle: (() => void) | null = null

async function connect() {
  errorMessage.value = ''
  connecting.value = true
  try {
    await Connect(socketName.value)
    connected.value = true
    unsubscribeLifecycle = EventsOn('engine:lifecycle', (evt: LifecyclePayload) => {
      if (TREE_RELEVANT_LIFECYCLE_EVENTS.has(evt.type)) {
        refresh()
      }
    })
    await refresh()
  } catch (err) {
    errorMessage.value = String(err)
  } finally {
    connecting.value = false
  }
}

async function disconnect() {
  try {
    await Disconnect()
  } catch (err) {
    errorMessage.value = String(err)
  } finally {
    if (unsubscribeLifecycle) {
      unsubscribeLifecycle()
      unsubscribeLifecycle = null
    }
    connected.value = false
    sessions.value = []
    selectedWindowId.value = null
  }
}

async function refresh() {
  try {
    sessions.value = await Sessions()
  } catch (err) {
    errorMessage.value = String(err)
    return
  }
  reconcileSelectedWindow()
}

// Keeps `selectedWindowId` pointing at a window that still exists, so the
// pane grid isn't left showing a stale/killed window. If nothing is
// selected yet (first load) or the previous selection disappeared (e.g.
// its window was killed), fall back to the active window of the first
// session, if any -- so there's something to look at without requiring a
// manual click on first connect.
function reconcileSelectedWindow() {
  const allWindows = sessions.value.flatMap((s) => Object.values(s.Windows ?? {}))
  if (selectedWindowId.value && allWindows.some((w) => w.ID === selectedWindowId.value)) {
    return
  }
  const fallback = allWindows.find((w) => w.Active) ?? allWindows[0]
  selectedWindowId.value = fallback ? fallback.ID : null
}

function selectWindow(windowId: string) {
  selectedWindowId.value = windowId
}

// Task 10.1/10.2: management actions. Each wraps its engine call in the
// same errorMessage-surfacing pattern as connect()/refresh() above, and
// explicitly re-`refresh()`s on success rather than relying solely on the
// corresponding lifecycle event to arrive -- e.g. `%window-add` (behind
// NewWindow) currently updates the domain model without publishing a
// lifecycle event at all (only the window's own later `%layout-change`
// does), so waiting on the event bus alone would leave the tree stale
// immediately after a successful action. This keeps every action's
// success/failure outcome deterministic and immediately visible regardless
// of what the engine's notification stream happens to also deliver,
// satisfying 10.2's "without leaving the tree or pane grid in an
// inconsistent state".
async function runAction(action: () => Promise<void>) {
  errorMessage.value = ''
  try {
    await action()
    await refresh()
  } catch (err) {
    errorMessage.value = String(err)
  }
}

function createSession(name: string) {
  return runAction(() => CreateSession(name))
}
function killSession(name: string) {
  return runAction(() => KillSession(name))
}
function renameSession(oldName: string, newName: string) {
  return runAction(() => RenameSession(oldName, newName))
}
function newWindow(sessionName: string) {
  return runAction(() => NewWindow(sessionName))
}
function killWindow(windowId: string) {
  return runAction(() => KillWindow(windowId))
}
function renameWindow(windowId: string, newName: string) {
  return runAction(() => RenameWindow(windowId, newName))
}
function splitPane(paneId: string, vertical: boolean) {
  return runAction(() => SplitPane(paneId, vertical))
}
function killPane(paneId: string) {
  return runAction(() => KillPane(paneId))
}
</script>

<template>
  <div class="shell">
    <header class="shell__connect-bar">
      <input
        v-model="socketName"
        class="shell__socket-input"
        placeholder="tmux -L socket name (blank = default)"
        :disabled="connected"
      />
      <button v-if="!connected" @click="connect" :disabled="connecting">Connect</button>
      <button v-else @click="disconnect">Disconnect</button>
      <button v-if="connected" @click="refresh">Refresh</button>
      <span v-if="errorMessage" class="shell__error">{{ errorMessage }}</span>
    </header>
    <main class="shell__body">
      <nav v-if="connected" class="shell__nav">
        <NavigationTree
          :sessions="sessions"
          :selected-window-id="selectedWindowId"
          @select-window="selectWindow"
          @create-session="createSession"
          @kill-session="killSession"
          @rename-session="renameSession"
          @new-window="newWindow"
          @kill-window="killWindow"
          @rename-window="renameWindow"
          @split-pane="splitPane"
          @kill-pane="killPane"
        />
      </nav>
      <PaneGrid
        v-if="connected"
        class="shell__pane-grid"
        :sessions="sessions"
        :selected-window-id="selectedWindowId"
      />
      <div v-else class="shell__placeholder">Not connected.</div>
    </main>
  </div>
</template>

<style scoped>
/* Chrome tokens/spacing modeled on TmuxHub's (git.woa.com/tmuxhub/tmuxhub)
   default-dark theme + DesktopPaneHeader/OpenSessionsSidebar layout
   conventions -- near-black app background, slightly-raised surface bars,
   thin low-contrast borders instead of heavy dividers. */
.shell {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--app-bg);
  color: var(--th-text-hi);
  font-family: var(--app-ui-font);
}
.shell__connect-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.4rem 0.6rem;
  background: var(--th-surface);
  border-bottom: 1px solid var(--th-border);
}
.shell__socket-input {
  flex: 0 0 260px;
  background: var(--app-bg);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.5rem;
  font-family: var(--app-code-font);
  font-size: 0.8rem;
}
.shell__socket-input::placeholder {
  color: var(--th-text-lo);
}
.shell__socket-input:disabled {
  opacity: 0.6;
}
.shell__connect-bar button {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.7rem;
  font-size: 0.8rem;
  cursor: pointer;
}
.shell__connect-bar button:hover:not(:disabled) {
  border-color: var(--th-text-lo);
}
.shell__connect-bar button:disabled {
  opacity: 0.5;
  cursor: default;
}
.shell__error {
  color: var(--th-danger);
  font-size: 0.8rem;
}
.shell__body {
  flex: 1;
  display: flex;
  overflow: auto;
}
.shell__nav {
  width: 280px;
  flex: 0 0 280px;
  background: var(--th-surface);
  border-right: 1px solid var(--th-border);
  overflow: auto;
  padding: 0.4rem;
}
.shell__pane-grid {
  flex: 1;
  min-width: 0;
  background: var(--app-bg);
}
.shell__placeholder {
  padding: 1rem;
  color: var(--th-text-lo);
  font-size: 0.85rem;
}
</style>
