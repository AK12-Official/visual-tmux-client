import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import SessionList from './components/SessionList.vue'
import HostManagerModal from './components/HostManagerModal.vue'
import { clearToken, setToken } from './api'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'
import { resetLocaleForTest, setLocale } from './i18n/index'

const PROXY = '127.0.0.1:7890'

const messages = {
  en: {
    viaProxy: `Via proxy ${PROXY}`,
    direct: 'Direct connection',
    unreachable: `The proxy ${PROXY} cannot be reached. Check that the proxy app is running and that its address is correct in Settings.`,
    authRequired: `The proxy ${PROXY} asks for a username or password, which this app does not support.`,
    refused: `The proxy ${PROXY} refused to connect to the host. Check the rules in the proxy app.`,
    targetUnreachable: `The host did not answer through the proxy ${PROXY}. Check the host address and port.`,
  },
  'zh-CN': {
    viaProxy: `经代理 ${PROXY}`,
    direct: '直连',
    unreachable: `无法连接到代理 ${PROXY}。请确认代理软件正在运行，并在设置中核对代理地址。`,
    authRequired: `代理 ${PROXY} 要求用户名或密码，本应用不支持。`,
    refused: `代理 ${PROXY} 拒绝连接到该主机。请检查代理软件中的规则。`,
    targetUnreachable: `通过代理 ${PROXY} 无法连到该主机。请检查主机的地址和端口。`,
  },
} as const

const testConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
  web: {
    session_poll_interval: 100000,
    activity_decay: 1000,
    activity_throttle: 250,
    resize_debounce: 50,
    reconnect: { initial_delay: 200, max_delay: 1500 },
    terminal: { scrollback: 1000, font_size: 15, min_font_size: 10, max_font_size: 20 },
    notifications: { max_toasts: 5, error_lifetime: 50, warning_lifetime: 50, info_lifetime: 50 },
  },
}

beforeEach(() => {
  setConfig(testConfig)
  resetToastsForTest()
  resetLocaleForTest('en')
})

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function idle(ms = 20): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}

afterEach(() => {
  clearToken()
  resetToastsForTest()
  resetLocaleForTest('en')
  resetConfigForTest()
  localStorage.clear()
})

const remoteHost = {
  id: 'server-1',
  name: 'Production Server',
  address: 'prod.example.com',
  port: 22,
  username: 'deploy',
  credentialType: 'password',
  isLocal: false,
  revision: 3,
  status: 'connected',
  createdAt: 2000,
  updatedAt: 3000,
}

async function openManagerWithCapabilities(caps: Record<string, unknown>): Promise<ReturnType<typeof mount>> {
  setToken('test-token')
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url === '/api/hosts') return json({ hosts: [{ id: 'local', name: 'Local Host', isLocal: true, revision: 1, credentialType: 'none', status: 'connected', createdAt: 1000, updatedAt: 1000 }, remoteHost] })
    if (url.endsWith('/capabilities')) return json(caps)
    return json({}, 200)
  }
  const wrapper = mount(HostManagerModal, { props: { open: true }, attachTo: document.body })
  await nextTick()
  await idle()
  const testBtn = wrapper.findAll('.host-card')[1].find('.host-card__controls button')
  assert.ok(testBtn.exists())
  await testBtn.trigger('click')
  await idle()
  return wrapper
}

test('host card shows the connection path and a proxy failure in the current language', async () => {
  resetLocaleForTest('en')
  const wrapper = await openManagerWithCapabilities({
    ssh: false,
    tmux: false,
    sftp: false,
    posixRename: false,
    error: 'proxy_unreachable: proxy 127.0.0.1:7890',
    connectionPath: { mode: 'proxy', address: PROXY },
  })
  try {
    const path = wrapper.find('[data-testid="host-connection-path"]')
    const error = wrapper.find('[data-testid="host-caps-error"]')
    assert.equal(path.text(), messages.en.viaProxy)
    assert.equal(error.text(), messages.en.unreachable)
    assert.ok(!error.text().includes('proxy_unreachable'), 'the wire code is not shown')

    setLocale('zh-CN')
    await nextTick()
    assert.equal(path.text(), messages['zh-CN'].viaProxy)
    assert.equal(error.text(), messages['zh-CN'].unreachable)
  } finally {
    wrapper.unmount()
  }
})

test('host card says a remote host without a proxy connects directly', async () => {
  resetLocaleForTest('zh-CN')
  const wrapper = await openManagerWithCapabilities({
    ssh: true,
    tmux: true,
    sftp: true,
    posixRename: true,
    connectionPath: { mode: 'direct' },
  })
  try {
    assert.equal(wrapper.find('[data-testid="host-connection-path"]').text(), messages['zh-CN'].direct)
    assert.equal(wrapper.find('[data-testid="host-caps-error"]').exists(), false)
  } finally {
    wrapper.unmount()
  }
})

