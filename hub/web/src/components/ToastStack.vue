<script setup lang="ts">
import { dismiss, useToasts } from '../toasts'

const toasts = useToasts()

const LEVEL_TAG: Record<string, string> = {
  error: 'ERROR',
  warning: 'WARN',
  info: 'INFO',
}
</script>

<template>
  <!-- aria-live so screen readers announce arrivals; the stack grows upward:
       the container is anchored at the bottom and each new toast is appended,
       pushing the earlier ones up. -->
  <div class="toast-stack" role="region" aria-label="Notifications" aria-live="polite">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="toast"
      :class="`toast--${t.level}`"
      :role="t.level === 'error' ? 'alert' : 'status'"
      aria-atomic="true"
    >
      <span class="toast__tag">{{ LEVEL_TAG[t.level] }}</span>
      <span class="toast__message">{{ t.message }}</span>
      <button class="toast__close" type="button" aria-label="Dismiss notification" @click="dismiss(t.id)">✕</button>
    </div>
  </div>
</template>

<style scoped>
.toast-stack {
  position: absolute;
  bottom: 0.75rem;
  left: 0.75rem;
  right: 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  /* New toasts render above the terminal without stealing focus or clicks
     outside a toast's own box. */
  pointer-events: none;
  z-index: 30;
}
.toast {
  pointer-events: auto;
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 0.5rem;
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-left: 3px solid var(--th-text-mid);
  border-radius: 6px;
  padding: 0.45rem 0.7rem;
  font-size: 0.85rem;
  color: var(--th-text-hi);
}
/* Level distinction is carried by the left bar and the tag color so the
   levels stay distinguishable at a glance, not by wording alone. */
.toast--error {
  border-left-color: var(--th-danger);
}
.toast--warning {
  border-left-color: var(--th-warning);
}
.toast--info {
  border-left-color: var(--th-text-mid);
}
.toast__tag {
  font-size: 0.65rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  color: var(--th-text-lo);
  white-space: nowrap;
}
.toast--error .toast__tag {
  color: var(--th-danger);
}
.toast--warning .toast__tag {
  color: var(--th-warning);
}
.toast__message {
  flex: 1 1 12rem;
  min-width: 0;
  overflow-wrap: anywhere;
}
.toast__close {
  background: none;
  border: none;
  color: var(--th-text-mid);
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0;
  align-self: center;
}
.toast__close:hover {
  color: var(--th-text-hi);
}
</style>
