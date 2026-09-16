<script setup lang="ts">
// The tree, as a flat list of the rows currently visible. There is no recursive
// component: the state is already keyed by path, so which rows show is just a
// walk over what is expanded (see visibleRows).
import { computed } from 'vue'

import type { Entry } from '../../files/api'
import { isTruncated, visibleRows, type TreeState } from '../../files/tree'

const props = defineProps<{
  path: string
  state: TreeState
  current: string
}>()

const emit = defineEmits<{
  (e: 'open', entry: Entry, path: string): void
  (e: 'select', path: string): void
  (e: 'toggle', path: string): void
  (e: 'context', event: MouseEvent, entry: Entry, path: string): void
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

const rows = computed(() => visibleRows(props.state, props.path))
const truncated = computed(() => isTruncated(props.state, props.path))

function onContext(event: MouseEvent, entry: Entry, path: string) {
  emit('context', event, entry, path)
}
</script>

<template>
  <div class="tree">
    <p v-if="rows.length === 0" class="tree__empty">This directory is empty.</p>
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
