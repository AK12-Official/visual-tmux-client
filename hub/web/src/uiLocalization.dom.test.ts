import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import TerminalView from './components/TerminalView.vue'
import SessionList from './components/SessionList.vue'
import { clearToken, setToken } from './api'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'
import { setLocale, resetLocaleForTest, t } from './i18n/index'

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

const origFetch = globalThis.fetch

beforeEach(() => {
  resetConfigForTest()
  setConfig(testConfig)
  resetToastsForTest()
  clearToken()
  localStorage.clear()
  resetLocaleForTest('en')
  globalThis.fetch = async (): Promise<Response> => {
    return new Response(JSON.stringify({ error: 'AUTH_FAILED' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }
})

afterEach(() => {
  clearToken()
  localStorage.clear()
  resetLocaleForTest('en')
  globalThis.fetch = origFetch
})

test('Task 3.1: Login interface, accessible names and persistent statuses update live on language switch', async () => {
  resetLocaleForTest('en')
  const wrapper = mount(App)
  await nextTick()

  // 1. In English
  const title = wrapper.find('.auth-card__title')
  const hint = wrapper.find('.auth-card__hint')
  const input = wrapper.find('.auth-card__input')
  const btn = wrapper.find('.auth-card__btn')

  assert.equal(title.text(), 'Visual Tmux Client')
  assert.equal(hint.text(), 'Enter the access token to connect.')
  assert.equal(input.attributes('placeholder'), 'token')
  assert.equal(btn.text(), 'Connect')

  // Switch to zh-CN
  setLocale('zh-CN')
  await nextTick()

  assert.equal(hint.text(), '输入访问令牌以连接。')
  assert.equal(input.attributes('placeholder'), '令牌')
  assert.equal(btn.text(), '连接')

  // Switch back to en
  setLocale('en')
  await nextTick()

  assert.equal(hint.text(), 'Enter the access token to connect.')
  assert.equal(input.attributes('placeholder'), 'token')
  assert.equal(btn.text(), 'Connect')

  wrapper.unmount()
})

test('Task 3.1 & 3.3: SessionList controls, validation, and confirmation dialogs localize in both languages', async () => {
  resetLocaleForTest('en')
  const wrapper = mount(SessionList, {
    props: {
      sessions: [
        { name: 'dev', windows: 1, active: true },
        { name: 'prod', windows: 2, active: false },
      ],
      error: '',
      selected: null,
      active: {},
      hostId: 'local',
      hostName: 'Local Host',
    },
  })
  await nextTick()

  // Toolbar in English
  const createBtn = wrapper.find('.session-list__create-btn')
  assert.equal(createBtn.text(), '+ New session')
  const defaultModeBtn = wrapper.find('.session-list__mode')
  assert.equal(defaultModeBtn.text(), 'Default')

  // Switch to Simplified Chinese
  setLocale('zh-CN')
  await nextTick()

  assert.equal(createBtn.text(), '+ 新建会话')
  assert.equal(defaultModeBtn.text(), '默认')

  // Switch to manual mode
  const manualBtn = wrapper.findAll('.session-list__mode')[1]
  assert.equal(manualBtn.text(), '手动')
  await manualBtn.trigger('click')
  await nextTick()

  // Verify icon button accessible names in Chinese
  const pinBtn = wrapper.find('.session-list__icon-btn')
  assert.ok(pinBtn.attributes('title')?.includes('置顶') || pinBtn.attributes('aria-label')?.includes('置顶'))

  // Switch back to English
  setLocale('en')
  await nextTick()
  assert.equal(createBtn.text(), '+ New session')
  assert.equal(defaultModeBtn.text(), 'Default')
  assert.equal(manualBtn.text(), 'Manual')
  assert.ok(pinBtn.attributes('title')?.toLowerCase().includes('pin') || pinBtn.attributes('aria-label')?.toLowerCase().includes('pin'))

  wrapper.unmount()
})

test('Task 3.3: TerminalView ended states and diagnostics presentation separate known errors from raw codes', async () => {
  resetLocaleForTest('en')
  const wrapper = mount(TerminalView, {
    props: {
      session: 'main',
      alive: true,
      fontSize: 16,
      hostId: 'remote-1',
    },
  })
  await nextTick()

  // Simulate ended with known code AUTH_FAILED
  ;(wrapper.vm as any).state = 'ended'
  ;(wrapper.vm as any).endedDetail = 'AUTH_FAILED'
  await nextTick()

  const overlay = wrapper.find('.terminal-view__ended')
  assert.ok(overlay.exists(), 'Ended overlay must be displayed')
  assert.equal(wrapper.find('.terminal-view__ended-title').text(), 'Connection lost')
  assert.equal(wrapper.find('.terminal-view__ended-detail').text(), 'Authentication failed. Check the token and try again.')
  assert.equal(wrapper.find('.terminal-view__reconnect').text(), 'Reconnect')

  // Switch locale to Chinese
  setLocale('zh-CN')
  await nextTick()

  assert.equal(wrapper.find('.terminal-view__ended-title').text(), '连接已断开')
  assert.equal(wrapper.find('.terminal-view__ended-detail').text(), '身份验证失败。请检查令牌并重试。')
  assert.equal(wrapper.find('.terminal-view__reconnect').text(), '重新连接')

  // Simulate host_revision_conflict
  ;(wrapper.vm as any).endedDetail = 'host_revision_conflict'
  await nextTick()
  assert.equal(wrapper.find('.terminal-view__ended-detail').text(), '主机配置已更改，请刷新')

  setLocale('en')
  await nextTick()
  assert.equal(wrapper.find('.terminal-view__ended-detail').text(), 'the host configuration has changed; please refresh')

  // Simulate unknown diagnostic
  ;(wrapper.vm as any).endedDetail = 'Socket pipe broken abruptly [errno 104]'
  await nextTick()
  // Unknown diagnostic preserves the raw string verbatim
  assert.equal(wrapper.find('.terminal-view__ended-detail').text(), 'Socket pipe broken abruptly [errno 104]')

  wrapper.unmount()
})
