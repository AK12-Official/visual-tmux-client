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
import { getConfig } from './config'

export type ToastLevel = 'error' | 'warning' | 'info'

export interface Toast {
  id: number
  level: ToastLevel
  message: string
}

const toasts = ref<Toast[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

/** notify raises a toast. The newest toast joins the bottom of the stack. */
export function notify(level: ToastLevel, message: string): void {
  const notifs = getConfig().web.notifications
  const toast: Toast = { id: nextId++, level, message }
  toasts.value.push(toast)
  while (toasts.value.length > notifs.max_toasts) {
    const dropped = toasts.value.shift()
    if (dropped) clearTimer(dropped.id)
  }
  const lifetime =
    level === 'error'
      ? notifs.error_lifetime
      : level === 'warning'
        ? notifs.warning_lifetime
        : notifs.info_lifetime

  timers.set(toast.id, setTimeout(() => dismiss(toast.id), lifetime))
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

export function resetToastsForTest(): void {
  for (const timer of timers.values()) {
    clearTimeout(timer)
  }
  timers.clear()
  toasts.value = []
  nextId = 1
}
