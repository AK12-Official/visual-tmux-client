import test from 'node:test'
import assert from 'node:assert/strict'
import {
  validateClientConfig,
  setConfig,
  getConfig,
  resetConfigForTest,
  fetchClientConfig,
  type ClientConfig,
} from './config'

const validPayload: ClientConfig = {
  version: 1,
  web: {
    session_poll_interval: 5000,
    activity_decay: 2000,
    activity_throttle: 500,
    resize_debounce: 100,
    reconnect: {
      initial_delay: 500,
      max_delay: 3000,
    },
    terminal: {
      scrollback: 5000,
      font_size: 13,
      min_font_size: 8,
      max_font_size: 24,
    },
    notifications: {
      max_toasts: 5,
      error_lifetime: 8000,
      warning_lifetime: 5000,
      info_lifetime: 3000,
    },
  },
}

test('validateClientConfig accepts valid payload and freezes result', () => {
  const cfg = validateClientConfig(validPayload)
  assert.equal(cfg.version, 1)
  assert.equal(cfg.web.session_poll_interval, 5000)
  assert.equal(cfg.web.terminal.font_size, 13)

  setConfig(cfg)
  const retrieved = getConfig()
  assert.equal(retrieved.version, 1)
  assert.ok(Object.isFrozen(retrieved))
  assert.ok(Object.isFrozen(retrieved.web))
  assert.ok(Object.isFrozen(retrieved.web.reconnect))
})

test('getConfig throws before initialization', () => {
  resetConfigForTest()
  assert.throws(() => getConfig(), /not been initialized/)
})

test('validateClientConfig rejects non-objects and wrong version', () => {
  assert.throws(() => validateClientConfig(null), /must be a JSON object/)
  assert.throws(() => validateClientConfig(''), /must be a JSON object/)
  assert.throws(() => validateClientConfig({ version: 2, web: validPayload.web }), /expected version 1/)
  assert.throws(() => validateClientConfig({ version: 1 }), /missing or invalid "web" object/)
})

test('validateClientConfig rejects invalid web durations and limits', () => {
  // poll interval < 100
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: { ...validPayload.web, session_poll_interval: 50 },
      }),
    /session_poll_interval must be an integer >= 100/,
  )

  // activity_decay < 100
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: { ...validPayload.web, activity_decay: 10 },
      }),
    /activity_decay must be an integer >= 100/,
  )

  // activity_throttle < 0
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: { ...validPayload.web, activity_throttle: -1 },
      }),
    /activity_throttle must be an integer >= 0/,
  )

  // reconnect max_delay < initial_delay
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: {
          ...validPayload.web,
          reconnect: { initial_delay: 500, max_delay: 200 },
        },
      }),
    /reconnect.max_delay must be an integer >= initial_delay/,
  )

  // terminal min_font_size < 6
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: {
          ...validPayload.web,
          terminal: { ...validPayload.web.terminal, min_font_size: 4 },
        },
      }),
    /terminal.min_font_size must be an integer >= 6/,
  )

  // terminal font_size < min_font_size
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: {
          ...validPayload.web,
          terminal: { ...validPayload.web.terminal, min_font_size: 10, font_size: 8 },
        },
      }),
    /terminal.font_size must be between min_font_size and max_font_size/,
  )

  // notifications max_toasts < 1
  assert.throws(
    () =>
      validateClientConfig({
        ...validPayload,
        web: {
          ...validPayload.web,
          notifications: { ...validPayload.web.notifications, max_toasts: 0 },
        },
      }),
    /notifications.max_toasts must be an integer >= 1/,
  )
})

test('fetchClientConfig loads and validates config over HTTP', async () => {
  resetConfigForTest()
  const mockFetch: typeof fetch = async () => {
    return {
      ok: true,
      status: 200,
      json: async () => validPayload,
    } as Response
  }

  const loaded = await fetchClientConfig(mockFetch)
  assert.equal(loaded.version, 1)
  assert.equal(getConfig().web.session_poll_interval, 5000)
})

test('fetchClientConfig rejects HTTP error responses', async () => {
  resetConfigForTest()
  const mockFetch: typeof fetch = async () => {
    return {
      ok: false,
      status: 500,
      json: async () => ({}),
    } as Response
  }

  await assert.rejects(
    () => fetchClientConfig(mockFetch),
    /Failed to load client configuration \(HTTP 500\)/,
  )
  assert.throws(() => getConfig(), /not been initialized/)
})

test('bootstrapApp handles failure, retry, and single mount guarantee', async () => {
  const { bootstrapApp, resetBootstrapForTest } = await import('./bootstrap')
  resetBootstrapForTest()
  resetConfigForTest()

  let attempts = 0
  const flakyFetch: typeof fetch = async () => {
    attempts++
    if (attempts === 1) {
      return { ok: false, status: 503, json: async () => ({}) } as Response
    }
    return { ok: true, status: 200, json: async () => validPayload } as Response
  }

  let mountCount = 0
  const mockMount = () => {
    mountCount++
  }

  const mockBtn = { disabled: false, onclick: null as null | (() => void) }
  const mockEl = {
    innerHTML: '',
    querySelector(selector: string) {
      if (selector === '#vtc-retry-btn') return mockBtn
      return null
    },
  } as unknown as HTMLElement

  // First attempt: should fail and render error UI
  const firstRes = await bootstrapApp(mockEl, flakyFetch, mockMount)
  assert.equal(firstRes, false)
  assert.equal(mountCount, 0)
  assert.ok(mockEl.innerHTML.includes('Configuration Error'))
  assert.ok(typeof mockBtn.onclick === 'function')

  // Retry clicked: should succeed and mount
  mockBtn.onclick()
  // Wait a microtask tick for async retry to complete
  await new Promise((resolve) => setTimeout(resolve, 10))
  assert.equal(mountCount, 1)

  // Repeated bootstrapApp call: should be a no-op, mountCount remains 1
  const secondRes = await bootstrapApp(mockEl, flakyFetch, mockMount)
  assert.equal(secondRes, true)
  assert.equal(mountCount, 1)
})

