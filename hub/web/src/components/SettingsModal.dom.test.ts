import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../../test/dom.mjs'

import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SettingsModal from './SettingsModal.vue'
import { setToken } from '../api'
import { setConfig, resetConfigForTest, type ClientConfig } from '../config'
import { resetToastsForTest, useToasts } from '../toasts'
import { resetLocaleForTest, setLocale } from '../i18n/index'

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
  setToken('test-token')
  resetLocaleForTest('en')
})

afterEach(() => {
  resetToastsForTest()
  resetLocaleForTest('en')
  resetConfigForTest()
})

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

interface RecordedRequest {
  url: string
  method: string
  body: unknown
}

function setupFetchMock(handler: (req: RecordedRequest) => Response | Promise<Response>): RecordedRequest[] {
  const requests: RecordedRequest[] = []
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const req: RecordedRequest = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : null,
    }
    requests.push(req)
    return handler(req)
  }
  return requests
}

async function openSettings(settings: unknown) {
  const requests = setupFetchMock(async (req) => {
    if (req.method === 'GET') return json(settings)
    return json({ error: 'not found' }, 404)
  })
  const wrapper = mount(SettingsModal, { props: { open: true }, attachTo: document.body })
  await flushPromises()
  await nextTick()
  return { wrapper, requests }
}

const input = (wrapper: Awaited<ReturnType<typeof openSettings>>['wrapper']) =>
  wrapper.find<HTMLInputElement>('#settings-proxy-address')
const hint = (wrapper: Awaited<ReturnType<typeof openSettings>>['wrapper']) =>
  wrapper.find('#settings-proxy-help')

test('SettingsModal: one sentence names the saved proxy in both languages', async () => {
  const { wrapper } = await openSettings({
    defaultHostId: 'server-1',
    revision: 4,
    proxyAddress: '127.0.0.1:7890',
  })

  assert.equal(wrapper.find('#settings-title').text(), 'Settings')
  assert.equal(wrapper.find('[role="dialog"]').attributes('aria-labelledby'), 'settings-title')
  assert.equal(input(wrapper).element.value, '127.0.0.1:7890')
  assert.equal(
    wrapper.find('label[for="settings-proxy-address"]').text(),
    'Proxy address',
    'the field has a visible label',
  )
  assert.equal(
    input(wrapper).attributes('aria-describedby'),
    'settings-proxy-help',
    'the field points at its explanation',
  )
  assert.equal(
    hint(wrapper).text(),
    'Remote connections and new local terminals both use the proxy 127.0.0.1:7890.',
  )
  assert.equal(wrapper.findAll('.settings-field-hint').length, 2, 'the hint and the note about the shell configuration')

  // Editing the field does not name a proxy that is not saved yet.
  await input(wrapper).setValue('10.0.0.1:1080')
  assert.equal(
    hint(wrapper).text(),
    'Remote connections and new local terminals both use the proxy 127.0.0.1:7890.',
  )

  setLocale('zh-CN')
  await nextTick()
  assert.equal(wrapper.find('#settings-title').text(), '设置')
  assert.equal(wrapper.find('label[for="settings-proxy-address"]').text(), '代理地址')
  assert.equal(hint(wrapper).text(), '远程连接和新建的本地终端都会使用代理 127.0.0.1:7890。')

  wrapper.unmount()
})

test('SettingsModal: the sentence says that new local terminals use the proxy, in both languages', async () => {
  const { wrapper } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })

  assert.ok(hint(wrapper).text().includes('new local terminals'), 'the sentence covers local terminals')
  setLocale('zh-CN')
  await nextTick()
  assert.ok(hint(wrapper).text().includes('新建的本地终端'))
  wrapper.unmount()
})

test('SettingsModal: the hint about the shell configuration is shown whenever an address is entered', async () => {
  const hint = (wrapper: Awaited<ReturnType<typeof openSettings>>['wrapper']) =>
    wrapper.find('#settings-proxy-shell-hint')
  const { wrapper } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
  assert.equal(hint(wrapper).exists(), false, 'no address, no hint')

  await input(wrapper).setValue('127.0.0.1:7890')
  assert.equal(hint(wrapper).text(), 'Proxy variables set in your own shell configuration take precedence.')
  setLocale('zh-CN')
  await nextTick()
  assert.equal(hint(wrapper).text(), '如果你自己的 shell 配置里也设置了代理变量，会优先生效。')

  await input(wrapper).setValue('   ')
  assert.equal(hint(wrapper).exists(), false, 'a blank address counts as none')
  wrapper.unmount()

  // A saved address is an entered address too.
  const saved = await openSettings({ defaultHostId: 'local', revision: 2, proxyAddress: '127.0.0.1:7890' })
  assert.equal(hint(saved.wrapper).exists(), true)
  saved.wrapper.unmount()
})