test('SessionList explains each proxy failure in both languages', async () => {
  const cases: Array<[string, keyof typeof messages.en]> = [
    ['proxy_unreachable', 'unreachable'],
    ['proxy_auth_required', 'authRequired'],
    ['proxy_refused', 'refused'],
    ['proxy_target_unreachable', 'targetUnreachable'],
  ]
  resetLocaleForTest('en')
  const wrapper = mount(SessionList, {
    props: { sessions: [], error: '', selected: null, active: {}, hostId: 'server-1', hostName: 'Production Server' },
  })
  try {
    for (const [code, key] of cases) {
      await wrapper.setProps({ error: `${code}: proxy ${PROXY}` })
      await nextTick()
      const errorEl = wrapper.find('.session-list__error span')
      assert.equal(errorEl.text(), messages.en[key])
      assert.ok(!errorEl.text().includes(code), 'the wire code is not shown')

      setLocale('zh-CN')
      await nextTick()
      assert.equal(errorEl.text(), messages['zh-CN'][key])

      setLocale('en')
      await nextTick()
    }
  } finally {
    wrapper.unmount()
  }
})

test('the Settings button comes before Hosts and opens the settings dialog', async () => {
  setToken('test-token')
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({ hosts: [{ id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true }] })
    }
    if (url.endsWith('/sessions')) return json({ sessions: [] })
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'local', revision: 1, proxyAddress: PROXY })
    }
    return json({}, 404)
  }
  const wrapper = mount(App)
  await nextTick()
  await idle()
  try {
    const buttons = wrapper.findAll('.app__bar-actions button')
    const settings = buttons[0]
    assert.ok(settings.classes('app__settings-btn'), 'Settings is the first action')
    assert.equal(buttons[1].text(), 'Hosts')
    assert.equal(settings.text(), 'Settings')
    assert.equal(settings.attributes('aria-label'), 'Settings')
    assert.equal(wrapper.find('.settings-modal').exists(), false)

    setLocale('zh-CN')
    await nextTick()
    assert.equal(settings.text(), '设置')
    setLocale('en')
    await nextTick()

    await settings.trigger('click')
    await nextTick()
    await idle()
    const dialog = wrapper.find('.settings-modal')
    assert.ok(dialog.exists())
    assert.equal(dialog.find('.settings-modal__title').text(), 'Settings')
    assert.equal((dialog.find('#settings-proxy-address').element as HTMLInputElement).value, PROXY)
  } finally {
    wrapper.unmount()
  }
})

test('a failing proxy stops session polling and is explained in the session list', async () => {
  const config: ClientConfig = {
    version: 1,
    files: { enabled: true, max_file_size: 104857600 },
    web: {
      session_poll_interval: 40,
      activity_decay: 1000,
      activity_throttle: 250,
      resize_debounce: 50,
      reconnect: { initial_delay: 200, max_delay: 1500 },
      terminal: { scrollback: 1000, font_size: 15, min_font_size: 10, max_font_size: 20 },
      notifications: { max_toasts: 5, error_lifetime: 50, warning_lifetime: 50, info_lifetime: 50 },
    },
  }
  setConfig(config)
  setToken('test-token')
  resetLocaleForTest('zh-CN')

  let sessionRequests = 0
  let failing = true
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({ hosts: [{ id: 'server-1', name: 'Production Server', revision: 1, connectionRevision: 1, trusted: true }] })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'server-1', revision: 1, proxyAddress: PROXY })
    }
    if (url.endsWith('/sessions')) {
      sessionRequests++
      if (failing) {
        return json({ error: `proxy_unreachable: proxy ${PROXY}` }, 503)
      }
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await idle(300)
  try {
    const errorEl = wrapper.find('.session-list__error')
    assert.ok(errorEl.exists())
    assert.ok(errorEl.text().includes(messages['zh-CN'].unreachable), errorEl.text())
    assert.ok(!errorEl.text().includes('proxy_unreachable'), 'the wire code is not shown')

    // Polling stopped: the count stays flat while the proxy keeps failing.
    const stopped = sessionRequests
    assert.ok(stopped >= 1 && stopped <= 3, `few attempts expected, saw ${stopped}`)
    await idle(300)
    assert.equal(sessionRequests, stopped)

    // Retry reaches the hub again and, once the proxy is back, polling resumes.
    failing = false
    await wrapper.find('.session-list__error button').trigger('click')
    await nextTick()
    await idle(300)
    assert.ok(sessionRequests > stopped + 1, 'polling resumed after the proxy recovered')
    assert.equal(wrapper.find('.session-list__error').exists(), false)
  } finally {
    wrapper.unmount()
  }
})
