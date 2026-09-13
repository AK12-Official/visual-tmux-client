<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Session } from '../api'
import {
  applyOrder,
  loadOrder,
  saveOrder,
  type OrderMode,
  type OrderState,
} from '../ordering'

const props = defineProps<{
  sessions: Session[]
  error: string
  selected: string | null
  /** Sessions with terminal output in the last few seconds. */
  active: Record<string, boolean>
}>()

const emit = defineEmits<{
  (e: 'select', name: string): void
  (e: 'create'): void
  (e: 'rename', oldName: string, newName: string): void
  (e: 'kill', name: string): void
  (e: 'bulkKill', names: string[]): void
  (e: 'retry'): void
}>()

// --- Ordering (manual mode persists per browser) ---

const order = ref<OrderState>(loadOrder())

function setMode(mode: OrderMode): void {
  order.value = { ...order.value, mode }
  saveOrder(order.value)
}

const ordered = computed(() => applyOrder(props.sessions, order.value))

// Keep stored entries consistent with reality: sessions that disappeared
// (killed anywhere) are pruned from the saved state.
watch(
  () => props.sessions,
  (sessions) => {
    const names = new Set(sessions.map((s) => s.name))
    const order2 = [...new Set(order.value.order.filter((n) => names.has(n)))]
    const pinned2 = [...new Set(order.value.pinned.filter((n) => names.has(n)))]
    if (order2.length !== order.value.order.length || pinned2.length !== order.value.pinned.length) {
      order.value = { ...order.value, order: order2, pinned: pinned2 }
      saveOrder(order.value)
    }
  },
)

function isPinned(name: string): boolean {
  return order.value.pinned.includes(name)
}

// Pinning is a flag plus "move to the very front", so a freshly pinned
// session visibly jumps to the top of the pinned group. Unpinning only clears
// the flag: rewriting order[] there too would drop the session's entry
// entirely, and applyOrder ranks unknown names last — so unpinning would fling
// a hand-placed session to the bottom of the list instead of leaving it put.
function togglePin(name: string): void {
  const pinned = order.value.pinned.filter((n) => n !== name)
  if (isPinned(name)) {
    order.value = { ...order.value, pinned }
  } else {
    pinned.push(name)
    order.value = {
      ...order.value,
      order: [name, ...order.value.order.filter((n) => n !== name)],
      pinned,
    }
  }
  saveOrder(order.value)
}

// --- Drag reorder (manual mode only) ---

const dragName = ref<string | null>(null)
const dragOverName = ref<string | null>(null)

const vFocus = {
  mounted: (el: HTMLElement) => el.focus(),
}

function onDragStart(name: string, ev: DragEvent): void {
  dragName.value = name
  // Some browsers refuse to start a drag without payload data.
  ev.dataTransfer?.setData('text/plain', name)
  if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
}

function onDragOver(target: string): void {
  if (!dragName.value || dragName.value === target) return
  if (isPinned(dragName.value) !== isPinned(target)) return
  dragOverName.value = target
}

function onDrop(target: string): void {
  const name = dragName.value
  dragName.value = null
  dragOverName.value = null
  if (!name || name === target) return
  if (isPinned(name) !== isPinned(target)) return
  const list = ordered.value.map((s) => s.name)
  const from = list.indexOf(name)
  const to = list.indexOf(target)
  if (from < 0 || to < 0) return
  list.splice(to, 0, ...list.splice(from, 1))
  order.value = { ...order.value, order: list }
  saveOrder(order.value)
}

// Dragging is convenient with a pointer, but must not be the only way to
// reorder. These controls move within the pinned or unpinned group because
// pinning deliberately fixes the group boundary.
function canMove(name: string, delta: -1 | 1): boolean {
  const pinned = isPinned(name)
  const group = ordered.value.filter((s) => isPinned(s.name) === pinned)
  const index = group.findIndex((s) => s.name === name)
  return index >= 0 && index + delta >= 0 && index + delta < group.length
}

function moveBy(name: string, delta: -1 | 1): void {
  if (!canMove(name, delta)) return
  const pinned = isPinned(name)
  const group = ordered.value.filter((s) => isPinned(s.name) === pinned)
  const index = group.findIndex((s) => s.name === name)
  const target = group[index + delta].name
  const list = ordered.value.map((s) => s.name)
  const from = list.indexOf(name)
  const to = list.indexOf(target)
  ;[list[from], list[to]] = [list[to], list[from]]
  order.value = { ...order.value, order: list }
  saveOrder(order.value)
}

