import test from 'node:test'
import assert from 'node:assert/strict'
import {
  setConfig,
  resetConfigForTest,
  resolveFontSize,
  type ClientConfig,
} from './config'
import {
  computeReconnectDelay,
  shouldThrottleActivity,
  TerminalSession,
  type TerminalHooks,
} from './terminal'

const nonDefaultConfig: ClientConfig = {
  version: 1,
  web: {
    session_poll_interval: 3000,
    activity_decay: 1500,
    activity_throttle: 250,
    resize_debounce: 80,
    reconnect: {
      initial_delay: 200,
      max_delay: 1500,
    },
    terminal: {
      scrollback: 2500,
      font_size: 16,
      min_font_size: 10,
      max_font_size: 22,
    },
    notifications: {
      max_toasts: 3,
      error_lifetime: 6000,
      warning_lifetime: 4000,
      info_lifetime: 2000,
    },
  },
}

test('computeReconnectDelay honors configured initial and max delays', () => {
  setConfig(nonDefaultConfig)
  const reconnectCfg = nonDefaultConfig.web.reconnect

  assert.equal(computeReconnectDelay(0, reconnectCfg), 200)
  assert.equal(computeReconnectDelay(1, reconnectCfg), 400)
  assert.equal(computeReconnectDelay(2, reconnectCfg), 800)
  // 200 * 2^3 = 1600, capped at max_delay = 1500
  assert.equal(computeReconnectDelay(3, reconnectCfg), 1500)
  assert.equal(computeReconnectDelay(4, reconnectCfg), 1500)
})

test('shouldThrottleActivity throttles rapid calls within configured window', () => {
  const throttle = 250
  assert.equal(shouldThrottleActivity(1000, 1100, throttle), true)
  assert.equal(shouldThrottleActivity(1000, 1249, throttle), true)
  assert.equal(shouldThrottleActivity(1000, 1250, throttle), false)
  assert.equal(shouldThrottleActivity(1000, 1500, throttle), false)
})

test('resolveFontSize handles null, invalid, valid, and out-of-bounds storage', () => {
  const termCfg = nonDefaultConfig.web.terminal // min 10, max 22, default 16

  // No storage or empty string -> default font size
  assert.equal(resolveFontSize(null, termCfg), 16)
  assert.equal(resolveFontSize('', termCfg), 16)

  // Illegal storage values -> default font size
  assert.equal(resolveFontSize('invalid', termCfg), 16)
  assert.equal(resolveFontSize('NaN', termCfg), 16)

  // Valid storage values -> parsed font size
  assert.equal(resolveFontSize('14', termCfg), 14)
  assert.equal(resolveFontSize('18', termCfg), 18)

  // Below min_font_size -> clamped to min_font_size (10)
  assert.equal(resolveFontSize('4', termCfg), 10)
  assert.equal(resolveFontSize('9', termCfg), 10)

  // Above max_font_size -> clamped to max_font_size (22)
  assert.equal(resolveFontSize('25', termCfg), 22)
  assert.equal(resolveFontSize('72', termCfg), 22)
})

