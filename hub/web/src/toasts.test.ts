import test from 'node:test'
import assert from 'node:assert/strict'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { notify, dismiss, useToasts, resetToastsForTest } from './toasts'

const customConfig: ClientConfig = {
  version: 1,
  web: {
    session_poll_interval: 2000,
    activity_decay: 1000,
    activity_throttle: 250,
    resize_debounce: 50,
    reconnect: {
      initial_delay: 200,
      max_delay: 1500,
    },
    terminal: {
      scrollback: 1000,
      font_size: 15,
      min_font_size: 10,
      max_font_size: 20,
    },
    notifications: {
      max_toasts: 2,
      error_lifetime: 60,
      warning_lifetime: 35,
      info_lifetime: 15,
    },
  },
}

test('toasts module does not evaluate config at module import time', () => {
  resetConfigForTest()
  resetToastsForTest()
  // Calling notify without config should fail gracefully, demonstrating dynamic evaluation
  assert.throws(() => notify('info', 'test'), /not been initialized/)
})

test('toasts honor non-default stack bound by evicting oldest', () => {
  setConfig(customConfig)
  resetToastsForTest()
  const toasts = useToasts()

  notify('info', 'msg 1')
  notify('info', 'msg 2')
  assert.equal(toasts.value.length, 2)
  assert.equal(toasts.value[0].message, 'msg 1')
  assert.equal(toasts.value[1].message, 'msg 2')

  // Exceed bound: max_toasts is 2, msg 1 should be dropped
  notify('warning', 'msg 3')
  assert.equal(toasts.value.length, 2)
  assert.equal(toasts.value[0].message, 'msg 2')
  assert.equal(toasts.value[1].message, 'msg 3')
  assert.equal(toasts.value[1].level, 'warning')
})

test('toasts support manual dismissal', () => {
  setConfig(customConfig)
  resetToastsForTest()
  const toasts = useToasts()

  notify('error', 'critical error')
  assert.equal(toasts.value.length, 1)
  const id = toasts.value[0].id

  dismiss(id)
  assert.equal(toasts.value.length, 0)
})

test('toasts expire according to configured level lifetimes', async () => {
  setConfig(customConfig)
  resetToastsForTest()
  const toasts = useToasts()

  notify('info', 'ephemeral info')
  assert.equal(toasts.value.length, 1)

  // info lifetime is 15 ms, wait 30 ms
  await new Promise((resolve) => setTimeout(resolve, 30))
  assert.equal(toasts.value.length, 0)
})