// --- Select mode (batch management) ---

const selecting = ref(false)
const checked = ref(new Set<string>())
const confirmingBulk = ref(false)

function toggleSelectMode(): void {
  selecting.value = !selecting.value
  checked.value = new Set()
  confirmingBulk.value = false
}

function toggleCheck(name: string): void {
  const next = new Set(checked.value)
  if (next.has(name)) {
    next.delete(name)
  } else {
    next.add(name)
  }
  checked.value = next
}

function confirmBulkKill(): void {
  if (checked.value.size === 0) return
  emit('bulkKill', [...checked.value])
  selecting.value = false
  checked.value = new Set()
  confirmingBulk.value = false
}

// --- Rename ---

const renaming = ref<string | null>(null)
const renameDraft = ref('')

function startRename(name: string): void {
  renaming.value = name
  renameDraft.value = name
}

function submitRename(oldName: string): void {
  const name = renameDraft.value.trim()
  renaming.value = null
  if (!name || name === oldName) return
  // Carry the stored position and pin under BOTH names until the server has
  // ruled: the hub may reject the new name (a reserved or full-width
  // character), and replacing the entry outright would leave it pointing at a
  // name that never exists — the pruning watcher would then drop it, silently
  // losing the session's manual position and pin. Holding both adjacent lets
  // the watcher prune whichever name loses, so the survivor keeps the slot
  // either way. A name that already belongs to another live session is not
  // added: the rename cannot succeed, and that session owns its own slot.
  const taken = props.sessions.some((s) => s.name === name)
  const carry = (n: string): string[] => (n === oldName && !taken ? [oldName, name] : [n])
  order.value = {
    ...order.value,
    order: order.value.order.flatMap(carry),
    pinned: order.value.pinned.flatMap(carry),
  }
  saveOrder(order.value)
  emit('rename', oldName, name)
}

// --- Single kill (with confirm) ---

const confirmingKill = ref<string | null>(null)

function requestKill(name: string): void {
  confirmingKill.value = name
}

function confirmKill(): void {
  if (confirmingKill.value === null) return
  emit('kill', confirmingKill.value)
  confirmingKill.value = null
}

const manualMode = computed(() => order.value.mode === 'manual')
</script>

