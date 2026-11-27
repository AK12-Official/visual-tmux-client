import test from 'node:test'
import assert from 'node:assert/strict'

import { PROXY_CODES, isProxyFailure, parseProxyFailure, proxyFailureText } from './hostErrors'
import { en } from './i18n/locales/en'
import { zhCN } from './i18n/locales/zh-CN'
import { resetLocaleForTest, setLocale } from './i18n/index'

test('parseProxyFailure reads the code and the proxy address out of a hub message', () => {
  assert.deepEqual(parseProxyFailure('proxy_unreachable: proxy 127.0.0.1:7890'), {
    code: 'proxy_unreachable',
    proxy: '127.0.0.1:7890',
  })
  assert.deepEqual(parseProxyFailure('proxy_target_unreachable: proxy [::1]:1080'), {
    code: 'proxy_target_unreachable',
    proxy: '[::1]:1080',
  })
  // A host status carries the code alone.
  assert.deepEqual(parseProxyFailure('proxy_refused'), { code: 'proxy_refused', proxy: '' })

  assert.equal(parseProxyFailure('host_unavailable'), null)
  assert.equal(parseProxyFailure('host_unavailable: ssh authentication failed'), null)
  assert.equal(parseProxyFailure(''), null)
})

test('isProxyFailure tells a proxy problem from a host problem', () => {
  assert.equal(isProxyFailure('proxy_auth_required: proxy 10.0.0.1:1080'), true)
  assert.equal(isProxyFailure('host_unavailable'), false)
  assert.equal(isProxyFailure('proxy'), false)
})

test('proxyFailureText names the proxy and says who failed, in each language', () => {
  resetLocaleForTest('en')
  const unreachable = proxyFailureText('proxy_unreachable: proxy 127.0.0.1:7890')
  assert.ok(unreachable)
  assert.ok(unreachable.includes('127.0.0.1:7890'), 'the message names the proxy')
  assert.ok(/proxy/i.test(unreachable), 'the message says the proxy is the problem')

  setLocale('zh-CN')
  const target = proxyFailureText('proxy_target_unreachable: proxy 127.0.0.1:7890')
  assert.ok(target)
  assert.ok(target.includes('127.0.0.1:7890'))
  assert.ok(target.includes('主机'), 'the message says the host did not answer')
  assert.equal(target, proxyFailureText('proxy_target_unreachable', '127.0.0.1:7890'))

  assert.equal(proxyFailureText('host_unavailable'), null)
})

test('each proxy code has its own message in both languages', () => {
  for (const [locale, catalog, other] of [
    ['en', en, 'the host is unreachable or disconnected'],
    ['zh-CN', zhCN, '主机不可达或已断开连接'],
  ] as const) {
    resetLocaleForTest(locale)
    const texts = new Set<string>()
    for (const code of PROXY_CODES) {
      const text = proxyFailureText(`${code}: proxy 127.0.0.1:7890`)
      assert.ok(text, `${locale} has no message for ${code}`)
      assert.ok(text.includes('127.0.0.1:7890'), `${locale} message for ${code} does not name the proxy`)
      assert.ok(!text.includes('{'), `${locale} message for ${code} leaves a placeholder unfilled`)
      assert.notEqual(text, other, `${code} must not read as a direct host failure`)
      texts.add(text)
    }
    assert.equal(texts.size, PROXY_CODES.length, `${locale} gives the same words to two different failures`)
  }
})
