import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import FileManagerOverlay from './components/files/FileManagerOverlay.vue'
import { clearToken, setToken, getToken } from './api'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'

const testConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
  web: {
    session_poll_interval: 100000, // manual triggering via refresh in tests
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
      max_toasts: 5,
      error_lifetime: 50,
      warning_lifetime: 50,
      info_lifetime: 50,
    },
  },
}

beforeEach(() => {
  setConfig(testConfig)
  resetToastsForTest()
  setToken('valid-central-token')
})

afterEach(() => {
  clearToken()
  resetToastsForTest()
  resetConfigForTest()
  localStorage.removeItem('vtc:recent-host')
})

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

test('late list response from host A does not overwrite active host B session list', async () => {
  let resolveHostA: ((r: Response) => void) | null = null

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({ sessions: [] })
    }
    if (url.includes('/api/hosts/host-a/sessions')) {
      return new Promise<Response>((resolve) => {
        resolveHostA = resolve
      })
    }
    if (url.includes('/api/hosts/host-b/sessions')) {
      return json({
        sessions: [
          { name: 'b-session', windows: 1, attached: 0, created: 1000 },
        ],
      })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Switch to Host A (request will hang in resolveHostA)
  const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
  assert.ok(hostSelect.exists(), 'host selector should exist')
  await hostSelect.setValue('host-a')
  await nextTick()

  // Rapidly switch to Host B
  await hostSelect.setValue('host-b')
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Verify Host B's session is shown
  assert.ok(wrapper.text().includes('b-session'), 'Host B session should be rendered')

  // Now resolve the late response from Host A with a-session
  assert.ok(resolveHostA !== null, 'Host A request should be in-flight')
  resolveHostA!(
    json({
      sessions: [
        { name: 'a-session', windows: 1, attached: 0, created: 1000 },
      ],
    }),
  )
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Verify Host B's workspace is preserved and NOT overwritten by Host A's late response
  assert.ok(wrapper.text().includes('b-session'), 'Host B session must still be rendered')
  assert.ok(!wrapper.text().includes('a-session'), 'Late response from Host A must NOT appear on Host B')
  wrapper.unmount()
})

test('late error response from host A does not pollute host B error state', async () => {
  let rejectHostA: ((r: Response) => void) | null = null

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({ sessions: [] })
    }
    if (url.includes('/api/hosts/host-a/sessions')) {
      return new Promise<Response>((resolve) => {
        rejectHostA = resolve
      })
    }
    if (url.includes('/api/hosts/host-b/sessions')) {
      return json({
        sessions: [
          { name: 'b-clean', windows: 1, attached: 0, created: 1000 },
        ],
      })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
  await hostSelect.setValue('host-a')
  await nextTick()

  // Switch to Host B
  await hostSelect.setValue('host-b')
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Host A returns 503 host_unavailable late
  assert.ok(rejectHostA !== null, 'Host A request should be in-flight')
  rejectHostA!(json({ error: 'host_unavailable' }, 503))
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Host B session list should still be cleanly displayed, no error
  assert.ok(wrapper.text().includes('b-clean'), 'Host B sessions should remain displayed')
  assert.ok(!wrapper.text().includes('host_unavailable'), 'Host A error must NOT pollute Host B')
  wrapper.unmount()
})

test('remote host authentication failure preserves central token and offers repair', async () => {
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'remote-bad', name: 'Remote Bad Auth', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({ sessions: [] })
    }
    if (url.includes('/api/hosts/remote-bad/sessions')) {
      // Remote host authentication failure returns 503 or errorText with authentication failed
      return json({ error: 'host_unavailable: ssh authentication failed' }, 503)
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Switch to remote-bad
  const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
  await hostSelect.setValue('remote-bad')
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Central token MUST NOT be cleared
  assert.equal(getToken(), 'valid-central-token', 'central bearer token must not be cleared on remote auth failure')

  // App must NOT return to login prompt
  assert.ok(!wrapper.find('.app__auth').exists(), 'app must remain authenticated in central view')

  // SessionList should display error and the Edit Host repair button
  const errorEl = wrapper.find('.session-list__error')
  assert.ok(errorEl.exists(), 'error element should be rendered')
  assert.ok(errorEl.text().includes('ssh authentication failed'))

  const repairBtn = wrapper.find('.session-list__btn--repair')
  assert.ok(repairBtn.exists(), 'repair button (Edit host) must be offered for remote failure')

  // Clicking Edit host opens the HostManagerModal
  await repairBtn.trigger('click')
  await nextTick()
  assert.ok(wrapper.find('.host-modal').exists(), 'Host manager modal should open for repair')
  wrapper.unmount()
})