<template>
  <div class="session-list">
    <button class="session-list__create-btn" type="button" @click="emit('create')">+ New session</button>

    <div v-if="!selecting" class="session-list__toolbar">
      <div class="session-list__modes" role="group" aria-label="Sort mode">
        <button
          class="session-list__mode"
          type="button"
          :class="{ 'session-list__mode--on': !manualMode }"
          :aria-pressed="!manualMode"
          @click="setMode('default')"
        >Default</button>
        <button
          class="session-list__mode"
          type="button"
          :class="{ 'session-list__mode--on': manualMode }"
          :aria-pressed="manualMode"
          @click="setMode('manual')"
        >Manual</button>
      </div>
      <button
        class="session-list__tool"
        type="button"
        title="batch select"
        aria-label="Toggle batch selection mode"
        :aria-pressed="selecting"
        @click="toggleSelectMode"
      >Select</button>
    </div>

    <div v-if="error" class="session-list__error" role="alert" aria-live="polite">
      <span>{{ error }}</span>
      <button class="session-list__btn" type="button" @click="emit('retry')">Retry</button>
    </div>

    <div v-else-if="sessions.length === 0" class="session-list__empty" role="status">
      No sessions. Create one above.
    </div>

    <ul v-else class="session-list__items">
      <li
        v-for="s in ordered"
        :key="s.name"
        class="session-list__row"
        :class="{
          'session-list__row--selected': s.name === selected && !selecting,
          'session-list__row--checked': checked.has(s.name),
          'session-list__row--active': active[s.name] && s.name !== selected,
          'session-list__row--drag-over': dragOverName === s.name && dragName !== s.name,
        }"
        :draggable="manualMode && renaming !== s.name"
        tabindex="0"
        :aria-label="`Session ${s.name}; ${s.windows} window${s.windows === 1 ? '' : 's'}; ${s.attached ? 'attached' : 'detached'}`"
        :aria-current="s.name === selected && !selecting ? 'true' : undefined"
        @click="selecting ? toggleCheck(s.name) : emit('select', s.name)"
        @keydown.enter.self="selecting ? toggleCheck(s.name) : emit('select', s.name)"
        @keydown.space.self.prevent="selecting ? toggleCheck(s.name) : emit('select', s.name)"
        @dragstart="manualMode && onDragStart(s.name, $event)"
        @dragover.prevent="onDragOver(s.name)"
        @dragleave="dragOverName === s.name && (dragOverName = null)"
        @drop.prevent="onDrop(s.name)"
        @dragend="dragName = null; dragOverName = null"
      >
        <div v-if="renaming === s.name" class="session-list__rename" @click.stop>
          <input
            v-focus
            v-model="renameDraft"
            type="text"
            class="session-list__input"
            aria-label="Rename session"
            @keyup.enter="submitRename(s.name)"
            @keyup.esc="renaming = null"
          />
          <button class="session-list__btn" type="button" @click="submitRename(s.name)">OK</button>
          <button class="session-list__btn" type="button" aria-label="Cancel rename" @click="renaming = null">Cancel</button>
        </div>

        <template v-else>
          <div class="session-list__row-main">
            <input
              v-if="selecting"
              type="checkbox"
              class="session-list__check"
              :checked="checked.has(s.name)"
              :aria-label="`Select session ${s.name}`"
              tabindex="-1"
              @click.stop
              @change="toggleCheck(s.name)"
            />
            <span v-if="isPinned(s.name)" class="session-list__pin-mark" title="pinned">★</span>
            <span class="session-list__name">{{ s.name }}</span>
            <span class="session-list__actions" @click.stop>
              <button
                v-if="manualMode"
                class="session-list__icon-btn"
                type="button"
                :title="isPinned(s.name) ? 'unpin' : 'pin to top'"
                :aria-label="isPinned(s.name) ? `Unpin ${s.name}` : `Pin ${s.name} to top`"
                :aria-pressed="isPinned(s.name)"
                @click="togglePin(s.name)"
              ><span aria-hidden="true">{{ isPinned(s.name) ? '★' : '☆' }}</span></button>
              <button
                v-if="manualMode"
                class="session-list__icon-btn"
                type="button"
                title="move up"
                :aria-label="`Move ${s.name} up`"
                :disabled="!canMove(s.name, -1)"
                @click="moveBy(s.name, -1)"
              ><span aria-hidden="true">↑</span></button>
              <button
                v-if="manualMode"
                class="session-list__icon-btn"
                type="button"
                title="move down"
                :aria-label="`Move ${s.name} down`"
                :disabled="!canMove(s.name, 1)"
                @click="moveBy(s.name, 1)"
              ><span aria-hidden="true">↓</span></button>
              <button
                class="session-list__icon-btn"
                type="button"
                title="rename"
                :aria-label="`Rename ${s.name}`"
                @click="startRename(s.name)"
              ><span aria-hidden="true">✎</span></button>
              <button
                class="session-list__icon-btn session-list__icon-btn--danger"
                type="button"
                title="kill"
                :aria-label="`Kill ${s.name}`"
                @click="requestKill(s.name)"
              ><span aria-hidden="true">✕</span></button>
            </span>
          </div>
          <div class="session-list__row-meta">
            {{ s.windows }} window{{ s.windows === 1 ? '' : 's' }} ·
            {{ s.attached ? 'attached' : 'detached' }}
          </div>
        </template>
      </li>
    </ul>

    <div v-if="selecting" class="session-list__batch" role="region" aria-label="Batch actions">
      <template v-if="!confirmingBulk">
        <span class="session-list__batch-count">{{ checked.size }} selected</span>
        <button
          class="session-list__btn session-list__btn--danger"
          type="button"
          :disabled="checked.size === 0"
          @click="confirmingBulk = true"
        >Kill</button>
        <button class="session-list__btn" type="button" @click="toggleSelectMode">Cancel</button>
      </template>
      <template v-else>
        <span>Kill {{ checked.size }} session{{ checked.size === 1 ? '' : 's' }}?</span>
        <button class="session-list__btn session-list__btn--danger" type="button" @click="confirmBulkKill">Kill</button>
        <button class="session-list__btn" type="button" @click="confirmingBulk = false">Cancel</button>
      </template>
    </div>

    <div v-else-if="confirmingKill" class="session-list__confirm" role="alertdialog" aria-label="Confirm session termination">
      <span>Kill session "{{ confirmingKill }}"?</span>
      <button class="session-list__btn session-list__btn--danger" type="button" @click="confirmKill">Kill</button>
      <button class="session-list__btn" type="button" @click="confirmingKill = null">Cancel</button>
    </div>
  </div>
