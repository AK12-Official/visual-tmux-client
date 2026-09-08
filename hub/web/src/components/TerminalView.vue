<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { TerminalSession, type ConnState, type NoticeLevel } from '../terminal'

const props = defineProps<{
  session: string
  /** Whether the session still exists server-side (from the polled list). */
  alive: boolean
  /** Terminal font size in px, owned and persisted by App.vue. */
  fontSize?: number
}>()
const emit = defineEmits<{
  (e: 'state', session: string, state: ConnState): void
  (e: 'notice', message: string, level?: NoticeLevel): void
}>()

const el = ref<HTMLDivElement | null>(null)
const state = ref<ConnState>('connecting')
// Why the terminal ended, when it did: undefined for a plain exit (a detach,
// or the session finishing), or the server's message for a refused attachment.
const endedDetail = ref<string | null>(null)
let session: TerminalSession | null = null

function attach(): void {
  if (!el.value) return
  endedDetail.value = null
  state.value = 'connecting'
  try {
    session = new TerminalSession(el.value, props.session, {
      onState: (s, detail) => {
        state.value = s
        endedDetail.value = s === 'ended' ? detail ?? null : null
        emit('state', props.session, s)
      },
      onNotice: (message, level) => emit('notice', message, level),
    }, props.fontSize)
  } catch (err) {
    emit('notice', `Terminal init failed: ${String(err)}`)
  }
}

// Reconnect builds a fresh terminal session inside this same component
// instance. The old one is disposed first; KeepAlive keeps the component (and
// this method) reachable, so a detached view is never a dead end.
function reconnect(): void {
  session?.dispose()
  session = null
  attach()
}

onMounted(attach)

// Live font-size changes from the header apply to the live terminal session.
watch(
  () => props.fontSize,
  (px) => {
    if (px !== undefined) session?.setFontSize(px)
  },
)

onBeforeUnmount(() => {
  session?.dispose()
  session = null
})
</script>

<template>
  <div class="terminal-view">
    <div ref="el" class="terminal-view__surface"></div>
    <div v-if="state === 'ended'" class="terminal-view__overlay">
      <div class="terminal-view__ended">
        <template v-if="alive">
          <p class="terminal-view__ended-title">
            {{ endedDetail ? 'Terminal disconnected' : `Detached from “${session}”` }}
          </p>
          <p v-if="endedDetail" class="terminal-view__ended-detail">{{ endedDetail }}</p>
          <p v-else class="terminal-view__ended-detail">The session is still running.</p>
          <button class="terminal-view__reconnect" @click="reconnect">Reconnect</button>
        </template>
        <template v-else>
          <p class="terminal-view__ended-title">Session “{{ session }}” has ended.</p>
          <p v-if="endedDetail" class="terminal-view__ended-detail">{{ endedDetail }}</p>
        </template>
      </div>
    </div>
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
.terminal-view__overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(10, 10, 10, 0.78);
  z-index: 10;
}
.terminal-view__ended {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  max-width: 28rem;
  padding: 1.25rem 1.5rem;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 8px;
  text-align: center;
}
.terminal-view__ended-title {
  margin: 0;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--th-text-hi);
}
.terminal-view__ended-detail {
  margin: 0;
  font-size: 0.8rem;
  color: var(--th-text-mid);
  overflow-wrap: anywhere;
}
.terminal-view__reconnect {
  margin-top: 0.35rem;
  background: var(--th-raised);
  color: var(--th-text-hi);
  border: 1px solid var(--th-accent);
  border-radius: 4px;
  padding: 0.4rem 1rem;
  font-size: 0.85rem;
  cursor: pointer;
}
.terminal-view__reconnect:hover {
  background: var(--th-accent);
  color: #0a0a0a;
}
</style>