test('local 401 clears central token and triggers central logout', async () => {
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({ hosts: [{ id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true }] })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'local', revision: 1 })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({ error: 'unauthorized' }, 401)
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Local 401 must clear token and show login view
  assert.equal(getToken(), null, 'local 401 must clear token')
  assert.ok(wrapper.find('.app__auth').exists(), 'local 401 must show login view')
  wrapper.unmount()
})

test('offline host preserves cached sessions and marks them stale', async () => {
  let shouldFail = false

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'remote-box', name: 'Remote Box', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({ sessions: [] })
    }
    if (url.includes('/api/hosts/remote-box/sessions')) {
      if (shouldFail) {
        return json({ error: 'host_unavailable' }, 503)
      }
      return json({
        sessions: [
          { name: 'cached-work', windows: 2, attached: 1, created: 1000 },
        ],
      })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Switch to remote-box
  const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
  await hostSelect.setValue('remote-box')
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Initially session is present
  assert.ok(wrapper.text().includes('cached-work'))

  // Next poll fails with host_unavailable
  shouldFail = true
  // Trigger retry button in UI
  const retryBtn = wrapper.find('.session-list__tool') // click select mode or trigger retry
  // Or call retry from SessionList
  const sessionList = wrapper.findComponent({ name: 'SessionList' })
  sessionList.vm.$emit('retry')
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))

  // Cached session MUST still be rendered
  assert.ok(wrapper.text().includes('cached-work'), 'cached sessions must not be wiped out on offline failure')

  // Stale badge and warning must be visible
  const staleBanner = wrapper.find('.session-list__stale')
  assert.ok(staleBanner.exists(), 'stale banner must be displayed')
  assert.ok(staleBanner.text().includes('Stale'), 'stale badge should be visible')
  assert.ok(staleBanner.text().includes('host_unavailable'), 'error should be shown in stale banner')
  wrapper.unmount()
})

test('cross-host terminal path insertion is refused with warning toast', async () => {
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'local', revision: 1 })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/home/user', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({ path: '/home/user', entries: [], truncated: false })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [{ name: 'work', windows: 1, attached: 1, created: 1000 }] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Select session to open terminal
    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    assert.ok(sessionList.exists(), 'SessionList must exist')
    sessionList.vm.$emit('select', 'work')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fmBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    assert.ok(fmBtn.exists(), 'file manager button must exist')
    await fmBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 50))
    await nextTick()

    const fm = wrapper.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be open')

    // Emit insert-path from a different host (host-a) while terminal is on local
    fm.vm.$emit('insert-path', "'/srv/work/file.txt'", 'host-a')
    await nextTick()

    // Verify warning toast is shown refusing insertion
    const toasts = wrapper.findAll('.toast')
    assert.ok(
      toasts.some((t) => t.text().includes('Cannot insert path across hosts')),
      `expected cross-host insertion refusal toast, found: ${toasts.map((t) => t.text()).join(', ')}`,
    )
  } finally {
    wrapper.unmount()
  }
})

test('recent host precedes default host on bootstrap', async () => {
  localStorage.setItem('vtc:recent-host', 'host-a')
  const requestedHostSessions: string[] = []

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-b', revision: 1 })
    }
    const match = url.match(/\/api\/hosts\/([^/]+)\/sessions/)
    if (match) {
      requestedHostSessions.push(match[1])
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.deepEqual(requestedHostSessions, ['host-a'], 'should fetch sessions for recent host-a')
    const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
    assert.equal(hostSelect.element.value, 'host-a')
  } finally {
    localStorage.removeItem('vtc:recent-host')
    wrapper.unmount()
  }
})

