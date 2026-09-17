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

/** items are the entries a user can choose right now, in the order they appear.
 *
 * A disabled one -- Download over a directory -- is not a stop: a menu that
 * lands the focus on something that cannot be chosen and then moves nowhere is
 * the same as no focus at all. */
function items(): HTMLButtonElement[] {
  const el = menu.value
  if (!el) return []
  return Array.from(el.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)'))
}

/** focusItem puts the focus on one of them, wrapping at either end. */
function focusItem(index: number) {
  const found = items()
  if (found.length === 0) return
  found[((index % found.length) + found.length) % found.length].focus()
}

/**
 * onKeydown answers the keys a menu is expected to answer.
 *
 * The focus is one of these keys' business at all because the menu takes it when
 * it opens: a menu that is read but not reachable is a menu a keyboard user
 * cannot act on, and every action here -- rename, delete, download -- is
 * otherwise reachable only with a pointer. Which entry the move is measured from
 * is the focus itself rather than a second index kept beside it, so there is
 * nothing to fall out of step with what is on screen.
 *
 * Escape is answered wherever the key is pressed, as it was before; the moves
 * only apply while the focus is on an item, since an arrow aimed at something
 * else is that something else's key.
 */
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    emit('close')
    return
  }
  const found = items()
  const current = found.indexOf(document.activeElement as HTMLButtonElement)
  if (current < 0) return
  const move: Record<string, number> = {
    ArrowDown: current + 1,
    ArrowUp: current - 1,
    Home: 0,
    End: found.length - 1,
  }
  const to = move[event.key]
  if (to === undefined) {
    // Tab is the one other key that means something here: a menu is not a form,
    // so leaving it is dismissing it rather than a step to its neighbour.
    if (event.key === 'Tab') emit('close')
    return
  }
  // The browser's own use of these keys -- scrolling the page under a menu that
  // is anchored to a point on it -- is not what a menu's arrow keys mean.
  event.preventDefault()
  focusItem(to)
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
  // Opened, and taken: the focus moves into the menu for every way of opening
  // it, which is what makes the keyboard route -- Shift+F10 or the menu key on
  // the row -- land somewhere the user can act from. Closing hands it back; see
  // the overlay's closeMenu.
  focusItem(0)
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
