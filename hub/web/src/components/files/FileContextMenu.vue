<script setup lang="ts">
// Right-click menu for a tree entry. It renders nothing itself when closed; the
// overlay owns when it is open.
import { onBeforeUnmount, onMounted } from 'vue'

defineProps<{ x: number; y: number; name: string; isDir: boolean }>()
const emit = defineEmits<{
  (e: 'new-file'): void
  (e: 'new-dir'): void
  (e: 'rename'): void
  (e: 'delete'): void
  (e: 'download'): void
  (e: 'copy-path'): void
  (e: 'insert-path'): void
  (e: 'close'): void
}>()

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close')
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="menu" :style="{ left: `${x}px`, top: `${y}px` }" role="menu" @contextmenu.prevent>
    <div class="menu__title" :title="name">{{ name }}</div>
    <button class="menu__item" type="button" role="menuitem" @click="emit('new-file')">
      New file
    </button>
    <button class="menu__item" type="button" role="menuitem" @click="emit('new-dir')">
      New directory
    </button>
    <button class="menu__item" type="button" role="menuitem" @click="emit('rename')">
      Rename
    </button>
    <button class="menu__item" type="button" role="menuitem" @click="emit('copy-path')">
      Copy path
    </button>
    <button class="menu__item" type="button" role="menuitem" @click="emit('insert-path')">
      Insert path into terminal
    </button>
    <button
      class="menu__item"
      type="button"
      role="menuitem"
      :disabled="isDir"
      @click="emit('download')"
    >
      Download
    </button>
    <button
      class="menu__item menu__item--danger"
      type="button"
      role="menuitem"
      @click="emit('delete')"
    >
      Delete
    </button>
  </div>
</template>

<style scoped>
.menu {
  position: fixed;
  z-index: 40;
  min-width: 200px;
  padding: 4px;
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  box-shadow: 0 8px 24px rgb(0 0 0 / 45%);
}

.menu__title {
  padding: 6px 10px;
  overflow: hidden;
  color: var(--th-text-lo);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
  border-bottom: 1px solid var(--th-border);
  margin-bottom: 4px;
}

.menu__item {
  display: block;
  width: 100%;
  padding: 6px 10px;
  color: var(--th-text-hi);
  font-size: 12px;
  text-align: left;
  background: none;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}

.menu__item:hover:not(:disabled) {
  background: var(--th-surface);
}

.menu__item:disabled {
  color: var(--th-text-lo);
  cursor: default;
}

.menu__item--danger {
  color: var(--th-danger);
}
</style>