test('recent host absent falls back to registered default host', async () => {
  localStorage.setItem('vtc:recent-host', 'missing-host')
  const requestedHostSessions: string[] = []

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-b', revision: 1 })
    }
    const match = url.match(/\/api\/hosts\/([^/]+)\/sessions/)
    if (match) {
      requestedHostSessions.push(match[1])
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.deepEqual(requestedHostSessions, ['host-b'], 'should fall back to registered default host-b')
    const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
    assert.equal(hostSelect.element.value, 'host-b')
  } finally {
    localStorage.removeItem('vtc:recent-host')
    wrapper.unmount()
  }
})

test('no valid preference renders selection prompt without issuing session requests', async () => {
  localStorage.removeItem('vtc:recent-host')
  const requestedHostSessions: string[] = []

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: null, revision: 1 })
    }
    const match = url.match(/\/api\/hosts\/([^/]+)\/sessions/)
    if (match) {
      requestedHostSessions.push(match[1])
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(requestedHostSessions.length, 0, 'zero session requests should be sent before explicit selection')

    const prompt = wrapper.find('.app__discovery-state--prompt')
    assert.ok(prompt.exists(), 'host selection prompt should be rendered')
    const promptButtons = wrapper.findAll('.app__host-card-btn')
    assert.equal(promptButtons.length, 2, 'prompt buttons for available hosts should be present')

    await promptButtons[0].trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.deepEqual(requestedHostSessions, ['host-a'], 'selecting host-a fetches its sessions')
  } finally {
    wrapper.unmount()
  }
})

test('empty registry renders add-host prompt without issuing session requests', async () => {
  localStorage.removeItem('vtc:recent-host')
  const requestedHostSessions: string[] = []

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({ hosts: [] })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: null, revision: 1 })
    }
    const match = url.match(/\/api\/hosts\/([^/]+)\/sessions/)
    if (match) {
      requestedHostSessions.push(match[1])
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(requestedHostSessions.length, 0, 'zero session requests should be sent for empty registry')
    const emptyState = wrapper.find('.app__discovery-state--empty')
    assert.ok(emptyState.exists(), 'empty registry state should be rendered')
    const addBtn = wrapper.find('.app__discovery-btn')
    assert.ok(addBtn.exists(), 'add host button should be offered')

    await addBtn.trigger('click')
    await nextTick()
    assert.ok(wrapper.find('.host-modal').exists(), 'HostManagerModal should be opened')
  } finally {
    wrapper.unmount()
  }
})

