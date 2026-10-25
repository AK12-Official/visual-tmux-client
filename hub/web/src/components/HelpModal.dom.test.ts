import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import HelpModal, { resetGuideCacheForTest } from './HelpModal.vue'
import { setLocale, resetLocaleForTest, t } from '../i18n/index'
import { resetToastsForTest, useToasts } from '../toasts'
import { setConfig, resetConfigForTest, type ClientConfig } from '../config'

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

const origFetch = globalThis.fetch

const mockEnMd = `# tmux User Guide\n\nEnglish guide content.`
const mockZhMd = `# tmux 使用指南\n\n中文指南内容。`

beforeEach(() => {
  resetConfigForTest()
  setConfig(testConfig)
  resetGuideCacheForTest()
  resetToastsForTest()
  resetLocaleForTest('en')
})

afterEach(() => {
  globalThis.fetch = origFetch
  resetConfigForTest()
  resetGuideCacheForTest()
  resetToastsForTest()
  resetLocaleForTest('en')
})

test('Task 4.1 & 4.2: Open help in English and Chinese with localized controls and same-origin content', async () => {
  const fetchedUrls: string[] = []
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    fetchedUrls.push(url)
    if (url.includes('tmux-guide.en.md')) {
      return new Response(mockEnMd, { status: 200 })
    }
    if (url.includes('tmux-guide.zh-CN.md')) {
      return new Response(mockZhMd, { status: 200 })
    }
    return new Response('Not found', { status: 404 })
  }

  // 1. English
  resetLocaleForTest('en')
  const wrapperEn = mount(HelpModal, { props: { open: true } })
  await nextTick()
  // Wait for async marked & fetch
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  const titleEn = wrapperEn.find('#help-title')
  const closeBtnEn = wrapperEn.find('.help__close')
  assert.equal(titleEn.text(), 'tmux User Guide')
  assert.equal(closeBtnEn.attributes('aria-label'), 'Close Help')
  assert.ok(wrapperEn.html().includes('English guide content.'))
  assert.ok(fetchedUrls.some((u) => u.includes('tmux-guide.en.md')))

  wrapperEn.unmount()

  // 2. Simplified Chinese
  resetLocaleForTest('zh-CN')
  const wrapperZh = mount(HelpModal, { props: { open: true } })
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  const titleZh = wrapperZh.find('#help-title')
  const closeBtnZh = wrapperZh.find('.help__close')
  assert.equal(titleZh.text(), 'tmux 使用指南')
  assert.equal(closeBtnZh.attributes('aria-label'), '关闭帮助')
  assert.ok(wrapperZh.html().includes('中文指南内容。'))
  assert.ok(fetchedUrls.some((u) => u.includes('tmux-guide.zh-CN.md')))

  wrapperZh.unmount()
})

test('Task 4.2: Switching language while open updates title, controls, and content seamlessly', async () => {
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.includes('tmux-guide.en.md')) {
      return new Response(mockEnMd, { status: 200 })
    }
    if (url.includes('tmux-guide.zh-CN.md')) {
      return new Response(mockZhMd, { status: 200 })
    }
    return new Response('Not found', { status: 404 })
  }

  resetLocaleForTest('en')
  const wrapper = mount(HelpModal, { props: { open: true } })
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(wrapper.find('#help-title').text(), 'tmux User Guide')
  assert.ok(wrapper.html().includes('English guide content.'))

  // Switch to Simplified Chinese while open
  setLocale('zh-CN')
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(wrapper.find('#help-title').text(), 'tmux 使用指南')
  assert.equal(wrapper.find('.help__close').attributes('aria-label'), '关闭帮助')
  assert.ok(wrapper.html().includes('中文指南内容。'))

  // Switch back to English
  setLocale('en')
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(wrapper.find('#help-title').text(), 'tmux User Guide')
  assert.equal(wrapper.find('.help__close').attributes('aria-label'), 'Close Help')
  assert.ok(wrapper.html().includes('English guide content.'))

  wrapper.unmount()
})

