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
  disposeTerminalSession,
  insertIntoTerminal,
  renameTerminalSession,
  shouldThrottleActivity,
  TerminalSession,
  type TerminalHooks,
} from './terminal'

const nonDefaultConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
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

test('attaching same-named sessions on two hosts targets the selected host', async () => {
  setConfig(nonDefaultConfig)

  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket
  const originalLocation = globalThis.location
  const originalFetch = globalThis.fetch
  const originalLocalStorage = globalThis.localStorage

  const store = new Map<string, string>()
  store.set('vtc:token', 'test-token')
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => store.set(key, val),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
  } as any

  const openedSockets: { url: string }[] = []

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }
  class MockWebSocket {
    static OPEN = 1
    static CONNECTING = 0
    static CLOSING = 2
    static CLOSED = 3
    binaryType = 'blob'
    readyState = 1
    onopen: (() => void) | null = null
    onmessage: ((ev: any) => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    url: string
    sent: any[] = []
    constructor(url: string) {
      this.url = url
      openedSockets.push({ url })
      setTimeout(() => {
        if (this.onopen) this.onopen()
      }, 0)
    }
    close() {}
    send(data: any) {
      this.sent.push(data)
    }
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  globalThis.location = { protocol: 'http:', host: 'localhost:8080' } as any

  const ticketsIssued: { session: string; hostId: string }[] = []
  globalThis.fetch = async (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const body = init?.body ? JSON.parse(String(init.body)) : {}
    ticketsIssued.push({ session: body.session, hostId: body.hostId })
    return new Response(JSON.stringify({ ticket: `ticket-for-${body.hostId}-${body.session}` }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  try {
    const mockContainerA = { clientWidth: 640, clientHeight: 480 } as HTMLElement
    const mockContainerB = { clientWidth: 640, clientHeight: 480 } as HTMLElement
    const hooksA: TerminalHooks = { onState: () => {}, onNotice: () => {} }
    const hooksB: TerminalHooks = { onState: () => {}, onNotice: () => {} }

    // Create session "dev" on host "host-a" and session "dev" on host "host-b"
    const sessionA = new TerminalSession(mockContainerA, 'dev', hooksA, 16, 'host-a')
    const sessionB = new TerminalSession(mockContainerB, 'dev', hooksB, 16, 'host-b')

    assert.equal(sessionA.host, 'host-a')
    assert.equal(sessionB.host, 'host-b')
    assert.equal(sessionA.name, 'dev')
    assert.equal(sessionB.name, 'dev')

    // Wait for async ticket issue and WebSocket connection
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(ticketsIssued.length, 2)
    assert.deepEqual(ticketsIssued[0], { session: 'dev', hostId: 'host-a' })
    assert.deepEqual(ticketsIssued[1], { session: 'dev', hostId: 'host-b' })

    assert.equal(openedSockets.length, 2)
    assert.ok(openedSockets[0].url.includes('/ws/host-a/dev?ticket=ticket-for-host-a-dev'))
    assert.ok(openedSockets[1].url.includes('/ws/host-b/dev?ticket=ticket-for-host-b-dev'))

    // Test text insertion targeted by hostId
    assert.equal(insertIntoTerminal('dev', 'echo hello-a\n', 'host-a'), true)
    assert.equal(insertIntoTerminal('dev', 'echo hello-b\n', 'host-b'), true)
    assert.equal(insertIntoTerminal('dev', 'echo hello-c\n', 'non-existent-host'), false)

    // Test rename session targeted by hostId
    renameTerminalSession('dev', 'renamed-dev', 'host-a')
    assert.equal(sessionA.name, 'renamed-dev')
    assert.equal(sessionB.name, 'dev') // host-b's dev must NOT be renamed

    // Test disposal targeted by hostId
    disposeTerminalSession('renamed-dev', 'host-a')
    assert.equal((sessionA as any).disposed, true)
    assert.equal((sessionB as any).disposed, false)

    sessionB.dispose()
  } finally {
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    globalThis.location = originalLocation
    globalThis.fetch = originalFetch
    globalThis.localStorage = originalLocalStorage
    resetConfigForTest()
  }
})

test("output on host A's dev never triggers activity on host B's same-named session", async () => {
  setConfig(nonDefaultConfig)

  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket
  const originalLocation = globalThis.location
  const originalFetch = globalThis.fetch
  const originalLocalStorage = globalThis.localStorage

  const store = new Map<string, string>()
  store.set('vtc:token', 'test-token')
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => store.set(key, val),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
  } as any

  let socketA: any = null
  let socketB: any = null

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }
  class MockWebSocket {
    static OPEN = 1
    static CONNECTING = 0
    static CLOSING = 2
    static CLOSED = 3
    binaryType = 'blob'
    readyState = 1
    onopen: (() => void) | null = null
    onmessage: ((ev: any) => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    url: string
    constructor(url: string) {
      this.url = url
      if (url.includes('/ws/host-a/')) socketA = this
      if (url.includes('/ws/host-b/')) socketB = this
      setTimeout(() => {
        if (this.onopen) this.onopen()
      }, 0)
    }
    close() {}
    send() {}
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  globalThis.location = { protocol: 'http:', host: 'localhost:8080' } as any
  globalThis.fetch = async (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const body = init?.body ? JSON.parse(String(init.body)) : {}
    return new Response(JSON.stringify({ ticket: `ticket-${body.hostId}-${body.session}` }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  try {
    const mockContainerA = { clientWidth: 640, clientHeight: 480 } as HTMLElement
    const mockContainerB = { clientWidth: 640, clientHeight: 480 } as HTMLElement
    let activityCountA = 0
    let activityCountB = 0

    const hooksA: TerminalHooks = {
      onState: () => {},
      onNotice: () => {},
      onActivity: () => {
        activityCountA++
      },
    }
    const hooksB: TerminalHooks = {
      onState: () => {},
      onNotice: () => {},
      onActivity: () => {
        activityCountB++
      },
    }

    const sessionA = new TerminalSession(mockContainerA, 'dev', hooksA, 16, 'host-a')
    const sessionB = new TerminalSession(mockContainerB, 'dev', hooksB, 16, 'host-b')

    await new Promise((r) => setTimeout(r, 20))
    assert.ok(socketA !== null, 'socketA should be connected')
    assert.ok(socketB !== null, 'socketB should be connected')

    // Simulate binary output arriving from host-a's tmux session
    const payloadA = new Uint8Array([104, 101, 108, 108, 111]).buffer
    socketA.onmessage({ data: payloadA })

    // Verify activity fired ONLY for host-a, not host-b
    assert.equal(activityCountA, 1)
    assert.equal(activityCountB, 0)

    sessionA.dispose()
    sessionB.dispose()
  } finally {
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    globalThis.location = originalLocation
    globalThis.fetch = originalFetch
    globalThis.localStorage = originalLocalStorage
    resetConfigForTest()
  }
})

test('TerminalSession ends reconnection when host revision conflict occurs', async () => {
  setConfig(nonDefaultConfig)

  const originalResizeObserver = globalThis.ResizeObserver
  const originalWebSocket = globalThis.WebSocket
  const originalLocation = globalThis.location
  const originalFetch = globalThis.fetch
  const originalLocalStorage = globalThis.localStorage

  class MockResizeObserver {
    observe() {}
    disconnect() {}
  }

  class MockWebSocket {
    static OPEN = 1
    static CONNECTING = 0
    static CLOSING = 2
    static CLOSED = 3
    binaryType = 'blob'
    readyState = 1
    onopen: (() => void) | null = null
    onmessage: ((ev: any) => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    url: string
    constructor(url: string) {
      this.url = url
    }
    close() {}
    send() {}
  }

  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver
  globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
  globalThis.location = { protocol: 'http:', host: 'localhost:8080' } as any

  const store = new Map<string, string>()
  store.set('visual-tmux-client-token', 'test-token')
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => store.set(key, val),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
  } as any

  globalThis.fetch = async (): Promise<Response> => {
    return new Response(JSON.stringify({ error: 'host_revision_conflict' }), {
      status: 409,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  const states: string[] = []
  const notices: string[] = []
  const hooks: TerminalHooks = {
    onState: (state) => states.push(state),
    onNotice: (notice) => notices.push(notice),
  }

  const session = new TerminalSession({ clientWidth: 640, clientHeight: 480 } as HTMLElement, 'main', hooks, 16, 'remote-1')

  try {
    await new Promise((r) => setTimeout(r, 40))

    assert.ok(states.includes('ended'), 'session should transition to ended')
    assert.ok(notices.some((n) => n.includes('Host configuration changed')), 'notice should report host configuration change')
  } finally {
    session.dispose()
    globalThis.ResizeObserver = originalResizeObserver
    globalThis.WebSocket = originalWebSocket
    globalThis.location = originalLocation
    globalThis.fetch = originalFetch
    globalThis.localStorage = originalLocalStorage
    resetConfigForTest()
  }
})