test('host discovery failure renders error banner with retry button without fabricating local host', async () => {
  let discoveryAttempts = 0

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      discoveryAttempts++
      if (discoveryAttempts === 1) {
        return json({ error: 'database_unavailable' }, 500)
      }
      return json({
        hosts: [{ id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true }],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const errBanner = wrapper.find('.app__discovery-state--error')
    assert.ok(errBanner.exists(), 'discovery error banner must be rendered')
    assert.ok(errBanner.text().includes('database_unavailable'))

    assert.ok(!wrapper.find('.session-list').exists(), 'session list should not be mounted on discovery failure')

    const retryBtn = errBanner.find('button')
    assert.ok(retryBtn.exists(), 'retry button must exist')
    await retryBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(discoveryAttempts, 2, 'retry button should re-trigger discovery')
    assert.ok(!wrapper.find('.app__discovery-state--error').exists(), 'error banner should disappear on success')
  } finally {
    wrapper.unmount()
  }
})

test('active host removed stops target operations, preserves dirty drafts, and requires explicit selection', async () => {
  const listSessionsCalls: string[] = []

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/home/user', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({ path: '/home/user', entries: [], truncated: false })
    }
    const match = url.match(/\/api\/hosts\/([^/]+)\/sessions/)
    if (match) {
      listSessionsCalls.push(match[1])
      return json({ sessions: [{ name: 'work', windows: 1, attached: 1, created: 1000 }] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.ok(listSessionsCalls.includes('host-a'))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    sessionList.vm.$emit('select', 'work')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fmBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    await fmBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fm = wrapper.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be open')
    fm.vm.$emit('dirty-change', true, ['host-a'])
    await nextTick()

    const hostModal = wrapper.findComponent({ name: 'HostManagerModal' })
    hostModal.vm.$emit('host-deleted', 'host-a')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const removedBanner = wrapper.find('.app__removed-banner')
    assert.ok(removedBanner.exists(), 'removed banner must be rendered')

    assert.ok(wrapper.findComponent(FileManagerOverlay).exists(), 'file manager overlay must remain mounted for draft recovery')

    const callCountBefore = listSessionsCalls.length
    sessionList.vm.$emit('retry')
    await nextTick()
    assert.equal(listSessionsCalls.length, callCountBefore, 'no new session operations after host removal')

    const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
    await hostSelect.setValue('host-b')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.ok(listSessionsCalls.includes('host-b'), 'explicit selection switches to host-b')
    assert.ok(!wrapper.find('.app__removed-banner').exists(), 'removed banner cleared after switching host')
  } finally {
    wrapper.unmount()
  }
})

test('host rename preserving connection revision preserves terminal and session state', async () => {
  let hostRevision = 1
  let hostName = 'Original Name'

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: hostName, revision: hostRevision, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/api/hosts/host-a/sessions')) {
      return json({
        sessions: [{ name: 'main', windows: 1, attached: 1, created: 1000 }],
      })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    sessionList.vm.$emit('select', 'main')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const hostHeader = wrapper.find('.app__term-host')
    assert.ok(hostHeader.exists())
    assert.equal(hostHeader.text(), 'Original Name')

    hostRevision = 2
    hostName = 'Renamed Box'
    const hostModal = wrapper.findComponent({ name: 'HostManagerModal' })
    hostModal.vm.$emit('hosts-changed')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(hostHeader.text(), 'Renamed Box')
    assert.ok(wrapper.findComponent({ name: 'TerminalView' }).exists(), 'TerminalView must remain open across metadata rename')
  } finally {
    wrapper.unmount()
  }
})

test('central 401 on SSH host clears central token and triggers login', async () => {
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [{ id: 'ssh-box', name: 'SSH Box', revision: 1, connectionRevision: 1, trusted: true }],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'ssh-box', revision: 1 })
    }
    if (url.includes('/api/hosts/ssh-box/sessions')) {
      return json({ error: 'unauthorized' }, 401)
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    assert.equal(getToken(), null, 'central token must be cleared on 401 even when target is SSH host')
    assert.ok(wrapper.find('.app__auth').exists(), 'app must show login card on central 401')
  } finally {
    wrapper.unmount()
  }
})

test('F1: bulk kill does not target new host when switched mid-batch', async () => {
  const killCalls: { hostId: string; session: string }[] = []
  let resolveFirstKill: (() => void) | null = null

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/api/hosts/host-a/sessions') && init?.method === 'DELETE') {
      const parts = url.split('/')
      const sess = parts[parts.length - 1]
      killCalls.push({ hostId: 'host-a', session: sess })
      if (!resolveFirstKill) {
        await new Promise<void>((resolve) => {
          resolveFirstKill = resolve
        })
      }
      return json({ ok: true })
    }
    if (url.includes('/api/hosts/host-b/sessions') && init?.method === 'DELETE') {
      const parts = url.split('/')
      const sess = parts[parts.length - 1]
      killCalls.push({ hostId: 'host-b', session: sess })
      return json({ ok: true })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [{ name: 's1' }, { name: 's2' }] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    const bulkPromise = sessionList.vm.$emit('bulk-kill', ['s1', 's2'])

    // Wait until first kill is in flight
    while (!resolveFirstKill) {
      await new Promise((r) => setTimeout(r, 5))
    }

    // Switch host while bulk kill is pending
    const select = wrapper.find('#host-select')
    await select.setValue('host-b')
    await select.trigger('change')
    await nextTick()

    // Release first kill
    resolveFirstKill!()
    await bulkPromise
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Assert that no kills were ever directed at host-b
    const killsOnB = killCalls.filter((c) => c.hostId === 'host-b')
    assert.equal(killsOnB.length, 0, 'bulk kill must never target host-b after switching')
    assert.equal(killCalls.length, 2, 'both kills must target original host-a')
    assert.equal(killCalls[0].hostId, 'host-a')
    assert.equal(killCalls[1].hostId, 'host-a')
  } finally {
    wrapper.unmount()
  }
})