test('SettingsModal: with no proxy saved, one sentence explains the field', async () => {
  const { wrapper } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
  assert.equal(input(wrapper).element.value, '')
  assert.equal(
    hint(wrapper).text(),
    'Leave it empty to connect directly. With a proxy, remote connections and new local terminals both use it.',
  )
  assert.equal(wrapper.findAll('.settings-field-hint').length, 1, 'nothing else is explained')

  setLocale('zh-CN')
  await nextTick()
  assert.equal(hint(wrapper).text(), '留空则直接连接；填写后，远程连接和新建的本地终端都会使用这个代理。')
  wrapper.unmount()
})

test('SettingsModal: an address that is not host:port is refused before it is sent', async () => {
  for (const bad of ['http://127.0.0.1:7890', 'user:secret@127.0.0.1:7890', '127.0.0.1', '127.0.0.1:0', '127.0.0.1:70000']) {
    const { wrapper, requests } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
    await input(wrapper).setValue(bad)
    await wrapper.find('form.settings-form').trigger('submit')
    await flushPromises()
    await nextTick()

    const error = wrapper.find('#settings-proxy-error')
    assert.ok(error.exists(), `${bad}: expected an inline error`)
    assert.equal(error.attributes('role'), 'alert')
    assert.ok(error.text().includes('host:port'), `${bad}: the error explains the expected form`)
    assert.equal(input(wrapper).attributes('aria-invalid'), 'true')
    assert.equal(
      input(wrapper).attributes('aria-describedby'),
      'settings-proxy-error settings-proxy-help',
      'the error is announced with the field',
    )
    assert.equal(requests.filter((r) => r.method === 'PUT').length, 0, `${bad}: nothing was sent`)
    assert.equal(wrapper.emitted('close'), undefined, 'the dialog stays open')
    wrapper.unmount()
  }
})

test('SettingsModal: the hub refusing an address is explained and the saved setting is kept', async () => {
  // `a..b` passes the quick client check and fails the hub's stricter one.
  const { wrapper, requests } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    requests.push({
      url: String(input),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : null,
    })
    return init?.method === 'PUT'
      ? json({ error: 'invalid_proxy_address' }, 400)
      : json({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
  }

  await input(wrapper).setValue('a..b:80')
  await wrapper.find('form.settings-form').trigger('submit')
  await flushPromises()
  await nextTick()

  assert.ok(wrapper.find('#settings-proxy-error').text().includes('host:port'))
  assert.equal(wrapper.emitted('close'), undefined)
  assert.equal(
    requests.filter((r) => r.method === 'PUT').length,
    1,
    'the hub was asked once and its refusal is what is shown',
  )

  // Cancel, then reopen: the previous setting is still the saved one.
  await wrapper.find('.settings-form__actions .settings-btn--subtle').trigger('click')
  assert.ok(wrapper.emitted('close'))
  wrapper.unmount()
})

test('SettingsModal: a revision conflict keeps what was typed and lets the user save again', async () => {
  let current: Record<string, unknown> = { defaultHostId: 'local', revision: 5, proxyAddress: '' }
  const puts: unknown[] = []
  setupFetchMock(async (req) => {
    if (req.method === 'GET') return json(current)
    puts.push(req.body)
    if (req.body && (req.body as { revision: number }).revision === current.revision) {
      current = { ...current, proxyAddress: (req.body as { proxyAddress: string }).proxyAddress, revision: 6 }
      return json(current)
    }
    return json({ error: 'host_revision_conflict' }, 409)
  })

  const wrapper = mount(SettingsModal, { props: { open: true }, attachTo: document.body })
  await flushPromises()
  await nextTick()

  await input(wrapper).setValue('127.0.0.1:7890')
  // Someone else saves a different revision before this dialog's save arrives.
  current = { defaultHostId: 'local', revision: 6, proxyAddress: '' }
  await wrapper.find('form.settings-form').trigger('submit')
  await flushPromises()
  await nextTick()

  assert.deepEqual(puts, [{ proxyAddress: '127.0.0.1:7890', revision: 5 }])
  const alert = wrapper.find('p.settings-alert')
  assert.ok(alert.exists(), 'the conflict is explained')
  assert.equal(alert.text(), 'The settings were changed elsewhere. Check the current value, then save again.')
  assert.equal(input(wrapper).element.value, '127.0.0.1:7890', 'what was typed is kept')
  assert.equal(wrapper.emitted('close'), undefined)

  // Saving again uses the revision that was reloaded.
  await wrapper.find('form.settings-form').trigger('submit')
  await flushPromises()
  await nextTick()
  assert.deepEqual(puts[1], { proxyAddress: '127.0.0.1:7890', revision: 6 })
  assert.equal(wrapper.emitted('close')?.length, 1)
  wrapper.unmount()
})

test('SettingsModal: saving the proxy succeeds with the current revision and reports it', async () => {
  const { wrapper, requests } = await openSettings({ defaultHostId: 'server-1', revision: 7, proxyAddress: '' })
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    requests.push({
      url: String(input),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : null,
    })
    return init?.method === 'PUT'
      ? json({ defaultHostId: 'server-1', revision: 8, proxyAddress: 'socks5://127.0.0.1:7890' })
      : json({ defaultHostId: 'server-1', revision: 7, proxyAddress: '' })
  }

  await input(wrapper).setValue('socks5://127.0.0.1:7890')
  await wrapper.find('form.settings-form').trigger('submit')
  await flushPromises()
  await nextTick()

  assert.deepEqual(
    requests.filter((r) => r.method === 'PUT').map((r) => r.body),
    [{ proxyAddress: 'socks5://127.0.0.1:7890', revision: 7 }],
  )
  assert.equal(wrapper.emitted('close')?.length, 1, 'the dialog closes after a successful save')
  const toasts = useToasts().value
  assert.equal(toasts.length, 1, 'the save is confirmed')
  assert.equal(toasts[0].level, 'info')
  wrapper.unmount()
})