test('TerminalSession initializes from configuration and setFontSize triggers re-fit', () => {
  setConfig(nonDefaultConfig)

  // Mock DOM globals needed by TerminalSession
  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }
  class MockWebSocket {
    binaryType = 'blob'
    readyState = 0
    onopen: (() => void) | null = null
    onmessage: (() => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    close() {}
    send() {}
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket

  try {
    const mockContainer = {
      clientWidth: 640,
      clientHeight: 480,
    } as HTMLElement

    const hooks: TerminalHooks = {
      onState: () => {},
      onNotice: () => {},
    }

    const session = new TerminalSession(mockContainer, 's1', hooks)
    const termAny = (session as any).term
    const fitAny = (session as any).fit

    // Check configuration consumption
    assert.equal(termAny.options.scrollback, 2500)
    assert.equal(termAny.options.fontSize, 16)

    // Verify setFontSize updates terminal option and calls fit.fit()
    const fitCallsBefore = fitAny.fitCount
    session.setFontSize(20)
    assert.equal(termAny.options.fontSize, 20)
    assert.ok(fitAny.fitCount > fitCallsBefore, 'setFontSize must trigger fit() re-fit')

    session.dispose()
  } finally {
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    resetConfigForTest()
  }
})

test('computeReconnectDelay handles negative, non-integer, and huge values gracefully', () => {
  setConfig(nonDefaultConfig)
  const reconnectCfg = nonDefaultConfig.web.reconnect

  // Negative attempts clamped to 0
  assert.equal(computeReconnectDelay(-1, reconnectCfg), 200)
  assert.equal(computeReconnectDelay(-100, reconnectCfg), 200)

  // Floating point attempt floored
  assert.equal(computeReconnectDelay(1.9, reconnectCfg), 400)

  // NaN floored to 0
  assert.equal(computeReconnectDelay(Number.NaN, reconnectCfg), 200)

  // Huge value capped at max_delay
  assert.equal(computeReconnectDelay(1000, reconnectCfg), 1500)
})

test('TerminalSession refit handles zero-dimension containers gracefully and exposes focus', () => {
  setConfig(nonDefaultConfig)

  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }
  class MockWebSocket {
    binaryType = 'blob'
    readyState = 0
    onopen: (() => void) | null = null
    onmessage: (() => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    close() {}
    send() {}
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket

  try {
    const zeroContainer = {
      clientWidth: 0,
      clientHeight: 0,
    } as HTMLElement

    const hooks: TerminalHooks = {
      onState: () => {},
      onNotice: () => {},
    }

    const session = new TerminalSession(zeroContainer, 's-zero', hooks)
    const fitAny = (session as any).fit

    const fitCallsBefore = fitAny.fitCount
    // Calling refit on zero-sized container must be a no-op and not call fit()
    session.refit()
    assert.equal(fitAny.fitCount, fitCallsBefore, 'refit on 0x0 container must not trigger fit()')

    // focus() must execute without throwing
    assert.doesNotThrow(() => session.focus())

    session.dispose()
  } finally {
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    resetConfigForTest()
  }
})

test('TerminalSession registers online/offline listeners and cleans them up on dispose', () => {
  setConfig(nonDefaultConfig)

  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket
  const originalWindow = (globalThis as any).window

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }
  class MockWebSocket {
    binaryType = 'blob'
    readyState = 0
    onopen: (() => void) | null = null
    onmessage: (() => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    close() {}
    send() {}
  }

  const listeners: Record<string, Function[]> = {}
  const mockWindow = {
    addEventListener: (event: string, fn: Function) => {
      listeners[event] = listeners[event] ?? []
      listeners[event].push(fn)
    },
    removeEventListener: (event: string, fn: Function) => {
      if (listeners[event]) {
        listeners[event] = listeners[event].filter((cb) => cb !== fn)
      }
    },
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  ;(globalThis as any).window = mockWindow

  try {
    const mockContainer = { clientWidth: 400, clientHeight: 300 } as HTMLElement
    const states: string[] = []
    const hooks: TerminalHooks = {
      onState: (s) => states.push(s),
      onNotice: () => {},
    }

    const session = new TerminalSession(mockContainer, 's-net', hooks)
    assert.ok(listeners['online']?.length === 1, 'online listener must be registered')
    assert.ok(listeners['offline']?.length === 1, 'offline listener must be registered')

    // Trigger offline event
    listeners['offline'][0]()
    assert.ok(states.includes('reconnecting'))

    // Trigger online event -> triggers immediate reconnect
    listeners['online'][0]()

    session.dispose()
    assert.equal(listeners['online']?.length, 0, 'online listener must be removed on dispose')
    assert.equal(listeners['offline']?.length, 0, 'offline listener must be removed on dispose')
  } finally {
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    ;(globalThis as any).window = originalWindow
    resetConfigForTest()
  }
})
