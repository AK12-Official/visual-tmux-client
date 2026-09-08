<script setup lang="ts">
import { ref } from 'vue'
import type { Session } from '../api'

const props = defineProps<{
  sessions: Session[]
  error: string
  selected: string | null
}>()

const emit = defineEmits<{
  (e: 'select', name: string): void
  (e: 'create', name: string): void
  (e: 'rename', oldName: string, newName: string): void
  (e: 'kill', name: string): void
  (e: 'retry'): void
}>()

const newName = ref('')
const renaming = ref<string | null>(null)
const renameDraft = ref('')
const confirmingKill = ref<string | null>(null)

function submitCreate() {
  const name = newName.value.trim()
  if (!name) return
  emit('create', name)
  newName.value = ''
}

function startRename(name: string) {
  renaming.value = name
  renameDraft.value = name
}

function submitRename(oldName: string) {
  const name = renameDraft.value.trim()
  renaming.value = null
  if (!name || name === oldName) return
  emit('rename', oldName, name)
}

function requestKill(name: string) {
  confirmingKill.value = name
}

function confirmKill() {
  if (confirmingKill.value === null) return
  emit('kill', confirmingKill.value)
  confirmingKill.value = null
}
</script>

<template>
  <div class="session-list">
    <div class="session-list__create">
      <input
        v-model="newName"
        class="session-list__input"
        placeholder="new session name (blank = auto)"
        @keyup.enter="submitCreate"
      />
      <button class="session-list__btn" @click="submitCreate">Create</button>
    </div>

    <div v-if="error" class="session-list__error">
      <span>{{ error }}</span>
      <button class="session-list__btn" @click="emit('retry')">Retry</button>
    </div>

    <div v-else-if="sessions.length === 0" class="session-list__empty">
      No sessions. Create one above.
    </div>

    <ul v-else class="session-list__items">
      <li
        v-for="s in sessions"
        :key="s.name"
        class="session-list__row"
        :class="{ 'session-list__row--selected': s.name === selected }"
        @click="emit('select', s.name)"
      >
        <div v-if="renaming === s.name" class="session-list__rename" @click.stop>
          <input
            v-model="renameDraft"
            class="session-list__input"
            @keyup.enter="submitRename(s.name)"
            @keyup.esc="renaming = null"
          />
          <button class="session-list__btn" @click="submitRename(s.name)">OK</button>
        </div>

        <template v-else>
          <span class="session-list__name">{{ s.name }}</span>
          <span class="session-list__meta">{{ s.windows }}w · {{ s.attached ? 'attached' : 'detached' }}</span>
          <span class="session-list__actions" @click.stop>
            <button class="session-list__icon-btn" title="rename" @click="startRename(s.name)">✎</button>
            <button class="session-list__icon-btn session-list__icon-btn--danger" title="kill" @click="requestKill(s.name)">
              ✕
            </button>
          </span>
        </template>
      </li>
    </ul>

    <div v-if="confirmingKill" class="session-list__confirm">
      <span>Kill session "{{ confirmingKill }}"?</span>
      <button class="session-list__btn session-list__btn--danger" @click="confirmKill">Kill</button>
      <button class="session-list__btn" @click="confirmingKill = null">Cancel</button>
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
.session-list__create {
  display: flex;
  gap: 0.4rem;
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
  gap: 2px;
}
.session-list__row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0.5rem;
  border-radius: 4px;
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
.session-list__name {
  font-family: var(--app-code-font);
  font-size: 0.85rem;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.session-list__meta {
  color: var(--th-text-lo);
  font-size: 0.75rem;
  white-space: nowrap;
}
.session-list__actions {
  display: none;
  gap: 0.2rem;
}
.session-list__row:hover .session-list__actions {
  display: inline-flex;
}
.session-list__icon-btn {
  background: none;
  border: none;
  color: var(--th-text-mid);
  cursor: pointer;
  font-size: 0.85rem;
  padding: 0 0.2rem;
}
.session-list__icon-btn--danger:hover {
  color: var(--th-danger);
}
.session-list__rename {
  display: flex;
  gap: 0.4rem;
  width: 100%;
}
.session-list__confirm {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8rem;
  color: var(--th-text-hi);
  border-top: 1px solid var(--th-border);
  padding-top: 0.5rem;
}
</style>