test('F4: deleting the last host preserves activeHostRemoved state and open drafts', async () => {
  let hostsList = [{ id: 'host-only', name: 'Host Only', revision: 1, connectionRevision: 1, trusted: true }]

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({ hosts: hostsList })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-only', revision: 1 })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/home/user', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({ path: '/home/user', entries: [], truncated: false })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [{ name: 's1', windows: 1, attached: 1, created: 1000 }] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    sessionList.vm.$emit('select', 's1')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fmBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    await fmBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fm = wrapper.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be open')
    fm.vm.$emit('dirty-change', true, ['host-only'])
    await nextTick()

    // Delete the only host
    hostsList = []
    const hostModal = wrapper.findComponent({ name: 'HostManagerModal' })
    hostModal.vm.$emit('host-deleted', 'host-only')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Assert that the app did not transition to the unmounted empty state
    const removedBanner = wrapper.find('.app__removed-banner')
    assert.ok(removedBanner.exists(), 'removed banner must be rendered even when 0 hosts remain')
    assert.ok(removedBanner.text().includes('Add a host to continue'), 'banner informs user to add host')

    // Assert FileManagerOverlay remains mounted
    assert.ok(wrapper.findComponent(FileManagerOverlay).exists(), 'file manager overlay must remain mounted for draft recovery')
  } finally {
    wrapper.unmount()
  }
})

test('F2: late file read from host A is labeled with host A and does not become active on host B', async () => {
  let resolveReadFile: ((res: Response) => void) | null = null

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/work', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({
        path: '/work',
        entries: [{ name: 'fileA.txt', is_dir: false, size: 10, mtime: 1000 }],
        truncated: false,
      })
    }
    if (url.includes('/files/read')) {
      return new Promise<Response>((resolve) => {
        resolveReadFile = resolve
      })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [{ name: 's1', windows: 1, attached: 1, created: 1000 }] })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    sessionList.vm.$emit('select', 's1')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fmBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    await fmBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fm = wrapper.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be open')

    // Open fileA.txt via FileTree
    const tree = wrapper.findComponent({ name: 'FileTree' })
    tree.vm.$emit('open', { name: 'fileA.txt', is_dir: false, size: 10, mtime: 1000 }, '/work/fileA.txt')
    await nextTick()

    // While read is in flight, switch to host-b
    const select = wrapper.find('#host-select')
    await select.setValue('host-b')
    await select.trigger('change')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Now resolve read from host-a
    assert.ok(resolveReadFile !== null, 'read request must be in flight')
    resolveReadFile!(
      new Response('HOST A SECRET', {
        status: 200,
        headers: {
          'X-File-Mtime': '1000',
          'X-File-Mtime-Nanos': '1000000000',
        },
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Check that open tabs has hostId: 'host-a'
    const tabs = (fm.vm as any).tabs
    assert.equal(tabs.length, 1)
    assert.equal(tabs[0].hostId, 'host-a', 'opened tab must have original host-a identity')
    assert.equal(tabs[0].text, 'HOST A SECRET')
    // And on host-b, activeTabId must NOT be pointing to this host-a tab
    assert.notEqual((fm.vm as any).activeTabId, tabs[0].id, 'host-b must not activate host-a tab')
  } finally {
    wrapper.unmount()
  }
})

