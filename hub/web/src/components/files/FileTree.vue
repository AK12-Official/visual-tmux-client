<script setup lang="ts">
// The tree's root list: the current directory's entries, or why there are none.
import { computed } from 'vue'
import type { Entry } from '../../files/api'
import { cachedChildren, isTruncated, type TreeState } from '../../files/tree'

const props = defineProps<{
  path: string
  state: TreeState
  current: string
}>()

const emit = defineEmits<{
  (e: 'open', entry: Entry, path: string): void
  (e: 'toggle', path: string): void
  (e: 'context', event: MouseEvent, entry: Entry, path: string): void
}>()

const entries = computed(() => cachedChildren(props.state, props.path) ?? [])
const truncated = computed(() => isTruncated(props.state, props.path))

function forwardOpen(entry: Entry, path: string) {
  emit('open', entry, path)
}

function forwardToggle(path: string) {
  emit('toggle', path)
}

function forwardContext(event: MouseEvent, entry: Entry, path: string) {
  emit('context', event, entry, path)
}
</script>

<template>
  <div class="tree">
    <p v-if="entries.length === 0" class="tree__empty">This directory is empty.</p>
    <template v-else>
      <p v-if="truncated" class="tree__note">
        More entries than the listing limit — open a subdirectory to see the rest.
      </p>
      <FileTreeNode
        v-for="entry in entries"
        :key="entry.name"
        :entry="entry"
        :parent="path"
        :depth="0"
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
.tree {
  padding: 4px 0;
}

.tree__empty,
.tree__note {
  margin: 6px 10px;
  color: var(--th-text-lo);
  font-size: 11px;
}
</style>
