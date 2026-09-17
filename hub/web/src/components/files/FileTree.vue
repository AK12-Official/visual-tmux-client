<script setup lang="ts">
// The tree, as a flat list of the rows currently visible. There is no recursive
// component: the state is already keyed by path, so which rows show is just a
// walk over what is expanded (see visibleRows).
import { computed, ref } from 'vue'

import type { Entry } from '../../files/api'
import { cachedChildren, isTruncated, visibleRows, type TreeState } from '../../files/tree'

const props = defineProps<{
  path: string
  state: TreeState
  current: string
}>()

const emit = defineEmits<{
  (e: 'open', entry: Entry, path: string): void
  (e: 'select', path: string): void
  (e: 'toggle', path: string): void
  (e: 'context', event: MouseEvent, entry: Entry, path: string, opener: HTMLElement | null): void
}>()

// Two affordances per directory, because they are two different things: the
// caret shows or hides what is inside, and the name makes it the directory the
// manager is looking at.
function activate(row: { entry: Entry; path: string }) {
  if (row.entry.is_dir) {
    emit('select', row.path)
    return
  }
  emit('open', row.entry, row.path)
}

// root is the element the rows are rendered into, which is what the focus
// helpers below search. A ref rather than a document query, so the tree finds its
// own rows and not another manager's.
const root = ref<HTMLElement | null>(null)

const rows = computed(() => visibleRows(props.state, props.path))
const truncated = computed(() => isTruncated(props.state, props.path))
// "Empty" is a claim about the directory, so it is only made about one that has
// actually been read. A listing that has been dropped and not re-read -- which is
// what a failed reload leaves behind -- has nothing to show and no reason to
// claim there is nothing in there.
const loaded = computed(() => cachedChildren(props.state, props.path) !== undefined)

/** onContext passes the row itself along with the event.
 *
 * The menu the overlay opens is anchored to a place on screen, and a contextmenu
 * the browser raised for the keyboard -- Shift+F10, or the menu key -- carries
 * no pointer position to anchor it to. The row is what it is anchored to then,
 * and where the focus goes back when the menu closes, so the overlay is given
 * the element rather than having to find it again by name. */
function onContext(event: MouseEvent, entry: Entry, path: string) {
  const opener = event.currentTarget
  emit('context', event, entry, path, opener instanceof HTMLElement ? opener : null)
}

/** labels are the row labels currently rendered, in the order they appear. */
function labels(): HTMLElement[] {
  const el = root.value
  if (!el) return []
  return Array.from(el.querySelectorAll<HTMLElement>('.tree__label'))
}

/**
 * neighbourOf is the path of the row that stands where this one will have stood,
 * or null when the row is not rendered.
 *
 * The manager asks this before it removes an entry, so that it can put the
 * keyboard where the entry was afterwards: a row that is gone cannot say what
 * stood next to it. A path rather than an index, because an index means nothing
 * in a listing that has been re-read since.
 *
 * The row that *stands there* is the next one that is not inside the removed
 * entry, which the row's own depth decides: what follows a directory in a flat
 * listing is its first child, and deleting the directory takes that child too, so
 * the search skips anything deeper and falls back to the nearest row shallower
 * than it -- the parent, or a previous sibling of that parent. The entry named
 * may itself be gone by the time the answer arrives, which is why the caller has
 * a fallback of its own.
 */
function neighbourOf(path: string): string | null {
  const found = labels()
  const at = found.findIndex((label) => label.dataset.path === path)
  if (at < 0) return null
  const depth = depthOf(found[at])
  for (let i = at + 1; i < found.length; i++) {
    if (depthOf(found[i]) <= depth) return found[i].dataset.path ?? null
  }
  // Nothing follows it, so the nearest row that is not inside it: the row it sat
  // beside, or the one it sat under.
  for (let i = at - 1; i >= 0; i--) {
    if (depthOf(found[i]) <= depth) return found[i].dataset.path ?? null
  }
  return null
}

/** depthOf reads the indent a label was rendered at. */
function depthOf(label: HTMLElement): number {
  const depth = Number(label.dataset.depth ?? '')
  return Number.isFinite(depth) ? depth : 0
}

/** focusFirstRow focuses the first row, and reports whether there was one. */
function focusFirstRow(): boolean {
  return focusRowAt(0)
}

/** focusRowAt focuses the row at an index, clamped to what is rendered, and
 * reports whether there was anything to focus. */
function focusRowAt(index: number): boolean {
  const found = labels()
  if (found.length === 0) return false
  const at = Math.max(0, Math.min(index, found.length - 1))
  found[at].focus({ preventScroll: true })
  return true
}

/** focusPath focuses the row for one path, and reports whether it was there. */
function focusPath(path: string): boolean {
  const found = labels().find((label) => label.dataset.path === path)
  if (!found) return false
  found.focus({ preventScroll: true })
  return true
}

defineExpose({ neighbourOf, focusFirstRow, focusPath })
</script>

<template>
  <div ref="root" class="tree">
    <p v-if="rows.length === 0 && loaded" class="tree__empty">This directory is empty.</p>
    <template v-else>
      <p v-if="truncated" class="tree__note">
        More entries than the listing limit — open a subdirectory to see the rest.
      </p>
      <div
        v-for="row in rows"
        :key="row.path"
        class="tree__row"
        :class="{ 'tree__row--current': row.entry.is_dir && row.path === current }"
        :style="{ paddingLeft: `${4 + row.depth * 12}px` }"
      >
        <button
          v-if="row.entry.is_dir"
          class="tree__caret"
          type="button"
          :aria-expanded="row.expanded"
          :aria-label="row.expanded ? `Collapse ${row.entry.name}` : `Expand ${row.entry.name}`"
          @click="emit('toggle', row.path)"
        >{{ row.expanded ? '▾' : '▸' }}</button>
        <span v-else class="tree__caret tree__caret--none" aria-hidden="true"></span>
        <button
          class="tree__label"
          type="button"
          :data-path="row.path"
          :data-depth="row.depth"
          :title="row.path"
          @click="activate(row)"
          @contextmenu.prevent="onContext($event, row.entry, row.path)"
        >
          <span class="tree__name" :class="{ 'tree__name--dir': row.entry.is_dir }">{{
            row.entry.name
          }}</span>
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.tree {
  padding: 4px 0;
}

.tree__empty,
.tree__note {
  margin: 6px 10px;
  color: var(--th-text-lo);
  font-size: 11px;
}

.tree__row {
  display: flex;
  align-items: center;
}

.tree__row:hover {
  background: var(--th-surface);
}

.tree__row--current .tree__name {
  color: var(--th-accent);
}

.tree__caret {
  flex: 0 0 14px;
  color: var(--th-text-lo);
  font-size: 10px;
  text-align: center;
  background: none;
  border: none;
  cursor: pointer;
}

.tree__caret--none {
  cursor: default;
}

.tree__label {
  display: flex;
  flex: 1;
  min-width: 0;
  padding: 3px 6px 3px 0;
  color: var(--th-text-mid);
  font-size: 12px;
  text-align: left;
  background: none;
  border: none;
  cursor: pointer;
}

.tree__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree__name--dir {
  color: var(--th-text-hi);
}
</style>
