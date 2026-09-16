<script setup lang="ts">
// Right-click menu for a tree entry. It renders nothing itself when closed; the
// overlay owns when it is open.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ x: number; y: number; name: string; isDir: boolean }>()
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

const menu = ref<HTMLElement | null>(null)
const offset = ref({ left: props.x, top: props.y })

// The menu opens at the pointer and is then pulled back inside the viewport. One
// opened near the bottom edge would otherwise put its final entries -- Delete
// among them -- below the window with nothing to scroll.
function place() {
  const el = menu.value
  if (!el) return
  const { width, height } = el.getBoundingClientRect()
  offset.value = {
    left: Math.max(0, Math.min(props.x, window.innerWidth - width)),
    top: Math.max(0, Math.min(props.y, window.innerHeight - height)),
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close')
}

/** onPointerDown dismisses the menu when the press lands anywhere else. */
function onPointerDown(event: PointerEvent) {
  if (menu.value && !menu.value.contains(event.target as Node)) emit('close')
}

// A menu is anchored to a place on screen, so anything that moves what is under
// that place invalidates it: the tree scrolls, the window resizes. Leaving it up
// would leave it naming an entry the user is no longer looking at.
function onViewportChange() {
  emit('close')
}

onMounted(() => {
  place()
  document.addEventListener('keydown', onKeydown)
  // Capture phase, so a press that a descendant stops still dismisses the menu.
  document.addEventListener('pointerdown', onPointerDown, true)
  // Scroll does not bubble, so the capture phase is what catches a scroll inside
  // the tree or the editor.
  window.addEventListener('scroll', onViewportChange, { capture: true, passive: true })
  window.addEventListener('resize', onViewportChange)
})

// Watching the point as well as placing on mount, because the overlay can reuse
// this instance for a second right-click: a menu left at the first position
// would name an entry it is no longer next to, and every action on it -- Delete
// among them -- would then apply to the wrong entry.
//
// The flush is 'post' so the box being measured is the one just rendered. A
// pre-flush watcher measures the previous entry's title, which is wider or
// narrower than the new one, and clamps against a size that no longer applies.
watch(() => [props.x, props.y], place, { flush: 'post' })

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  document.removeEventListener('pointerdown', onPointerDown, true)
  window.removeEventListener('scroll', onViewportChange, { capture: true })
  window.removeEventListener('resize', onViewportChange)
})
</script>

<template>
  <div
    ref="menu"
    class="menu"
    :style="{ left: `${offset.left}px`, top: `${offset.top}px` }"
    role="menu"
    @contextmenu.prevent
  >
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