</template>

<style scoped>
.session-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  height: 100%;
}
.session-list__create-btn {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-size: 0.85rem;
  cursor: pointer;
  white-space: nowrap;
}
.session-list__create-btn:hover {
  border-color: var(--th-accent);
}
.session-list__toolbar {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.session-list__modes {
  display: inline-flex;
  border: 1px solid var(--th-border);
  border-radius: 4px;
  overflow: hidden;
}
.session-list__mode {
  background: none;
  border: none;
  color: var(--th-text-lo);
  font-size: 0.72rem;
  padding: 0.2rem 0.5rem;
  cursor: pointer;
}
.session-list__mode--on {
  background: var(--th-raised);
  color: var(--th-text-hi);
}
.session-list__tool {
  margin-left: auto;
  background: none;
  border: 1px solid var(--th-border);
  border-radius: 4px;
  color: var(--th-text-mid);
  font-size: 0.72rem;
  padding: 0.2rem 0.5rem;
  cursor: pointer;
}
.session-list__tool:hover {
  color: var(--th-text-hi);
  border-color: var(--th-text-lo);
}
.session-list__input {
  flex: 1;
  min-width: 0;
  background: var(--app-bg);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.5rem;
  font-family: var(--app-code-font);
  font-size: 0.8rem;
}
.session-list__btn {
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  padding: 0.3rem 0.6rem;
  font-size: 0.8rem;
  cursor: pointer;
  white-space: nowrap;
}
.session-list__btn:hover {
  border-color: var(--th-text-lo);
}
.session-list__btn:disabled {
  opacity: 0.4;
  cursor: default;
}
.session-list__btn--danger {
  border-color: var(--th-danger);
  color: var(--th-danger);
}
.session-list__error {
  color: var(--th-danger);
  font-size: 0.8rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.session-list__empty {
  color: var(--th-text-lo);
  font-size: 0.85rem;
}
.session-list__items {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
  min-height: 0;
}
/* Two-line card: name row over meta row, with enough padding to breathe. */
.session-list__row {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  padding: 0.5rem 0.55rem;
  border-radius: 6px;
  cursor: pointer;
  border: 1px solid transparent;
}
.session-list__row:hover {
  background: var(--th-raised);
}
.session-list__row--selected {
  background: var(--th-raised);
  border-color: var(--th-accent);
}
.session-list__row--checked {
  background: var(--th-raised);
  border-color: var(--th-accent);
}
/* Background activity: a green border on the card, distinct from the selected
   card's accent border and from the viewed session's breathing dot. */
.session-list__row--active {
  border-color: var(--th-green);
}
.session-list__row--drag-over {
  border-color: var(--th-accent);
  border-style: dashed;
}
.session-list__row[draggable='true'] {
  cursor: grab;
}
.session-list__row-main {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.session-list__check {
  accent-color: var(--th-accent);
  flex: 0 0 auto;
}
.session-list__pin-mark {
  color: var(--th-warning);
  font-size: 0.75rem;
  flex: 0 0 auto;
}
.session-list__name {
  font-family: var(--app-code-font);
  font-size: 0.85rem;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.session-list__row-meta {
  color: var(--th-text-lo);
  font-size: 0.72rem;
  padding-left: 0.05rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.session-list__actions {
  display: none;
  gap: 0.2rem;
  flex: 0 0 auto;
}
.session-list__row:hover .session-list__actions,
.session-list__row--selected .session-list__actions,
.session-list__row:focus-within .session-list__actions {
  display: inline-flex;
}
.session-list__icon-btn {
  background: none;
  border: none;
  color: var(--th-text-mid);
  cursor: pointer;
  font-size: 0.85rem;
  min-width: 24px;
  min-height: 24px;
  padding: 0 0.2rem;
}
.session-list__icon-btn:disabled {
  opacity: 0.35;
  cursor: default;
}
.session-list__icon-btn:hover {
  color: var(--th-text-hi);
}
.session-list__icon-btn--danger:hover {
  color: var(--th-danger);
}
.session-list__rename {
  display: flex;
  gap: 0.4rem;
  width: 100%;
}
.session-list__batch,
.session-list__confirm {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8rem;
  color: var(--th-text-hi);
  border-top: 1px solid var(--th-border);
  padding-top: 0.5rem;
}
.session-list__batch-count {
  color: var(--th-text-mid);
  margin-right: auto;
}
</style>
