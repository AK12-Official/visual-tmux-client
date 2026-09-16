<script setup lang="ts">
// One entry in the tree, recursing into itself for an expanded directory. The
// children are rendered from the shared state rather than passed down, so an
// expanded directory keeps its position under its parent.
import { computed } from 'vue'
import type { Entry } from '../../files/api'
import { cachedChildren, isExpanded, isTruncated, type TreeState } from '../../files/tree'

const props = defineProps<{
  entry: Entry
  /** parent is the absolute path of the directory this entry lives in. */
  parent: string
  depth: number
  state: TreeState
  current: string
}>()

const emit = defineEmits<{
  (e: 'open', entry: Entry, path: string): void
  (e: 'toggle', path: string): void
  (e: 'context', event: MouseEvent, entry: Entry, path: string): void
}>()

const path = computed(() =>
  props.parent === '/' ? `/${props.entry.name}` : `${props.parent}/${props.entry.name}`,
)
const expanded = computed(() => props.entry.is_dir && isExpanded(props.state, path.value))
const children = computed(() => cachedChildren(props.state, path.value) ?? [])
const truncated = computed(() => isTruncated(props.state, path.value))

function forwardOpen(entry: Entry, path: string) {
  emit('open', entry, path)
}

function forwardToggle(path: string) {
  emit('toggle', path)
}

function forwardContext(event: MouseEvent, entry: Entry, path: string) {
  emit('context', event, entry, path)
}

function activate() {
  if (props.entry.is_dir) {
    emit('toggle', path.value)
    return
  }
  emit('open', props.entry, path.value)
}
</script>

<template>
  <div class="node">
    <div class="node__row" :class="{ 'node__row--current': entry.is_dir && path === current }">
      <button
        class="node__label"
        type="button"
        :style="{ paddingLeft: `${6 + depth * 12}px` }"
        :title="path"
        @click="activate"
        @contextmenu.prevent="emit('context', $event, entry, path)"
      >
        <span class="node__glyph" aria-hidden="true">{{
          entry.is_dir ? (expanded ? '▾' : '▸') : '·'
        }}</span>
        <span class="node__name" :class="{ 'node__name--dir': entry.is_dir }">{{ entry.name }}</span>
      </button>
    </div>

    <template v-if="expanded">
      <p v-if="truncated" class="node__note" :style="{ paddingLeft: `${18 + depth * 12}px` }">
        More entries than the listing limit — open a subdirectory to see the rest.
      </p>
      <FileTreeNode
        v-for="child in children"
        :key="child.name"
        :entry="child"
        :parent="path"
        :depth="depth + 1"
        :state="state"
        :current="current"
        @open="forwardOpen"
        @toggle="forwardToggle"
        @context="forwardContext"
      />
    </template>
  </div>
</template>

<style scoped>
.node__row {
  display: flex;
}

.node__row--current .node__name {
  color: var(--th-accent);
}

.node__label {
  display: flex;
  flex: 1;
  gap: 4px;
  align-items: center;
  padding: 3px 6px;
  overflow: hidden;
  color: var(--th-text-mid);
  font-size: 12px;
  text-align: left;
  background: none;
  border: none;
  cursor: pointer;
}

.node__label:hover {
  background: var(--th-surface);
}

.node__glyph {
  width: 10px;
  color: var(--th-text-lo);
}

.node__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node__name--dir {
  color: var(--th-text-hi);
}

.node__note {
  margin: 2px 0;
  color: var(--th-text-lo);
  font-size: 11px;
}
</style>