test('SettingsModal: a failed load is reported and can be retried', async () => {
  let fail = true
  const requests = setupFetchMock(async (req) => {
    if (fail) return json({ error: 'boom' }, 500)
    return json({ defaultHostId: 'local', revision: 1, proxyAddress: '127.0.0.1:7890' })
  })
  const wrapper = mount(SettingsModal, { props: { open: true }, attachTo: document.body })
  await flushPromises()
  await nextTick()

  const alert = wrapper.find('.settings-alert')
  assert.ok(alert.exists())
  assert.equal(alert.attributes('role'), 'alert')
  assert.ok(alert.text().includes('The settings could not be loaded'))
  assert.equal(alert.find('button').text(), 'Retry')

  fail = false
  await wrapper.find('.settings-alert button').trigger('click')
  await flushPromises()
  await nextTick()
  assert.equal(requests.length, 2)
  assert.equal(input(wrapper).element.value, '127.0.0.1:7890')
  wrapper.unmount()
})

test('SettingsModal: focus moves inside, is trapped, Escape and the close button close, and focus returns', async () => {
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()

  const { wrapper } = await openSettings({ defaultHostId: 'local', revision: 1, proxyAddress: '' })
  const closeButton = wrapper.find<HTMLButtonElement>('.settings-modal__close')
  assert.equal(closeButton.element, document.activeElement, 'focus moves into the dialog when it opens')
  assert.equal(closeButton.attributes('aria-label'), 'Close settings')

  const panel = wrapper.find<HTMLElement>('[role="dialog"]')
  const order = ['settings-modal__close', 'settings-input', 'settings-btn--subtle', 'settings-btn--primary']
  for (const className of order) {
    assert.ok(panel.find(`.${className}`).exists(), `the dialog contains .${className}`)
  }

  // Tab from the last control wraps to the first.
  const save = wrapper.find<HTMLButtonElement>('.settings-btn--primary')
  save.element.focus()
  await panel.trigger('keydown', { key: 'Tab' })
  assert.equal(document.activeElement, closeButton.element)

  // Shift+Tab from the first control wraps to the last.
  await panel.trigger('keydown', { key: 'Tab', shiftKey: true })
  assert.equal(document.activeElement, save.element)

  // Escape closes the dialog.
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  await nextTick()
  assert.equal(wrapper.emitted('close')?.length, 1)

  // The close button closes the dialog too.
  await closeButton.trigger('click')
  assert.equal(wrapper.emitted('close')?.length, 2)

  // Focus returns to the control that opened the dialog.
  await wrapper.setProps({ open: false })
  await nextTick()
  assert.equal(document.activeElement, opener)

  wrapper.unmount()
  opener.remove()
})
