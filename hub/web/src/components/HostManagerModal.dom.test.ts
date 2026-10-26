import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../../test/dom.mjs'

import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import HostManagerModal from './HostManagerModal.vue'
import { setToken } from '../api'
import { setConfig, resetConfigForTest, type ClientConfig } from '../config'
import { resetToastsForTest } from '../toasts'
import { setLocale, resetLocaleForTest } from '../i18n/index'

const testConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
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
})

afterEach(() => {
  resetToastsForTest()
  resetLocaleForTest('en')
})

function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

interface RecordedRequest {
  url: string
  method: string
  headers: Record<string, string>
  body: unknown
}

function setupFetchMock(
  handler: (req: RecordedRequest) => Response | Promise<Response>,
): RecordedRequest[] {
  const requests: RecordedRequest[] = []
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    const headers: Record<string, string> = {}
    if (init?.headers) {
      new Headers(init.headers).forEach((v, k) => {
        headers[k] = v
      })
    }
    let body: unknown = null
    if (init?.body) {
      try {
        body = JSON.parse(String(init.body))
      } catch {
        body = String(init.body)
      }
    }
    const req: RecordedRequest = { url, method, headers, body }
    requests.push(req)
    return handler(req)
  }
  return requests
}

const mockHosts = [
  {
    id: 'local',
    name: 'Local Host',
    isLocal: true,
    revision: 1,
    credentialType: 'none',
    status: 'connected',
    createdAt: 1000,
    updatedAt: 1000,
  },
  {
    id: 'server-1',
    name: 'Production Server',
    address: 'prod.example.com',
    port: 22,
    username: 'deploy',
    credentialType: 'password',
    isLocal: false,
    revision: 3,
    status: 'connected',
    fingerprint: 'SHA256:abc123xyz',
    keyType: 'ssh-ed25519',
    createdAt: 2000,
    updatedAt: 3000,
  },
]

test('HostManagerModal: renders host list and capability test feedback', async () => {
  setToken('test-token')
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url === '/api/hosts/server-1/capabilities' && req.method === 'GET') {
      return json({
        ssh: true,
        tmux: true,
        sftp: true,
        posixRename: true,
      })
    }
    return json({ error: 'not found' }, 404)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Verify modal elements
  assert.equal(wrapper.find('.host-modal__title').text(), 'Host Management')
  const hostItems = wrapper.findAll('.host-card')
  assert.equal(hostItems.length, 2)
  assert.ok(wrapper.text().includes('Local Host'))
  assert.ok(wrapper.text().includes('Production Server'))
  assert.ok(wrapper.text().includes('Local system execution (no SSH)'))

  // Click Test Connection on server-1
  const testBtn = hostItems[1].find('button[aria-label="Test connection for Production Server"]')
  assert.ok(testBtn.exists())
  await testBtn.trigger('click')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Verify capability indicators are shown
  assert.ok(wrapper.text().includes('SSH: Connected'))
  assert.ok(wrapper.text().includes('tmux: Available'))
  assert.ok(wrapper.text().includes('SFTP: Available'))
  assert.ok(wrapper.text().includes('Atomic Overwrite: Supported'))

  wrapper.unmount()
})

test('HostManagerModal: keyboard navigation - Escape and Tab focus trap', async () => {
  setToken('test-token')
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts') {
      return json({ hosts: mockHosts })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Click Add Host to switch view to 'add'
  const addBtn = wrapper.find('.host-modal__add-btn')
  assert.ok(addBtn.exists())
  await addBtn.trigger('click')
  await nextTick()
  assert.ok(wrapper.find('form.host-form').exists())

  // Pressing Escape while in form view should return to list view without closing modal
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  await nextTick()
  assert.ok(!wrapper.find('form.host-form').exists())
  assert.equal(wrapper.emitted('close'), undefined)

  // Pressing Escape while in list view emits close
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  await nextTick()
  assert.ok(wrapper.emitted('close'))

  wrapper.unmount()
})

