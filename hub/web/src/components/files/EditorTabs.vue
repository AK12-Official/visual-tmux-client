<script setup lang="ts">
// The tab strip. It declares itself a tablist, which is a promise about how it
// behaves as well as about what it is: one stop in the tab order rather than one
// per tab, the arrows moving between them, a way to close one without leaving the
// keyboard, and every tab named so that the panel can say which one it is
// showing.
import { ref } from 'vue'

import { isDirty, panelElementId, tabElementId, type OpenFile } from '../../files/tabs'

defineProps<{ files: OpenFile[]; active: string | null }>()
const emit = defineEmits<{
  (e: 'select', path: string): void
  (e: 'close', path: string): void
}>()

const root = ref<HTMLElement | null>(null)

/** focusActiveTab puts the focus on the tab the manager says is active -- the one
 * in the tab order -- and reports whether there was one to put it on.
 *
 * It is how the keyboard is put back after a tab is closed: the element that had
 * the focus is removed with its tab, and the browser leaves the focus on the
 * document body. There is always an active tab while there are tabs at all --
 * the overlay keeps its active path pointing at one that is open -- and the
 * manager has a destination of its own for the case where there is none, so a
 * tab that cannot be found is reported rather than guessed at. */
function focusActiveTab(): boolean {
  const found = labels()
  const active = found.find((label) => label.tabIndex === 0)
  if (!active) return false
  active.focus({ preventScroll: true })
  return true
}

/** labels are the tab buttons rendered right now, in the order they appear. */
function labels(): HTMLButtonElement[] {
  const el = root.value
  if (!el) return []
  return Array.from(el.querySelectorAll<HTMLButtonElement>('.tabs__label'))
}

/**
 * onKeydown answers the keys a tablist is expected to answer.
 *
 * The tab it measures from is the focus rather than the selection: those are the
 * same thing whenever the focus is in the strip, because the active tab is the
 * one in the tab order, and the focus is what the arrows should move. Moving it
 * also selects, which is the pattern for tabs whose panels are already loaded --
 * and every open file's editor is loaded, deliberately, so that switching away
 * does not take its undo history with it. What is selected is read from the
 * button that was focused, so a selection cannot drift from the focus.
 */
function onKeydown(event: KeyboardEvent) {
  const found = labels()
  const current = found.findIndex((tab) => tab === document.activeElement)
  if (current < 0) return
  const path = found[current].dataset.path
  if (event.key === 'Delete') {
    if (path !== undefined) emit('close', path)
    return
  }
  const move: Record<string, number> = {
    ArrowRight: current + 1,
    ArrowLeft: current - 1,
    Home: 0,
    End: found.length - 1,
  }
  const to = move[event.key]
  if (to === undefined) return
  event.preventDefault()
  const at = ((to % found.length) + found.length) % found.length
  found[at].focus()
  const next = found[at].dataset.path
  if (next !== undefined) emit('select', next)
}
defineExpose({ focusActiveTab })
</script>

<template>
  <div ref="root" class="tabs" role="tablist" @keydown="onKeydown">
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
        :id="tabElementId(file.id)"
        :data-path="file.path"
        :tabindex="file.path === active ? 0 : -1"
        :aria-controls="panelElementId"
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
        tabindex="-1"
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