test('F3: deleting a file waiting for save on host A does not delete on host B after switch', async () => {
  const deleteCalls: { hostId: string; path: string }[] = []
  let resolveSave: ((res: Response) => void) | null = null

  // Mock confirm to true
  const origConfirm = window.confirm
  window.confirm = () => true

  let wrapper: any = null
  try {
    globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input)
      if (url.endsWith('/api/hosts')) {
        return json({
          hosts: [
            { id: 'host-a', name: 'Host A', revision: 1, connectionRevision: 1, trusted: true },
            { id: 'host-b', name: 'Host B', revision: 1, connectionRevision: 1, trusted: true },
          ],
        })
      }
      if (url.endsWith('/api/host-settings')) {
        return json({ defaultHostId: 'host-a', revision: 1 })
      }
      if (url.includes('/working-directory')) {
        return json({ path: '/work', substituted: false })
      }
      if (url.includes('/files/list')) {
        return json({
          path: '/work',
          entries: [{ name: 'fileA.txt', is_dir: false, size: 10, mtime: 1000 }],
          truncated: false,
        })
      }
      if (url.includes('/files/read')) {
        return new Response('initial', {
          status: 200,
          headers: {
            'X-File-Mtime': '1000',
            'X-File-Mtime-Nanos': '1000000000',
          },
        })
      }
      if (url.includes('/files/write')) {
        return new Promise<Response>((resolve) => {
          resolveSave = resolve
        })
      }
      if (url.includes('/files/delete')) {
        const match = url.match(/\/api\/hosts\/([^/]+)\/files\/delete/)
        let body: any = {}
        try {
          body = typeof init?.body === 'string' ? JSON.parse(init.body) : {}
        } catch {}
        deleteCalls.push({
          hostId: match ? match[1] : 'local',
          path: body.path || '',
        })
        return json({ ok: true })
      }
      if (url.includes('/sessions')) {
        return json({ sessions: [{ name: 's1', windows: 1, attached: 1, created: 1000 }] })
      }
      return json({}, 404)
    }

    wrapper = mount(App)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const sessionList = wrapper.findComponent({ name: 'SessionList' })
    sessionList.vm.$emit('select', 's1')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fmBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    await fmBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const fm = wrapper.findComponent(FileManagerOverlay)
    // Open file
    const tree = wrapper.findComponent({ name: 'FileTree' })
    tree.vm.$emit('open', { name: 'fileA.txt', is_dir: false, size: 10, mtime: 1000 }, '/work/fileA.txt')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Make tab dirty so saveFile proceeds
    const tab = (fm.vm as any).tabs[0]
    assert.ok(tab, 'tab must be open')
    tab.text = 'modified text'
    await nextTick()

    // Trigger save (which hangs in flight)
    const savePromise = (fm.vm as any).save()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    // Trigger delete on file
    const deletePromise = (fm.vm as any).deleteTarget(
      { name: 'fileA.txt', is_dir: false, size: 10, mtime: 1000 },
      '/work/fileA.txt',
    )
    await nextTick()

    // While delete is waiting for save, switch host to host-b
    const select = wrapper.find('#host-select')
    await select.setValue('host-b')
    await select.trigger('change')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Now resolve save on host-a
    assert.ok(resolveSave !== null, 'save must be in flight')
    resolveSave!(json({ mtime: 2000 }))
    await savePromise
    await deletePromise
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Verify delete call was made to host-a and NOT host-b
    assert.equal(deleteCalls.length, 1, 'delete must be called once')
    assert.equal(deleteCalls[0].hostId, 'host-a', 'delete must be sent to original host-a')
    assert.equal(deleteCalls[0].path, '/work/fileA.txt')
  } finally {
    window.confirm = origConfirm
    wrapper?.unmount()
  }
})