test('HostManagerModal: field validation errors on add', async () => {
  setToken('test-token')
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts') {
      return json({ hosts: mockHosts })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Switch to add view
  const addBtn = wrapper.find('.host-modal__add-btn')
  assert.ok(addBtn.exists())
  await addBtn.trigger('click')
  await nextTick()

  // Attempt submit without filling anything
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()

  const text = wrapper.text()
  assert.ok(text.includes('Host ID is required'))
  assert.ok(text.includes('Host name is required'))
  assert.ok(text.includes('Host address is required'))
  assert.ok(text.includes('Username is required'))
  assert.ok(text.includes('Password is required for new host'))

  // Test reserved ID "local"
  const idInput = wrapper.find('#host-field-id')
  await idInput.setValue('local')
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()
  assert.ok(wrapper.text().includes('Host ID "local" is reserved for the built-in host'))

  // Test invalid port
  const portInput = wrapper.find('#host-field-port')
  await portInput.setValue('99999')
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()
  assert.ok(wrapper.text().includes('Port must be between 1 and 65535'))

  // Test private key validation when credentialType is private_key
  const keyRadio = wrapper.find('input[type="radio"][value="private_key"]')
  await keyRadio.setValue()
  await nextTick()
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()
  assert.ok(wrapper.text().includes('Private key is required for new host'))

  wrapper.unmount()
})

test('HostManagerModal: displayed form field errors dynamically update on live language switch', async () => {
  resetLocaleForTest('en')
  setToken('test-token')
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts') {
      return json({ hosts: mockHosts })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Switch to add view
  const addBtn = wrapper.find('.host-modal__add-btn')
  assert.ok(addBtn.exists())
  await addBtn.trigger('click')
  await nextTick()

  // Submit empty form in English mode
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()

  assert.ok(wrapper.text().includes('Host ID is required'))
  assert.ok(wrapper.text().includes('Host name is required'))
  assert.ok(wrapper.text().includes('Host address is required'))

  // Switch to Chinese without resubmitting form
  setLocale('zh-CN')
  await nextTick()

  assert.ok(wrapper.text().includes('主机标识为必填项'))
  assert.ok(wrapper.text().includes('主机名称为必填项'))
  assert.ok(wrapper.text().includes('SSH 地址为必填项'))
  assert.ok(!wrapper.text().includes('Host ID is required'))

  // Switch back to English
  setLocale('en')
  await nextTick()
  assert.ok(wrapper.text().includes('Host ID is required'))
  assert.ok(!wrapper.text().includes('主机标识为必填项'))

  wrapper.unmount()
})

test('HostManagerModal: secret retention semantics on edit', async () => {
  setToken('test-token')
  let updateBody: unknown = null
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url.startsWith('/api/hosts/server-1') && req.method === 'PUT') {
      updateBody = req.body
      return json({
        id: 'server-1',
        name: 'Production Server Updated',
        address: 'prod.example.com',
        port: 22,
        username: 'deploy',
        credentialType: 'password',
        isLocal: false,
        revision: 4,
        createdAt: 2000,
        updatedAt: 4000,
      })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Find edit button for server-1
  const editBtn = wrapper.find('button[aria-label="Edit Production Server"]')
  assert.ok(editBtn.exists())
  await editBtn.trigger('click')
  await nextTick()

  // Verify form is populated with existing data
  assert.equal((wrapper.find('#host-field-name').element as HTMLInputElement).value, 'Production Server')
  assert.equal((wrapper.find('#host-field-address').element as HTMLInputElement).value, 'prod.example.com')
  // Password input MUST be empty
  assert.equal((wrapper.find('#host-field-password').element as HTMLInputElement).value, '')

  // Update only the name, leaving password blank
  await wrapper.find('#host-field-name').setValue('Production Server Updated')
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))

  // Verify that update request was made with credentials OMITTED
  assert.ok(updateBody !== null)
  const payload = updateBody as Record<string, unknown>
  assert.equal(payload.name, 'Production Server Updated')
  assert.equal(payload.address, 'prod.example.com')
  assert.equal(payload.credentials, undefined, 'credentials field must be omitted when left blank')
  assert.equal(payload.revision, 3)

  wrapper.unmount()
})

