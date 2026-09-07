<script lang="ts" setup>
// Task 7.1: session/window/pane navigation tree, populated from the
// engine's domain model (via App.vue's `Sessions()` call). Purely
// presentational — App.vue owns fetching/refreshing the data; task 7.2
// wires live lifecycle-event updates on top of this same component.
import { ref } from 'vue'
import { domain } from '../../wailsjs/go/models'

const props = defineProps<{
  sessions: domain.Session[]
  selectedWindowId: string | null
}>()

// Task 10.1: management-action UI. This component stays presentational --
// it only owns the transient "editing"/"confirming" UI state for inline
// rename/kill affordances, and emits the actual engine operation upward for
// App.vue to invoke and report failures for (task 10.2). Modeled on
// TmuxHub's inline rename/kill controls rather than native `prompt()`/
// `confirm()` dialogs, which are unreliable to drive/test inside a Wails
// WKWebView.
const emit = defineEmits<{
  (e: 'select-window', windowId: string): void
  (e: 'create-session', name: string): void
  (e: 'kill-session', name: string): void
  (e: 'rename-session', oldName: string, newName: string): void
  (e: 'new-window', sessionName: string): void
  (e: 'kill-window', windowId: string): void
  (e: 'rename-window', windowId: string, newName: string): void
  (e: 'split-pane', paneId: string, vertical: boolean): void
  (e: 'kill-pane', paneId: string): void
}>()

function windowsOf(session: domain.Session): domain.Window[] {
  return Object.values(session.Windows ?? {}).sort((a, b) => a.Name.localeCompare(b.Name))
}

function panesOf(win: domain.Window): domain.Pane[] {
  return Object.values(win.Panes ?? {}).sort((a, b) => a.ID.localeCompare(b.ID))
}

const newSessionName = ref('')
function submitCreateSession() {
  const name = newSessionName.value.trim()
  if (!name) return
  emit('create-session', name)
  newSessionName.value = ''
}

const renamingSession = ref<string | null>(null)
const renameSessionDraft = ref('')
function startRenameSession(name: string) {
  renamingSession.value = name
  renameSessionDraft.value = name
}
function submitRenameSession(oldName: string) {
  const newName = renameSessionDraft.value.trim()
  renamingSession.value = null
  if (!newName || newName === oldName) return
  emit('rename-session', oldName, newName)
}
function cancelRenameSession() {
  renamingSession.value = null
}

const confirmKillSession = ref<string | null>(null)
function submitKillSession(name: string) {
  confirmKillSession.value = null
  emit('kill-session', name)
}

const renamingWindow = ref<string | null>(null)
const renameWindowDraft = ref('')
function startRenameWindow(win: domain.Window) {
  renamingWindow.value = win.ID
  renameWindowDraft.value = win.Name
}
function submitRenameWindow(windowId: string) {
  const newName = renameWindowDraft.value.trim()
  renamingWindow.value = null
  if (!newName) return
  emit('rename-window', windowId, newName)
}
function cancelRenameWindow() {
  renamingWindow.value = null
}

const confirmKillWindow = ref<string | null>(null)
function submitKillWindow(windowId: string) {
  confirmKillWindow.value = null
  emit('kill-window', windowId)
}

const confirmKillPane = ref<string | null>(null)
function submitKillPane(paneId: string) {
  confirmKillPane.value = null
  emit('kill-pane', paneId)
}
</script>