test('RC-3: host discovery failure keeps dirty workspace and drafts mounted', async () => {
  let fail = false
  const origFetch = globalThis.fetch
  globalThis.fetch = async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return fail
        ? json({ error: 'temporary_failure' }, 503)
        : json({ hosts: [{ id: 'host-a', name: 'A', revision: 1, connectionRevision: 1, trusted: true }] })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/work', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({ path: '/work', entries: [], truncated: false })
    }
    if (url.includes('/sessions')) {
      return json({ sessions: [{ name: 's1', windows: 1, attached: 1, created: 1000 }] })
    }
    return json({}, 404)
  }
  const w = mount(App)
  const settle = async () => {
    await nextTick()
    await new Promise((r) => setTimeout(r, 25))
  }
  try {
    await settle()
    w.findComponent({ name: 'SessionList' }).vm.$emit('select', 's1')
    await settle()
    await w.find('[title="browse files in this session"]').trigger('click')
    await settle()

    const fm = w.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be mounted')
    ;(fm.vm as any).tabs.push({
      id: 1,
      hostId: 'host-a',
      revision: 1,
      path: '/work/a.txt',
      name: 'a.txt',
      text: 'MY UNSAVED DRAFT',
      saved: '',
      stamp: { millis: 1, nanos: null },
      size: 10,
      binary: false,
    })
    await settle()

    // Inject 503 error on host list refresh
    fail = true
    w.findComponent({ name: 'HostManagerModal' }).vm.$emit('hosts-changed')
    await settle()

    // Verify error banner is shown and editor remains mounted
    assert.ok(w.find('.app__discovery-state--error').exists(), 'error banner should be visible')
    assert.ok(w.findComponent(FileManagerOverlay).exists(), 'FileManagerOverlay must remain mounted on transient error')
    assert.equal((w.findComponent(FileManagerOverlay).vm as any).tabs.length, 1, 'draft must not be lost')
    assert.equal((w.findComponent(FileManagerOverlay).vm as any).tabs[0].text, 'MY UNSAVED DRAFT')

    // Retry and verify error cleared and draft preserved
    fail = false
    await w.find('.app__discovery-btn').trigger('click')
    await settle()

    assert.ok(w.findComponent(FileManagerOverlay).exists())
    assert.equal((w.findComponent(FileManagerOverlay).vm as any).tabs.length, 1, 'drafts preserved after successful retry')
    assert.equal((w.findComponent(FileManagerOverlay).vm as any).tabs[0].text, 'MY UNSAVED DRAFT')
  } finally {
    globalThis.fetch = origFetch
    w.unmount()
  }
})

test('session create and rename send expected connection revision and handle conflict', async () => {
  const origFetch = globalThis.fetch
  const calls: Array<{ url: string; method: string; headers: Headers; body: string }> = []
  let conflictMode = false

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    const headers = new Headers(init?.headers)
    const body = typeof init?.body === 'string' ? init.body : ''
    calls.push({ url, method, headers, body })

    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'remote-1', name: 'Remote 1', revision: 2, connectionRevision: 5, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'remote-1', revision: 1 })
    }
    if (url.includes('/api/hosts/remote-1/sessions') && method === 'GET') {
      return json({
        sessions: [{ name: 'sess-a', windows: 1, attached: 0, created: 1000 }],
      })
    }
    if (url.includes('/api/hosts/remote-1/sessions') && method === 'POST') {
      if (conflictMode) {
        return json({ error: 'host_revision_conflict' }, 409)
      }
      return json({ name: 'sess-new', windows: 1, attached: 0, created: 1001 }, 201)
    }
    if (url.includes('/api/hosts/remote-1/sessions/sess-a') && method === 'PATCH') {
      if (conflictMode) {
        return json({ error: 'host_revision_conflict' }, 409)
      }
      return json({ name: 'sess-renamed', windows: 1, attached: 0, created: 1000 }, 200)
    }
    return json({}, 404)
  }

  const w = mount(App)
  const settle = async () => {
    await nextTick()
    await new Promise((r) => setTimeout(r, 25))
  }

  try {
    await settle()

    // 1. Trigger session create
    w.findComponent({ name: 'SessionList' }).vm.$emit('create')
    await settle()

    const createCall = calls.find((c) => c.method === 'POST' && c.url.includes('/sessions'))
    assert.ok(createCall, 'POST session call should have been made')
    assert.equal(
      createCall.headers.get('X-Expected-Connection-Revision'),
      '5',
      'createSession must send X-Expected-Connection-Revision header matching host connectionRevision',
    )
    const createBody = JSON.parse(createCall.body)
    assert.equal(
      createBody.expected_connection_revision ?? createBody.expectedConnectionRevision,
      5,
      'createSession must send expected connection revision in payload',
    )

    // 2. Trigger session rename
    w.findComponent({ name: 'SessionList' }).vm.$emit('rename', 'sess-a', 'sess-renamed')
    await settle()

    const renameCall = calls.find((c) => c.method === 'PATCH' && c.url.includes('/sessions/sess-a'))
    assert.ok(renameCall, 'PATCH session call should have been made')
    assert.equal(
      renameCall.headers.get('X-Expected-Connection-Revision'),
      '5',
      'renameSession must send X-Expected-Connection-Revision header matching host connectionRevision',
    )
    const renameBody = JSON.parse(renameCall.body)
    assert.equal(
      renameBody.expected_connection_revision ?? renameBody.expectedConnectionRevision,
      5,
      'renameSession must send expected connection revision in payload',
    )

    // 3. Test conflict error handling on create and rename
    conflictMode = true
    w.findComponent({ name: 'SessionList' }).vm.$emit('create')
    await settle()

    let toasts = w.findAll('.toast')
    assert.ok(
      toasts.some((t) => t.text().includes('Could not create session on Remote 1: host configuration has changed')),
      `expected create conflict toast, found: ${toasts.map((t) => t.text()).join(', ')}`,
    )

    w.findComponent({ name: 'SessionList' }).vm.$emit('rename', 'sess-a', 'sess-conflict')
    await settle()

    toasts = w.findAll('.toast')
    assert.ok(
      toasts.some((t) => t.text().includes('Could not rename session on Remote 1: host configuration has changed')),
      `expected rename conflict toast, found: ${toasts.map((t) => t.text()).join(', ')}`,
    )
  } finally {
    globalThis.fetch = origFetch
    w.unmount()
  }
})

