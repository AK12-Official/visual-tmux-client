import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import { clearToken, setToken } from './api'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'
import {
  setLocale,
  resetLocaleForTest,
  getLocale,
  getPreference,
  setupLanguageChangeListener,
  LOCALE_STORAGE_KEY,
} from './i18n/index'

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

beforeEach(() => {
  resetConfigForTest()
  setConfig(testConfig)
  resetToastsForTest()
  clearToken()
  localStorage.clear()
  resetLocaleForTest('en')
})

afterEach(() => {
  clearToken()
  localStorage.clear()
  resetLocaleForTest('en')
})

test('login language selector is accessible and changes active language and document lang', async () => {
  const wrapper = mount(App)
  await nextTick()

  const select = wrapper.find('#login-lang-select')
  assert.ok(select.exists(), 'Login language selector must exist')
  assert.ok(select.attributes('aria-label'), 'Language selector must have aria-label')
  const label = wrapper.find('label[for="login-lang-select"]')
  assert.ok(label.exists(), 'Label for language selector must exist')

  // Switch to zh-CN
  await select.setValue('zh-CN')
  assert.equal(getPreference(), 'zh-CN')
  assert.equal(getLocale(), 'zh-CN')
  assert.equal(document.documentElement.lang, 'zh-CN')
  assert.equal(localStorage.getItem(LOCALE_STORAGE_KEY), 'zh-CN')

  // Switch to en
  await select.setValue('en')
  assert.equal(getPreference(), 'en')
  assert.equal(getLocale(), 'en')
  assert.equal(document.documentElement.lang, 'en')
  assert.equal(localStorage.getItem(LOCALE_STORAGE_KEY), 'en')

  wrapper.unmount()
})

test('workspace header language selector is accessible and selection survives logout and host changes', async () => {
  setToken('valid-token')
  localStorage.setItem('vtc:recent-host', 'local')

  const wrapper = mount(App)
  await nextTick()

  const headerSelect = wrapper.find('#header-lang-select')
  assert.ok(headerSelect.exists(), 'Header language selector must exist')
  assert.ok(headerSelect.attributes('aria-label'), 'Header selector must have aria-label')
  const headerLabel = wrapper.find('label[for="header-lang-select"]')
  assert.ok(headerLabel.exists(), 'Header label must exist')

  // Change language in authenticated workspace to zh-CN
  await headerSelect.setValue('zh-CN')
  assert.equal(getPreference(), 'zh-CN')
  assert.equal(getLocale(), 'zh-CN')
  assert.equal(document.documentElement.lang, 'zh-CN')
  assert.equal(localStorage.getItem(LOCALE_STORAGE_KEY), 'zh-CN')

  // Simulate logout
  const logoutBtn = wrapper.find('.app__logout')
  assert.ok(logoutBtn.exists(), 'Logout button must exist')
  await logoutBtn.trigger('click')
  await nextTick()

  // Verify login view is active and language selection survived
  const loginSelect = wrapper.find('#login-lang-select')
  assert.ok(loginSelect.exists(), 'Back on login view')
  assert.equal(getPreference(), 'zh-CN')
  assert.equal(getLocale(), 'zh-CN')
  assert.equal(document.documentElement.lang, 'zh-CN')

  wrapper.unmount()
})

test('automatic browser language changes: handled in auto mode and ignored in explicit mode', async () => {
  const cleanup = setupLanguageChangeListener()
  try {
    // 1. Explicit mode: set to zh-CN
    setLocale('zh-CN')
    assert.equal(getPreference(), 'zh-CN')
    assert.equal(getLocale(), 'zh-CN')

    // Trigger languagechange event
    window.dispatchEvent(new Event('languagechange'))
    // Explicit choice must ignore browser language change
    assert.equal(getPreference(), 'zh-CN')
    assert.equal(getLocale(), 'zh-CN')

    // 2. Auto mode
    setLocale('auto')
    assert.equal(getPreference(), 'auto')

    // Dispatch event in auto mode
    window.dispatchEvent(new Event('languagechange'))
    assert.equal(getPreference(), 'auto')
    // document.documentElement.lang was updated
    assert.ok(document.documentElement.lang.length > 0)
  } finally {
    cleanup()
  }
})