<template>
  <div class="nav-tree-wrapper">
    <form class="nav-tree__new-session" @submit.prevent="submitCreateSession">
      <input
        v-model="newSessionName"
        class="nav-tree__new-session-input"
        placeholder="New session name"
      />
      <button type="submit" class="nav-tree__icon-btn" title="Create session">+ Session</button>
    </form>
    <ul class="nav-tree">
      <li v-for="session in props.sessions" :key="session.Key.Name" class="nav-tree__session">
        <div class="nav-tree__label nav-tree__label--session nav-tree__row">
          <template v-if="renamingSession === session.Key.Name">
            <input
              v-model="renameSessionDraft"
              class="nav-tree__rename-input"
              autofocus
              @keydown.enter="submitRenameSession(session.Key.Name)"
              @keydown.escape="cancelRenameSession"
              @click.stop
            />
            <button class="nav-tree__icon-btn" title="Confirm rename" @click.stop="submitRenameSession(session.Key.Name)">✓</button>
            <button class="nav-tree__icon-btn" title="Cancel" @click.stop="cancelRenameSession">✕</button>
          </template>
          <template v-else-if="confirmKillSession === session.Key.Name">
            <span class="nav-tree__confirm-label">Kill session?</span>
            <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Confirm kill" @click.stop="submitKillSession(session.Key.Name)">Yes</button>
            <button class="nav-tree__icon-btn" title="Cancel" @click.stop="confirmKillSession = null">No</button>
          </template>
          <template v-else>
            <span class="nav-tree__label-text">{{ session.Key.Name }}</span>
            <span class="nav-tree__row-actions">
              <button class="nav-tree__icon-btn" title="New window in this session" @click.stop="emit('new-window', session.Key.Name)">+win</button>
              <button class="nav-tree__icon-btn" title="Rename session" @click.stop="startRenameSession(session.Key.Name)">✎</button>
              <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Kill session" @click.stop="confirmKillSession = session.Key.Name">✕</button>
            </span>
          </template>
        </div>
        <ul>
          <li
            v-for="win in windowsOf(session)"
            :key="win.ID"
            class="nav-tree__window"
            :class="{
              'nav-tree__window--active': win.Active,
              'nav-tree__window--selected': win.ID === props.selectedWindowId,
            }"
            @click="emit('select-window', win.ID)"
          >
            <div class="nav-tree__label nav-tree__label--window nav-tree__row">
              <template v-if="renamingWindow === win.ID">
                <input
                  v-model="renameWindowDraft"
                  class="nav-tree__rename-input"
                  autofocus
                  @keydown.enter.stop="submitRenameWindow(win.ID)"
                  @keydown.escape.stop="cancelRenameWindow"
                  @click.stop
                />
                <button class="nav-tree__icon-btn" title="Confirm rename" @click.stop="submitRenameWindow(win.ID)">✓</button>
                <button class="nav-tree__icon-btn" title="Cancel" @click.stop="cancelRenameWindow">✕</button>
              </template>
              <template v-else-if="confirmKillWindow === win.ID">
                <span class="nav-tree__confirm-label">Kill window?</span>
                <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Confirm kill" @click.stop="submitKillWindow(win.ID)">Yes</button>
                <button class="nav-tree__icon-btn" title="Cancel" @click.stop="confirmKillWindow = null">No</button>
              </template>
              <template v-else>
                <span class="nav-tree__label-text">{{ win.Name }}</span>
                <span class="nav-tree__row-actions">
                  <button class="nav-tree__icon-btn" title="Rename window" @click.stop="startRenameWindow(win)">✎</button>
                  <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Kill window" @click.stop="confirmKillWindow = win.ID">✕</button>
                </span>
              </template>
            </div>
            <ul>
              <li
                v-for="pane in panesOf(win)"
                :key="pane.ID"
                class="nav-tree__pane"
                :class="{ 'nav-tree__pane--active': pane.Active, 'nav-tree__pane--dead': pane.Dead }"
              >
                <span class="nav-tree__pane-id">{{ pane.ID }}</span>
                <span v-if="pane.Command" class="nav-tree__pane-command">{{ pane.Command }}</span>
                <span v-if="pane.Dead" class="nav-tree__pane-dead-flag">dead</span>
                <span class="nav-tree__row-actions">
                  <template v-if="confirmKillPane === pane.ID">
                    <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Confirm kill" @click.stop="submitKillPane(pane.ID)">Yes</button>
                    <button class="nav-tree__icon-btn" title="Cancel" @click.stop="confirmKillPane = null">No</button>
                  </template>
                  <template v-else>
                    <button class="nav-tree__icon-btn" title="Split: new pane to the right" @click.stop="emit('split-pane', pane.ID, false)">⬌</button>
                    <button class="nav-tree__icon-btn" title="Split: new pane below" @click.stop="emit('split-pane', pane.ID, true)">⬍</button>
                    <button class="nav-tree__icon-btn nav-tree__icon-btn--danger" title="Kill pane" @click.stop="confirmKillPane = pane.ID">✕</button>
                  </template>
                </span>
              </li>
            </ul>
          </li>
        </ul>
      </li>
    </ul>
  </div>
