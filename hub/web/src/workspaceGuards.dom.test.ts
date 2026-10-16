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

