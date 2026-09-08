<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { TerminalSession, type ConnState } from '../terminal'

const props = defineProps<{ session: string }>()
const emit = defineEmits<{
  (e: 'state', state: ConnState): void
  (e: 'notice', message: string): void
}>()

const el = ref<HTMLDivElement | null>(null)
let session: TerminalSession | null = null

onMounted(() => {
  if (!el.value) return
  session = new TerminalSession(el.value, props.session, {
    onState: (state) => emit('state', state),
    onNotice: (message) => emit('notice', message),
  })
})

onBeforeUnmount(() => {
  session?.dispose()
  session = null
})
</script>

<template>
  <div class="terminal-view">
    <div ref="el" class="terminal-view__surface"></div>
  </div>
</template>

<style scoped>
.terminal-view {
  width: 100%;
  height: 100%;
  overflow: hidden;
}
.terminal-view__surface {
  width: 100%;
  height: 100%;
  padding: 6px;
  box-sizing: border-box;
}
</style>