test('R5-W2: late session create response from old connection revision does not select or refresh new workspace', async () => {
  localStorage.setItem('vtc:recent-host', 'host-a')
  const origFetch = globalThis.fetch
  let hostConnRev = 1
  let hostRev = 1
  let createPending = false
  let releaseCreate: (res: Response) => void = () => {}
  const createPromise = new Promise<Response>((resolve) => {
    releaseCreate = resolve
  })

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input)
    const method = init?.method ?? 'GET'

    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'host-a', name: 'Host A', revision: hostRev, connectionRevision: hostConnRev, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'host-a', revision: 1 })
    }
    if (url.includes('/api/hosts/host-a/sessions') && method === 'GET') {
      if (hostConnRev === 1) {
        return json({
          sessions: [{ name: 'sess-1', windows: 1, attached: 0, created: 1000 }],
        })
      }
      return json({
        sessions: [{ name: 'new-target-session', windows: 1, attached: 0, created: 2000 }],
      })
    }
    if (url.includes('/api/hosts/host-a/sessions') && method === 'POST') {
      createPending = true
      return createPromise
    }
    return json({}, 404)
  }

  const w = mount(App)
  const settle = async () => {
    await nextTick()
    await new Promise((r) => setTimeout(r, 25))
  }

  try {
    await settle()

    // 1. Trigger session create on host-a with connectionRevision 1
    w.findComponent({ name: 'SessionList' }).vm.$emit('create')
    await settle()
    assert.ok(createPending, 'create request should be in flight')

    // 2. While create is in flight, host-a updates connectionRevision from 1 to 2
    hostRev = 2
    hostConnRev = 2
    w.findComponent({ name: 'HostManagerModal' }).vm.$emit('hosts-changed')
    await settle()

    // Verify session list now shows new-target-session
    const list = w.findComponent({ name: 'SessionList' })
    assert.ok(
      (list.props('sessions') as any[]).some((s) => s.name === 'new-target-session'),
      'workspace should have updated sessions for rev 2',
    )

    // 3. Now release the late create response from connectionRevision 1
    releaseCreate(json({ name: 'old-target-session', windows: 1, attached: 0, created: 1001 }, 201))
    await settle()

    // 4. Verify TerminalView does NOT select old-target-session
    const term = w.findComponent({ name: 'TerminalView' })
    const selectedSession = term.exists() ? term.props('session') : null
    assert.notEqual(
      selectedSession,
      'old-target-session',
      'late create response from old connection revision must not select session on new connection',
    )
  } finally {
    globalThis.fetch = origFetch
    w.unmount()
  }
})
