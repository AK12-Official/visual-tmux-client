import test from 'node:test'
import assert from 'node:assert/strict'
import { renderLoading, renderError, resetBootstrapForTest, cleanupBootstrap } from './bootstrap'
import { setLocale, resetLocaleForTest, t } from './i18n/index'

test('renderLoading renders localized loading state', () => {
  const el = { innerHTML: '' } as unknown as HTMLElement

  resetLocaleForTest('en')
  renderLoading(el)
  assert.ok(el.innerHTML.includes('Loading configuration...'))

  setLocale('zh-CN')
  renderLoading(el)
  assert.ok(el.innerHTML.includes('正在加载配置...'))

  resetLocaleForTest('en')
})

test('renderError renders localized error heading and retry button in both languages with escaped diagnostics', () => {
  const mockBtn = { disabled: false, onclick: null as null | (() => void) }
  const el = {
    innerHTML: '',
    querySelector(sel: string) {
      if (sel === '#vtc-retry-btn') return mockBtn
      return null
    },
  } as unknown as HTMLElement

  const rawDiagnostic = '<script>alert("xss")</script> & "quotes" & \'single\''

  // English
  resetLocaleForTest('en')
  let retried = false
  renderError(el, rawDiagnostic, () => {
    retried = true
  })

  assert.ok(el.innerHTML.includes('Configuration Error'))
  assert.ok(el.innerHTML.includes('Retry'))
  assert.ok(el.innerHTML.includes('&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;'))
  assert.ok(el.innerHTML.includes('&amp; &quot;quotes&quot;'))
  assert.ok(!el.innerHTML.includes('<script>'))

  mockBtn.onclick?.()
  assert.equal(retried, true)
  assert.equal(mockBtn.disabled, true)

  // Simplified Chinese
  setLocale('zh-CN')
  let retriedZh = false
  mockBtn.disabled = false
  renderError(el, rawDiagnostic, () => {
    retriedZh = true
  })

  assert.ok(el.innerHTML.includes('配置错误'))
  assert.ok(el.innerHTML.includes('重试'))
  assert.ok(el.innerHTML.includes('&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;'))
  assert.ok(el.innerHTML.includes('&amp; &quot;quotes&quot;'))
  assert.ok(!el.innerHTML.includes('<script>'))

  mockBtn.onclick?.()
  assert.equal(retriedZh, true)
  assert.equal(mockBtn.disabled, true)

  resetLocaleForTest('en')
})

test('I18N-R4-02: pre-mount error and loading states dynamically update on locale change without manual re-render', () => {
  const buttons: { disabled: boolean; onclick: null | (() => void) }[] = []
  const el = {
    innerHTML: '',
    querySelector(sel: string) {
      if (sel === '#vtc-retry-btn') {
        const btn = { disabled: false, onclick: null as null | (() => void) }
        buttons.push(btn)
        return btn
      }
      return null
    },
  } as unknown as HTMLElement

  resetLocaleForTest('en')
  let retryCount = 0
  const onRetry = () => {
    retryCount++
  }

  // 1. Initial error rendered in English
  renderError(el, 'Backend unreachable: 503', onRetry)
  assert.ok(el.innerHTML.includes('Configuration Error'))
  assert.ok(el.innerHTML.includes('Retry'))
  assert.ok(el.innerHTML.includes('Backend unreachable: 503'))

  // 2. Locale changes to Chinese -> content should automatically update
  setLocale('zh-CN')
  assert.ok(el.innerHTML.includes('配置错误'), 'Heading should dynamically update to Chinese')
  assert.ok(el.innerHTML.includes('重试'), 'Retry button should dynamically update to Chinese')
  assert.ok(el.innerHTML.includes('Backend unreachable: 503'), 'Raw diagnostic must be preserved')

  // Verify retry button click works on dynamically re-rendered error
  const activeBtn = buttons[buttons.length - 1]
  assert.ok(activeBtn, 'Retry button must exist')
  activeBtn.onclick?.()
  assert.equal(retryCount, 1)
  assert.equal(activeBtn.disabled, true)

  // 3. Loading state also updates dynamically
  renderLoading(el)
  assert.ok(el.innerHTML.includes('正在加载配置...'))

  setLocale('en')
  assert.ok(el.innerHTML.includes('Loading configuration...'), 'Loading text should dynamically update to English')

  // 4. Cleanup removes listener
  resetBootstrapForTest()
  setLocale('zh-CN')
  // After resetBootstrapForTest(), innerHTML should not have been re-rendered to Chinese
  assert.ok(el.innerHTML.includes('Loading configuration...'), 'Listener must be detached after cleanup')

  resetLocaleForTest('en')
})

test('global diagnostic wrappers format messages in active language with diagnostics preserved', () => {
  resetLocaleForTest('en')
  const enJsMsg = t('errors.jsError', {
    message: 'Cannot read properties of undefined',
    filename: 'main.js',
    lineno: 42,
  })
  assert.equal(enJsMsg, 'JS Error: Cannot read properties of undefined (main.js:42)')

  const enRejection = t('errors.unhandledRejection', {
    reason: 'NetworkError: Failed to fetch',
  })
  assert.equal(enRejection, 'Unhandled rejection: NetworkError: Failed to fetch')

  setLocale('zh-CN')
  const zhJsMsg = t('errors.jsError', {
    message: 'Cannot read properties of undefined',
    filename: 'main.js',
    lineno: 42,
  })
  assert.equal(zhJsMsg, 'JS 错误: Cannot read properties of undefined (main.js:42)')

  const zhRejection = t('errors.unhandledRejection', {
    reason: 'NetworkError: Failed to fetch',
  })
  assert.equal(zhRejection, '未处理的 Promise 拒绝: NetworkError: Failed to fetch')

  resetLocaleForTest('en')
})
