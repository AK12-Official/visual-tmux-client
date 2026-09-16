<script setup lang="ts">
import { isDirty, type OpenFile } from '../../files/tabs'

defineProps<{ files: OpenFile[]; active: string | null }>()
const emit = defineEmits<{
  (e: 'select', path: string): void
  (e: 'close', path: string): void
}>()
</script>

<template>
  <div class="tabs" role="tablist">
    <div
      v-for="file in files"
      :key="file.path"
      class="tabs__tab"
      :class="{ 'tabs__tab--active': file.path === active }"
    >
      <button
        class="tabs__label"
        type="button"
        role="tab"
        :aria-selected="file.path === active"
        :title="file.path"
        @click="emit('select', file.path)"
      >
        <span class="tabs__name">{{ file.name }}</span>
        <!-- The marker is the only thing outside the editor that says a file has
             unsaved edits, so it is announced rather than merely drawn. -->
        <span
          v-if="isDirty(file)"
          class="tabs__dirty"
          role="img"
          aria-label="unsaved changes"
          title="unsaved changes"
        >●</span>
      </button>
      <button
        class="tabs__close"
        type="button"
        :aria-label="`Close ${file.name}`"
        @click="emit('close', file.path)"
      >×</button>
    </div>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  align-items: stretch;
  gap: 1px;
  overflow-x: auto;
  background: var(--th-surface);
  border-bottom: 1px solid var(--th-border);
}

.tabs__tab {
  display: flex;
  align-items: center;
  border-right: 1px solid var(--th-border);
  background: var(--th-surface);
}

.tabs__tab--active {
  background: var(--app-bg);
}

.tabs__label {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 4px 6px 10px;
  font-size: 12px;
  color: var(--th-text-mid);
  background: none;
  border: none;
  cursor: pointer;
}

.tabs__tab--active .tabs__label {
  color: var(--th-text-hi);
}

.tabs__name {
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tabs__dirty {
  color: var(--th-warning);
  font-size: 10px;
}

.tabs__close {
  padding: 6px 8px;
  font-size: 14px;
  line-height: 1;
  color: var(--th-text-lo);
  background: none;
  border: none;
  cursor: pointer;
}

.tabs__close:hover {
  color: var(--th-text-hi);
}
</style>