test('HostManagerModal: explicit secret replacement on edit', async () => {
  setToken('test-token')
  let updateBody: unknown = null
  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url.startsWith('/api/hosts/server-1') && req.method === 'PUT') {
      updateBody = req.body
      return json({
        id: 'server-1',
        name: 'Production Server',
        address: 'prod.example.com',
        port: 22,
        username: 'deploy',
        credentialType: 'password',
        isLocal: false,
        revision: 4,
        createdAt: 2000,
        updatedAt: 4000,
      })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Edit server-1
  const editBtn = wrapper.find('button[aria-label="Edit Production Server"]')
  assert.ok(editBtn.exists())
  await editBtn.trigger('click')
  await nextTick()

  // Enter a new password
  await wrapper.find('#host-field-password').setValue('new-super-secret-password')
  await wrapper.find('form.host-form').trigger('submit')
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))

  assert.ok(updateBody !== null)
  const payload = updateBody as { credentials?: { password?: string } }
  assert.deepEqual(payload.credentials, { password: 'new-super-secret-password' })

  wrapper.unmount()
})

for (const succeeds of [true, false]) {
  test(`HostManagerModal: existing host trust ${succeeds ? 'updates save revision' : 'failure stays unconfirmed'}`, async () => {
    setToken('test-token')
    let currentHost = { ...mockHosts[1]!, trusted: false }
    let finishTrust!: (response: Response) => void
    const trustResponse = new Promise<Response>((resolve) => { finishTrust = resolve })
    const requests = setupFetchMock(async (req) => {
      if (req.url === '/api/hosts') return json({ hosts: [currentHost] })
      if (req.url === '/api/hosts/server-1/probe') {
        return json({ endpoint: 'prod.example.com:22', fingerprint: 'SHA256:test', keyType: 'ssh-ed25519', challengeToken: 'existing-challenge' })
      }
      if (req.url === '/api/hosts/server-1/trust') return trustResponse
      if (req.method === 'PUT') {
        const body = req.body as { revision: number }
        if (body.revision !== currentHost.revision) return json({ error: 'host_revision_conflict' }, 409)
        return json({ ...currentHost, revision: currentHost.revision + 1 })
      }
      return json({ defaultHostId: null, revision: 1 })
    })
    const wrapper = mount(HostManagerModal, { props: { open: true }, attachTo: document.body })
    try {
      await flushPromises()
      await wrapper.find('button[aria-label="Edit Production Server"]').trigger('click')
      await wrapper.find('#host-field-name').setValue('Unsaved new name')
      await wrapper.find('#host-field-password').setValue('new-secret')
      await wrapper.find('.host-probe__probe-btn').trigger('click')
      await flushPromises()
      await wrapper.find('.host-probe__trust-btn').trigger('click')
      await flushPromises()
      assert.equal(wrapper.find('.host-probe-trusted-indicator').exists(), false)
      assert.equal(wrapper.find('button[type="submit"]').attributes('disabled'), '')
      await wrapper.find('form.host-form').trigger('submit')
      assert.equal(requests.filter((r) => r.method === 'PUT').length, 0)

      if (succeeds) {
        currentHost = { ...currentHost, revision: 4, trusted: true }
        finishTrust(json(currentHost))
      } else {
        finishTrust(json({ error: 'challenge_expired' }, 409))
      }
      await flushPromises()
      assert.equal(wrapper.find('.host-probe-trusted-indicator').exists(), succeeds)
      assert.equal((wrapper.find('#host-field-name').element as HTMLInputElement).value, 'Unsaved new name')
      assert.equal((wrapper.find('#host-field-password').element as HTMLInputElement).value, 'new-secret')
      if (!succeeds) assert.ok(wrapper.text().includes('challenge_expired'))
      await wrapper.find('form.host-form').trigger('submit')
      await flushPromises()
      const update = requests.find((r) => r.method === 'PUT')!
      assert.ok(update)
      const body = update.body as Record<string, unknown>
      assert.equal(body.revision, succeeds ? 4 : 3)
      assert.ok(update.url.endsWith(`?revision=${succeeds ? 4 : 3}`))
      assert.equal(body.challengeToken, undefined)
      assert.equal(body.name, 'Unsaved new name')
      assert.deepEqual(body.credentials, { password: 'new-secret' })
      assert.equal(wrapper.text().includes('host_revision_conflict'), false)
    } finally {
      wrapper.unmount()
    }
  })
}