test('Task 4.2: Stale response protection when switching language during load', async () => {
  let resolveZh: (res: Response) => void
  let resolveEn: (res: Response) => void

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.includes('tmux-guide.zh-CN.md')) {
      return new Promise<Response>((resolve) => {
        resolveZh = resolve
      })
    }
    if (url.includes('tmux-guide.en.md')) {
      return new Promise<Response>((resolve) => {
        resolveEn = resolve
      })
    }
    return new Response('Not found', { status: 404 })
  }

  // Start with zh-CN
  resetLocaleForTest('zh-CN')
  const wrapper = mount(HelpModal, { props: { open: true } })
  await nextTick()

  // Chinese load is pending
  assert.ok(wrapper.find('.help__loading').exists())

  // Switch to English while Chinese load is still pending
  setLocale('en')
  await nextTick()

  // Now resolve the late Chinese response
  resolveZh!(new Response(mockZhMd, { status: 200 }))
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // English request is pending, so modal must NOT display late Chinese content
  assert.ok(!wrapper.html().includes('中文指南内容。'))

  // Now resolve English response
  resolveEn!(new Response(mockEnMd, { status: 200 }))
  await new Promise((r) => setTimeout(r, 20))
  await nextTick()

  // English content is displayed
  assert.ok(wrapper.html().includes('English guide content.'))
  assert.equal(wrapper.find('#help-title').text(), 'tmux User Guide')

  wrapper.unmount()
})

test('Task 4.2: Cache reuse prevents redundant network requests for previously loaded languages', async () => {
  let fetchCountEn = 0
  let fetchCountZh = 0

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.includes('tmux-guide.en.md')) {
      fetchCountEn++
      return new Response(mockEnMd, { status: 200 })
    }
    if (url.includes('tmux-guide.zh-CN.md')) {
      fetchCountZh++
      return new Response(mockZhMd, { status: 200 })
    }
    return new Response('Not found', { status: 404 })
  }

  resetLocaleForTest('en')
  const wrapper = mount(HelpModal, { props: { open: true } })
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(fetchCountEn, 1)
  assert.equal(fetchCountZh, 0)
  assert.ok(wrapper.html().includes('English guide content.'))

  // Close help
  await wrapper.setProps({ open: false })
  await nextTick()

  // Reopen in English
  await wrapper.setProps({ open: true })
  await nextTick()
  // Immediate cache hit
  assert.equal(fetchCountEn, 1, 'Reopening must reuse cached English HTML without re-fetching')
  assert.ok(wrapper.html().includes('English guide content.'))

  // Switch to Chinese while open
  setLocale('zh-CN')
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.equal(fetchCountZh, 1, 'Chinese fetched once')
  assert.ok(wrapper.html().includes('中文指南内容。'))

  // Switch back to English
  setLocale('en')
  await nextTick()
  assert.equal(fetchCountEn, 1, 'Switching back to English must use cache without fetching')
  assert.ok(wrapper.html().includes('English guide content.'))

  wrapper.unmount()
})

test('Task 4.2: Closing help during load completes safely and caches for next open', async () => {
  let resolveEn: (res: Response) => void
  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    return new Promise<Response>((resolve) => {
      resolveEn = resolve
    })
  }

  resetLocaleForTest('en')
  const wrapper = mount(HelpModal, { props: { open: true } })
  await nextTick()

  // Close while fetch is in flight
  await wrapper.setProps({ open: false })
  await nextTick()

  // Resolve fetch
  resolveEn!(new Response(mockEnMd, { status: 200 }))
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  // Reopen help
  await wrapper.setProps({ open: true })
  await nextTick()
  await nextTick()
  await nextTick()

  // Cached content should now be immediately available
  assert.ok(wrapper.html().includes('English guide content.'))
  wrapper.unmount()
})

test('Task 4.2: Recover from failed loading on reopening help', async () => {
  let shouldFail = true
  globalThis.fetch = async (): Promise<Response> => {
    if (shouldFail) {
      return new Response('Server Error', { status: 500, statusText: 'Internal Server Error' })
    }
    return new Response(mockEnMd, { status: 200 })
  }

  resetLocaleForTest('en')
  const wrapper = mount(HelpModal, { props: { open: true } })
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  // Should have triggered error notification and not cached
  const toasts = useToasts()
  assert.ok(toasts.value.some((t) => t.level === 'error'), 'Should notify error on fetch failure')

  // Close help
  await wrapper.setProps({ open: false })
  await nextTick()

  // Fix network
  shouldFail = false

  // Reopen help: should retry and succeed
  await wrapper.setProps({ open: true })
  await nextTick()
  await new Promise((r) => setTimeout(r, 30))
  await nextTick()

  assert.ok(wrapper.html().includes('English guide content.'))
  wrapper.unmount()
})
