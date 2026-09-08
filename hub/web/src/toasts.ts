// Toast notification store. A module-level reactive store rather than props:
// emitters live at three depths (App.vue handlers, TerminalView, and
// terminal.ts, which is not a component at all), so threading a callback
// through every layer would cost more than the store costs.
//
// Contract (web-notifications spec): messages coexist as a stack where a new
// one pushes earlier ones aside, each carries exactly one level with distinct
// visual treatment, each self-expires after a level-appropriate lifetime, the
// user may dismiss any toast early, and the stack is bounded.

import { ref } from 'vue'

export type ToastLevel = 'error' | 'warning' | 'info'

export interface Toast {
  id: number
  level: ToastLevel
  message: string
}

// Lifetimes per level: errors live longest because they are the ones a user
// may still want to re-read; info is ephemeral by nature.
const LIFETIME_MS: Record<ToastLevel, number> = {
  error: 8000,
  warning: 5000,
  info: 3000,
}

// Stack bound: beyond this, the oldest toast is dropped to make room — the
// newest messages are the ones the user is most likely still acting on.
const MAX_TOASTS = 5

const toasts = ref<Toast[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

/** notify raises a toast. The newest toast joins the bottom of the stack. */
export function notify(level: ToastLevel, message: string): void {
  const toast: Toast = { id: nextId++, level, message }
  toasts.value.push(toast)
  while (toasts.value.length > MAX_TOASTS) {
    const dropped = toasts.value.shift()
    if (dropped) clearTimer(dropped.id)
  }
  timers.set(toast.id, setTimeout(() => dismiss(toast.id), LIFETIME_MS[level]))
}

function clearTimer(id: number): void {
  const timer = timers.get(id)
  if (timer) {
    clearTimeout(timer)
    timers.delete(id)
  }
}

/** dismiss removes one toast immediately (manual close or expiry). */
export function dismiss(id: number): void {
  clearTimer(id)
  const i = toasts.value.findIndex((t) => t.id === id)
  if (i >= 0) toasts.value.splice(i, 1)
}

/** useToasts exposes the reactive stack for rendering. */
export function useToasts() {
  return toasts
}