test('HostManagerModal: draft fingerprint probing and trust confirmation', async () => {
  setToken('test-token')
  let probeCalled = false
  let createBody: unknown = null

  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url === '/api/hosts/probe' && req.method === 'POST') {
      probeCalled = true
      return json({
        endpoint: 'staging.example.com:2222',
        fingerprint: 'SHA256:fingerprint_xyz789',
        keyType: 'ssh-rsa',
        challengeToken: 'chal-token-999',
      })
    }
    if (req.url === '/api/hosts' && req.method === 'POST') {
      createBody = req.body
      return json(
        {
          id: 'staging',
          name: 'Staging Server',
          address: 'staging.example.com',
          port: 2222,
          username: 'admin',
          credentialType: 'password',
          isLocal: false,
          revision: 1,
          createdAt: 5000,
          updatedAt: 5000,
        },
        201,
      )
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Open add form
  const addBtn = wrapper.find('.host-modal__add-btn')
  assert.ok(addBtn.exists())
  await addBtn.trigger('click')
  await nextTick()

  // Fill in address & port
  await wrapper.find('#host-field-id').setValue('staging')
  await wrapper.find('#host-field-name').setValue('Staging Server')
  await wrapper.find('#host-field-address').setValue('staging.example.com')
  await wrapper.find('#host-field-port').setValue('2222')
  await wrapper.find('#host-field-username').setValue('admin')
  await wrapper.find('#host-field-password').setValue('admin-secret')

  // Click Probe Host
  const probeBtn = wrapper.find('.host-probe__probe-btn')
  assert.ok(probeBtn.exists())
  await probeBtn.trigger('click')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  assert.equal(probeCalled, true)
  assert.ok(wrapper.text().includes('SHA256:fingerprint_xyz789'))
  assert.ok(wrapper.text().includes('ssh-rsa'))

  // Click Trust & Pin Fingerprint
  const trustBtn = wrapper.find('.host-probe__trust-btn')
  assert.ok(trustBtn.exists())
  await trustBtn.trigger('click')
  await nextTick()

  assert.ok(wrapper.text().includes('Fingerprint trusted'))

  // Submit form
  await wrapper.find('form.host-form').trigger('submit')
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.ok(createBody !== null)
  const payload = createBody as { challengeToken?: string }
  assert.equal(payload.challengeToken, 'chal-token-999')

  wrapper.unmount()
})

test('HostManagerModal: delete confirmation workflow', async () => {
  setToken('test-token')
  let deletedHostId = ''
  let deleteRevision = ''

  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url.startsWith('/api/hosts/') && req.method === 'DELETE') {
      const parsedUrl = new URL(req.url, 'http://localhost')
      deletedHostId = parsedUrl.pathname.split('/').pop() ?? ''
      deleteRevision = parsedUrl.searchParams.get('revision') ?? ''
      return json({ ok: true })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Click delete button for server-1
  const deleteBtn = wrapper.find('button[aria-label="Remove Production Server"]')
  assert.ok(deleteBtn.exists())
  await deleteBtn.trigger('click')
  await nextTick()

  // Verify confirmation banner appears
  assert.ok(wrapper.text().includes('Remove Host "Production Server"?'))
  assert.ok(wrapper.text().includes('Remote tmux sessions will NOT be killed'))

  // Cancel deletion
  await wrapper.find('.host-confirm__cancel').trigger('click')
  await nextTick()
  assert.ok(!wrapper.text().includes('Remove Host "Production Server"?'))
  assert.equal(deletedHostId, '')

  // Click delete again and confirm
  await deleteBtn.trigger('click')
  await nextTick()
  await wrapper.find('.host-confirm__delete').trigger('click')
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(deletedHostId, 'server-1')
  assert.equal(deleteRevision, '3')

  wrapper.unmount()
})

