<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { TerminalSession, type ConnState, type NoticeLevel } from '../terminal'

const props = defineProps<{ session: string }>()
const emit = defineEmits<{
  (e: 'state', session: string, state: ConnState): void
  (e: 'notice', message: string, level?: NoticeLevel): void
}>()

const el = ref<HTMLDivElement | null>(null)
let session: TerminalSession | null = null

onMounted(() => {
  if (!el.value) return
  try {
    session = new TerminalSession(el.value, props.session, {
      onState: (state) => emit('state', props.session, state),
      onNotice: (message, level) => emit('notice', message, level),
    })
  } catch (err) {
    emit('notice', `Terminal init failed: ${String(err)}`)
  }
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
/* The terminal must fill its flex parent. `height: 100%` does NOT work here
   because the parent (.app__main) is a flex item with no explicit height, so
   `100%` resolves to auto and the surface collapses to zero height — which
   makes xterm render nothing and refuse focus. Instead grow via flex:1 and
   absolutely fill the surface so xterm always gets a definite box. */
.terminal-view {
  flex: 1;
  min-width: 0;
  min-height: 0;
  position: relative;
  overflow: hidden;
}
.terminal-view__surface {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 6px;
  box-sizing: border-box;
}
</style>
