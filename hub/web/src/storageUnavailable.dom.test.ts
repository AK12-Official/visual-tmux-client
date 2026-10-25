import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import { clearToken, getToken, setToken, authFetch } from './api'
import { safeStorageGet, safeStorageSet, safeStorageRemove } from './storage'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'
import { setLocale, resetLocaleForTest } from './i18n/index'

const testConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
  web: {
    session_poll_interval: 100000,
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
  },
}

const origLocalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const origFetch = globalThis.fetch

function restoreStorage() {
  if (origLocalStorage) {
    Object.defineProperty(globalThis, 'localStorage', origLocalStorage)
  }
}

beforeEach(() => {
  resetConfigForTest()
  setConfig(testConfig)
  resetToastsForTest()
  clearToken()
  resetLocaleForTest('en')
  globalThis.fetch = async (): Promise<Response> => {
    return new Response(JSON.stringify({ hosts: [], defaultHostId: 'local' }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }
})

afterEach(() => {
  restoreStorage()
  clearToken()
  resetLocaleForTest('en')
  globalThis.fetch = origFetch
})

test('I18N-R4-01: storage getter throws SecurityError - safeStorage helpers degrade and memory token functions', async () => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    get() {
      throw new DOMException('Storage access blocked', 'SecurityError')
    },
  })

  // 1. Storage helpers return fallback without throwing
  assert.equal(safeStorageGet('any-key'), null)
  assert.equal(safeStorageSet('any-key', 'val'), false)
  assert.equal(safeStorageRemove('any-key'), false)

  // 2. Token management falls back to in-memory state
  assert.equal(getToken(), null)
  setToken('memory-fallback-token')
  assert.equal(getToken(), 'memory-fallback-token')

  // 3. authFetch uses in-memory token for Authorization header
  let sentAuthHeader = ''
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    sentAuthHeader = new Headers(init?.headers).get('Authorization') ?? ''
    return new Response('{}', { status: 200 })
  }

  await authFetch('/api/test')
  assert.equal(sentAuthHeader, 'Bearer memory-fallback-token')

  // 4. clearToken resets in-memory token
  clearToken()
  assert.equal(getToken(), null)
})

test('I18N-R4-01: storage getter throws SecurityError - App mounts, renders login, switches language, and authenticates', async () => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    get() {
      throw new DOMException('Storage access blocked', 'SecurityError')
    },
  })

  resetLocaleForTest('en')
  clearToken()

  // App mount must not throw unhandled SecurityError on setup
  const wrapper = mount(App)
  await nextTick()

  // Login view is visible
  const cardTitle = wrapper.find('.auth-card__title')
  assert.ok(cardTitle.exists(), 'Login card title should be present')
  assert.equal(cardTitle.text(), 'Visual Tmux Client')

  const submitBtn = wrapper.find('.auth-card__btn')
  assert.equal(submitBtn.text(), 'Connect')

  // Dynamic language switch on login page works without storage
  setLocale('zh-CN')
  await nextTick()

  assert.equal(wrapper.find('.auth-card__btn').text(), '连接')
  assert.equal(wrapper.find('.auth-card__hint').text(), '输入访问令牌以连接。')

  // Authenticate with in-memory token
  const input = wrapper.find('.auth-card__input')
  await input.setValue('valid-session-token')
  await wrapper.find('.auth-card__btn').trigger('click')
  await nextTick()

  // Token is saved in memory
  assert.equal(getToken(), 'valid-session-token')

  wrapper.unmount()
})

test('I18N-R4-01: localStorage.getItem throws SecurityError - fallback preserves memory token', async () => {
  const store = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem() {
        throw new DOMException('Blocked read', 'SecurityError')
      },
      setItem(key: string, val: string) {
        store.set(key, val)
      },
      removeItem(key: string) {
        store.delete(key)
      },
    },
  })

  clearToken()
  assert.equal(getToken(), null)

  setToken('my-token-after-read-failure')
  assert.equal(getToken(), 'my-token-after-read-failure')

  // App mounts cleanly despite getItem throwing
  const wrapper = mount(App)
  await nextTick()

  assert.ok(wrapper.find('.app').exists())
  wrapper.unmount()
})

test('I18N-R4-01: localStorage.setItem throws QuotaExceededError - fallback preserves memory token', async () => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem() {
        return null
      },
      setItem() {
        throw new DOMException('Storage quota exceeded', 'QuotaExceededError')
      },
      removeItem() {},
    },
  })

  clearToken()
  assert.equal(getToken(), null)

  setToken('quota-token-saved-to-memory')
  assert.equal(getToken(), 'quota-token-saved-to-memory')

  // App mounts cleanly despite setItem throwing
  const wrapper = mount(App)
  await nextTick()

  assert.ok(wrapper.find('.app').exists())
  wrapper.unmount()
})

test('I18N-R5-01: Preset old token, setItem throws QuotaExceededError - getToken and authFetch use new token', async () => {
  const store = new Map<string, string>()
  store.set('visual-tmux-client-token', 'old-token')

  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem(key: string) {
        return store.get(key) ?? null
      },
      setItem() {
        throw new DOMException('Storage quota exceeded', 'QuotaExceededError')
      },
      removeItem(key: string) {
        store.delete(key)
      },
    },
  })

  // 1. Before mutation, uninitialized memory reads existing old-token
  clearToken() // clears any lingering session state; since removeItem deletes it from store, let's re-seed store
  store.set('visual-tmux-client-token', 'old-token')
  assert.equal(getToken(), 'old-token')

  let sentAuthHeader = ''
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    sentAuthHeader = new Headers(init?.headers).get('Authorization') ?? ''
    return new Response('{}', { status: 200 })
  }

  // 2. Submit new token when storage write is rejected
  setToken('new-token')

  // 3. getToken must return new-token, not the shadowed old-token
  assert.equal(getToken(), 'new-token')

  // 4. authFetch must send new-token in Authorization header
  await authFetch('/api/hosts')
  assert.equal(sentAuthHeader, 'Bearer new-token', 'Request must send new token, not old token')
})

test('I18N-R5-01: Preset old token, removeItem throws - clearToken sets token to null and authFetch omits Authorization header', async () => {
  const store = new Map<string, string>()
  store.set('visual-tmux-client-token', 'old-token')

  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem(key: string) {
        return store.get(key) ?? null
      },
      setItem(key: string, val: string) {
        store.set(key, val)
      },
      removeItem() {
        throw new DOMException('Delete forbidden', 'SecurityError')
      },
    },
  })

  // 1. Initial state reads old-token
  assert.equal(getToken(), 'old-token')

  let sentAuthHeader: string | null = null
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    sentAuthHeader = new Headers(init?.headers).get('Authorization')
    return new Response('{}', { status: 200 })
  }

  // 2. Clear token (logout / 401)
  clearToken()

  // 3. getToken must be null even though removeItem failed and storage still has old-token
  assert.equal(getToken(), null, 'getToken must return null after clearToken')

  // 4. authFetch must not carry old Authorization header
  await authFetch('/api/hosts')
  assert.equal(sentAuthHeader, null, 'authFetch must not carry any Authorization header after logout')
})