test('deleting host with unsaved editor drafts displays dirty warning before confirmation', async () => {
  setToken('dummy-token')

  let deletedHostId = ''
  setupFetchMock((req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({
        hosts: [
          {
            id: 'server-1',
            name: 'Production Server',
            address: 'prod.example.com',
            port: 22,
            username: 'admin',
            trusted: true,
            hasCredential: true,
            revision: 3,
          },
        ],
      })
    }
    if (req.url.startsWith('/api/hosts/') && req.method === 'DELETE') {
      const parsedUrl = new URL(req.url, 'http://localhost')
      deletedHostId = parsedUrl.pathname.split('/').pop() ?? ''
      return json({ ok: true })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true, dirtyHosts: ['server-1'] },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Click delete button for server-1
  const deleteBtn = wrapper.find('button[aria-label="Remove Production Server"]')
  assert.ok(deleteBtn.exists())
  await deleteBtn.trigger('click')
  await nextTick()

  // Verify warning about unsaved editor drafts exists
  const warning = wrapper.find('.host-confirm-dialog__warning')
  assert.ok(warning.exists(), 'warning about unsaved drafts must exist')
  assert.ok(warning.text().includes('You have unsaved editor drafts on this host'))
  assert.ok(wrapper.text().includes('Remote tmux sessions will NOT be killed'))

  // Confirm delete and check host-deleted emission
  await wrapper.find('.host-confirm__delete').trigger('click')
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(deletedHostId, 'server-1')
  const emitted = wrapper.emitted('host-deleted')
  assert.ok(emitted, 'host-deleted must be emitted')
  assert.deepEqual(emitted[0], ['server-1'])

  wrapper.unmount()
})

test('HostManagerModal: uniform controls on local host (edit, test, default, delete)', async () => {
  setToken('test-token')
  let lastSettingsUpdate: unknown = null

  setupFetchMock((req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({
        hosts: [
          {
            id: 'local',
            name: 'Local Host',
            connectionType: 'local',
            revision: 1,
            connectionRevision: 1,
            hasCredential: false,
            trusted: true,
          },
          {
            id: 'server-1',
            name: 'Production Server',
            connectionType: 'ssh',
            address: 'prod.example.com',
            port: 22,
            username: 'deploy',
            credentialType: 'password',
            hasCredential: true,
            trusted: true,
            revision: 2,
            connectionRevision: 1,
          },
        ],
      })
    }
    if (req.url === '/api/host-settings' && req.method === 'GET') {
      return json({ defaultHostId: 'server-1', revision: 2 })
    }
    if (req.url.startsWith('/api/host-settings') && req.method === 'PUT') {
      lastSettingsUpdate = req.body
      return json({ defaultHostId: 'local', revision: 3 })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Local host card controls must NOT be hidden
  const localTestBtn = wrapper.find('button[aria-label="Test connection for Local Host"]')
  assert.ok(localTestBtn.exists(), 'local host must have Test button')

  const localEditBtn = wrapper.find('button[aria-label="Edit Local Host"]')
  assert.ok(localEditBtn.exists(), 'local host must have Edit button')

  const localSetDefaultBtn = wrapper.find('button[aria-label="Set Local Host as default host"]')
  assert.ok(localSetDefaultBtn.exists(), 'local host must have Set Default button when not default')

  const localDeleteBtn = wrapper.find('button[aria-label="Remove Local Host"]')
  assert.ok(localDeleteBtn.exists(), 'local host must have Delete button')

  // Click Set Default on local host
  await localSetDefaultBtn.trigger('click')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  assert.ok(lastSettingsUpdate !== null)
  assert.deepEqual(lastSettingsUpdate, { defaultHostId: 'local', revision: 2 })
  assert.ok(wrapper.emitted('default-changed'))

  wrapper.unmount()
})

test('HostManagerModal: editing local host hides SSH fields and updates name only', async () => {
  setToken('test-token')
  let updateBody: unknown = null

  setupFetchMock((req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({
        hosts: [
          {
            id: 'local',
            name: 'Local Host',
            connectionType: 'local',
            revision: 1,
            connectionRevision: 1,
            hasCredential: false,
            trusted: true,
          },
        ],
      })
    }
    if (req.url.startsWith('/api/hosts/local') && req.method === 'PUT') {
      updateBody = req.body
      return json({
        id: 'local',
        name: 'Renamed Local',
        connectionType: 'local',
        revision: 2,
        connectionRevision: 1,
        hasCredential: false,
        trusted: true,
      })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Click Edit Local Host
  const editBtn = wrapper.find('button[aria-label="Edit Local Host"]')
  assert.ok(editBtn.exists())
  await editBtn.trigger('click')
  await nextTick()

  // Verify form view
  assert.ok(wrapper.find('form.host-form').exists())
  assert.equal((wrapper.find('#host-field-id').element as HTMLInputElement).value, 'local')
  assert.equal((wrapper.find('#host-field-id').element as HTMLInputElement).disabled, true)
  assert.equal((wrapper.find('#host-field-name').element as HTMLInputElement).value, 'Local Host')

  // SSH fields must NOT exist in the DOM for local host
  assert.ok(!wrapper.find('#host-field-address').exists(), 'address field must not exist for local')
  assert.ok(!wrapper.find('#host-field-port').exists(), 'port field must not exist for local')
  assert.ok(!wrapper.find('#host-field-username').exists(), 'username field must not exist for local')
  assert.ok(!wrapper.find('.host-probe__probe-btn').exists(), 'server key probe must not exist for local')
  assert.ok(!wrapper.find('input[name="credentialType"]').exists(), 'credentials must not exist for local')

  // Change name and submit
  await wrapper.find('#host-field-name').setValue('Renamed Local')
  await wrapper.find('form.host-form').trigger('submit')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  assert.ok(updateBody !== null)
  const payload = updateBody as Record<string, unknown>
  assert.equal(payload.id, 'local')
  assert.equal(payload.name, 'Renamed Local')
  assert.equal(payload.connectionType, 'local')
  assert.equal(payload.address, undefined, 'irrelevant address must not be sent for local host')
  assert.equal(payload.port, undefined, 'irrelevant port must not be sent for local host')
  assert.equal(payload.credentials, undefined, 'irrelevant credentials must not be sent for local host')

  wrapper.unmount()
})

test('HostManagerModal: adding local host validates ID and name without SSH fields', async () => {
  setToken('test-token')
  let createBody: unknown = null

  setupFetchMock((req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: [] })
    }
    if (req.url === '/api/hosts' && req.method === 'POST') {
      createBody = req.body
      return json(
        {
          id: 'local',
          name: 'Primary System',
          connectionType: 'local',
          revision: 1,
          connectionRevision: 1,
          hasCredential: false,
          trusted: true,
        },
        201,
      )
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Open add form
  await wrapper.find('.host-modal__add-btn').trigger('click')
  await nextTick()

  // Select Local connection type
  const localRadio = wrapper.find('input[type="radio"][value="local"]')
  assert.ok(localRadio.exists(), 'local connection type radio must exist')
  await localRadio.setValue()
  await nextTick()

  // Verify SSH fields disappeared
  assert.ok(!wrapper.find('#host-field-address').exists())
  assert.ok(!wrapper.find('#host-field-port').exists())
  assert.ok(!wrapper.find('#host-field-username').exists())
  assert.ok(!wrapper.find('.host-probe__probe-btn').exists())

  // Fill in ID and name
  await wrapper.find('#host-field-id').setValue('local')
  await wrapper.find('#host-field-name').setValue('Primary System')

  // Submit form
  await wrapper.find('form.host-form').trigger('submit')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  assert.ok(createBody !== null)
  const payload = createBody as Record<string, unknown>
  assert.equal(payload.id, 'local')
  assert.equal(payload.name, 'Primary System')
  assert.equal(payload.connectionType, 'local')
  assert.equal(payload.address, undefined)
  assert.equal(payload.port, undefined)
  assert.equal(payload.username, undefined)

  wrapper.unmount()
})

test('HostManagerModal: structured capability status renders truthfully rather than by object truthiness', async () => {
  setToken('test-token')

  setupFetchMock((req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({
        hosts: [
          {
            id: 'local',
            name: 'Local Host',
            connectionType: 'local',
            revision: 1,
            connectionRevision: 1,
            hasCredential: false,
            trusted: true,
          },
          {
            id: 'server-1',
            name: 'Remote Offline',
            connectionType: 'ssh',
            address: 'offline.example.com',
            port: 22,
            username: 'deploy',
            hasCredential: true,
            trusted: true,
            revision: 1,
            connectionRevision: 1,
          },
        ],
      })
    }
    if (req.url === '/api/hosts/local/capabilities') {
      // Local host returns not_applicable for SSH and SFTP
      return json({
        hostId: 'local',
        connectionType: 'local',
        ssh: { state: 'not_applicable', available: false },
        tmux: { state: 'available', available: true },
        sftp: { state: 'not_applicable', available: false },
        posixRename: false,
      })
    }
    if (req.url === '/api/hosts/server-1/capabilities') {
      // SSH host with unavailable status (non-null object!)
      return json({
        hostId: 'server-1',
        connectionType: 'ssh',
        ssh: { state: 'unavailable', available: false, error: 'connection refused' },
        tmux: { state: 'unknown', available: false },
        sftp: { state: 'unavailable', available: false },
        posixRename: false,
      })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // 1. Test connection on local host
  const localTestBtn = wrapper.find('button[aria-label="Test connection for Local Host"]')
  await localTestBtn.trigger('click')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Must render N/A for SSH and SFTP, NOT 'Connected' or 'Available'!
  assert.ok(wrapper.text().includes('SSH: N/A'), 'SSH on local must render N/A')
  assert.ok(!wrapper.text().includes('SSH: Connected'), 'SSH on local must not render Connected')
  assert.ok(wrapper.text().includes('tmux: Available'), 'tmux on local must render Available')
  assert.ok(wrapper.text().includes('SFTP: N/A'), 'SFTP on local must render N/A')
  assert.ok(!wrapper.text().includes('Atomic Overwrite'), 'Atomic Overwrite must not render when SFTP is N/A')

  // 2. Test connection on remote offline host
  const remoteTestBtn = wrapper.find('button[aria-label="Test connection for Remote Offline"]')
  await remoteTestBtn.trigger('click')
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // Non-null object with available=false must render Offline, NOT Connected!
  assert.ok(wrapper.text().includes('SSH: Offline'), 'SSH on offline host must render Offline')
  assert.ok(wrapper.text().includes('tmux: Missing'), 'tmux on offline host must render Missing')
  assert.ok(wrapper.text().includes('SFTP: Unavailable'), 'SFTP on offline host must render Unavailable')

  wrapper.unmount()
})

test('I18N-R2-02: general form submit errors map to localized descriptions and update dynamically on language switch', async () => {
  resetLocaleForTest('zh-CN')
  setToken('test-token')
  let returnStatus = 409
  let returnError = 'duplicate_local_host'

  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts' && req.method === 'GET') {
      return json({ hosts: mockHosts })
    }
    if (req.url === '/api/hosts' && req.method === 'POST') {
      return json({ error: returnError }, returnStatus)
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  try {
    // 1. Switch to add view and select local execution
    await wrapper.find('.host-modal__add-btn').trigger('click')
    await nextTick()

    const localRadio = wrapper.find('input[type="radio"][value="local"]')
    assert.ok(localRadio.exists(), 'local connection type radio must exist')
    await localRadio.setValue()
    await nextTick()

    // Fill valid form
    await wrapper.find('#host-field-id').setValue('local')
    await wrapper.find('#host-field-name').setValue('My Local')
    await nextTick()

    // Submit form -> triggers duplicate_local_host
    await wrapper.find('form.host-form').trigger('submit')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    // Assert: localized Chinese message renders and does not show bare error code
    const alertEl = wrapper.find('.host-alert--danger')
    assert.ok(alertEl.exists(), 'danger alert must exist')
    assert.equal(
      alertEl.text(),
      '本地主机已存在，不能重复添加。',
      'must show localized Chinese message for duplicate_local_host',
    )
    assert.ok(!alertEl.text().includes('duplicate_local_host'))

    // Form inputs must remain preserved
    assert.equal((wrapper.find('#host-field-id').element as HTMLInputElement).value, 'local')
    assert.equal((wrapper.find('#host-field-name').element as HTMLInputElement).value, 'My Local')

    // Switch to English without resubmitting
    setLocale('en')
    await nextTick()
    assert.equal(
      alertEl.text(),
      'A local host already exists and cannot be duplicated.',
      'must update to English upon locale switch',
    )

    // Switch back to Chinese
    setLocale('zh-CN')
    await nextTick()
    assert.equal(
      alertEl.text(),
      '本地主机已存在，不能重复添加。',
    )

    // 2. Test unknown diagnostic error
    returnStatus = 500
    returnError = 'sqlite_busy'
    await wrapper.find('form.host-form').trigger('submit')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    assert.equal(
      wrapper.find('.host-alert--danger').text(),
      '保存主机失败：sqlite_busy',
      'unknown diagnostic must be rendered with localized context in Chinese',
    )

    // Switch to English
    setLocale('en')
    await nextTick()
    assert.equal(
      wrapper.find('.host-alert--danger').text(),
      'Failed to save host: sqlite_busy',
      'unknown diagnostic must be rendered with localized context in English',
    )
  } finally {
    resetLocaleForTest('en')
    wrapper.unmount()
  }
})

test('I18N-R2-03: host capability pills render localized labels without duplicate prefixes, and button aria-labels are localized', async () => {
  resetLocaleForTest('zh-CN')
  setToken('test-token')

  setupFetchMock(async (req) => {
    if (req.url === '/api/hosts') {
      return json({ hosts: mockHosts })
    }
    if (req.url === '/api/hosts/server-1/capabilities') {
      return json({
        hostId: 'server-1',
        revision: 3,
        online: true,
        ssh: { state: 'available', available: true },
        tmux: { state: 'available', available: true },
        sftp: { state: 'available', available: true },
        posixRename: true,
      })
    }
    return json({}, 200)
  })

  const wrapper = mount(HostManagerModal, {
    props: { open: true },
    attachTo: document.body,
  })
  await nextTick()
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  try {
    // 1. Verify localized aria-labels on host action buttons in Chinese
    assert.ok(
      wrapper.find('button[aria-label="测试 Production Server 的连接"]').exists(),
      'Test connection button must have Chinese aria-label',
    )
    assert.ok(
      wrapper.find('button[aria-label="编辑 Production Server"]').exists(),
      'Edit button must have Chinese aria-label',
    )
    assert.ok(
      wrapper.find('button[aria-label="将 Production Server 设为默认主机"]').exists(),
      'Set default button must have Chinese aria-label',
    )
    assert.ok(
      wrapper.find('button[aria-label="移除 Production Server"]').exists(),
      'Remove button must have Chinese aria-label',
    )

    // Test capabilities on Production Server
    const testBtn = wrapper.find('button[aria-label="测试 Production Server 的连接"]')
    await testBtn.trigger('click')
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    // Verify capability pills render in Chinese without duplicate prefix
    assert.ok(wrapper.text().includes('SSH: 已连接'))
    assert.ok(wrapper.text().includes('tmux: 可用'))
    assert.ok(wrapper.text().includes('SFTP: 可用'))
    assert.ok(wrapper.text().includes('原子覆盖: 支持'))
    assert.ok(
      !wrapper.text().includes('Atomic Overwrite: 原子覆盖'),
      'Must NOT produce duplicate English prefix in Chinese mode',
    )

    // Switch to English
    setLocale('en')
    await nextTick()

    assert.ok(wrapper.text().includes('SSH: Connected'))
    assert.ok(wrapper.text().includes('tmux: Available'))
    assert.ok(wrapper.text().includes('SFTP: Available'))
    assert.ok(wrapper.text().includes('Atomic Overwrite: Supported'))

    assert.ok(
      wrapper.find('button[aria-label="Test connection for Production Server"]').exists(),
      'Test connection button must have English aria-label',
    )
    assert.ok(
      wrapper.find('button[aria-label="Edit Production Server"]').exists(),
      'Edit button must have English aria-label',
    )
  } finally {
    resetLocaleForTest('en')
    wrapper.unmount()
  }
})