</template>

<style scoped>
/* Sidebar visual language modeled on TmuxHub's OpenSessionsSidebar: quiet
   session-group headers, a subtle raised+accent-bar treatment for the
   selected window row (in place of OpenSessionsSidebar's per-host
   grouping/pinning, which is out of scope here), and low-emphasis
   monospace pane rows. */
.nav-tree,
.nav-tree ul {
  list-style: none;
  margin: 0;
  padding-left: 0.7rem;
}
.nav-tree {
  padding-left: 0;
  font-size: 0.85rem;
  font-family: var(--app-ui-font);
}
.nav-tree__label {
  padding: 0.2rem 0.4rem;
  border-radius: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.nav-tree__label--session {
  font-weight: 600;
  font-size: 0.75rem;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--th-text-lo);
  margin-top: 0.4rem;
}
.nav-tree__window {
  cursor: pointer;
  border-left: 2px solid transparent;
  border-radius: 4px;
}
.nav-tree__window:hover {
  background: var(--th-raised);
}
.nav-tree__label--window {
  color: var(--th-text-mid);
}
.nav-tree__window--active > .nav-tree__label--window {
  color: var(--th-text-hi);
}
.nav-tree__window--selected {
  background: var(--th-raised);
  border-left-color: var(--th-accent);
}
.nav-tree__window--selected > .nav-tree__label--window {
  color: var(--th-text-hi);
  font-weight: 600;
}
.nav-tree__pane {
  padding: 0.1rem 0.4rem;
  color: var(--th-text-lo);
  display: flex;
  gap: 0.4rem;
  font-family: var(--app-code-font);
  font-size: 0.78rem;
}
.nav-tree__pane--active {
  color: var(--th-text-hi);
}
.nav-tree__pane--dead {
  text-decoration: line-through;
}
.nav-tree__pane-command {
  color: var(--th-accent);
}
.nav-tree__pane-dead-flag {
  color: var(--th-danger);
}
.nav-tree-wrapper {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.nav-tree__new-session {
  display: flex;
  gap: 0.3rem;
  padding: 0.2rem 0.2rem 0.5rem;
}
.nav-tree__new-session-input {
  flex: 1;
  min-width: 0;
  background: var(--app-bg);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.25rem 0.4rem;
  font-family: var(--app-code-font);
  font-size: 0.75rem;
}
.nav-tree__row {
  display: flex;
  align-items: center;
  gap: 0.3rem;
}
.nav-tree__label-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.nav-tree__row-actions {
  display: flex;
  gap: 0.15rem;
  flex-shrink: 0;
}
.nav-tree__pane {
  justify-content: space-between;
}
.nav-tree__icon-btn {
  background: var(--th-raised);
  color: var(--th-text-mid);
  border: 1px solid var(--th-border);
  border-radius: 3px;
  padding: 0.05rem 0.3rem;
  font-size: 0.7rem;
  line-height: 1.4;
  cursor: pointer;
}
.nav-tree__icon-btn:hover {
  border-color: var(--th-text-lo);
  color: var(--th-text-hi);
}
.nav-tree__icon-btn--danger:hover {
  border-color: var(--th-danger);
  color: var(--th-danger);
}
.nav-tree__rename-input {
  flex: 1;
  min-width: 0;
  background: var(--app-bg);
  color: var(--th-text-hi);
  border: 1px solid var(--th-accent);
  border-radius: 3px;
  padding: 0.1rem 0.3rem;
  font-family: inherit;
  font-size: inherit;
}
.nav-tree__confirm-label {
  flex: 1;
  color: var(--th-danger);
  font-size: 0.75rem;
}
</style>
