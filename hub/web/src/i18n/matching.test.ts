import test from 'node:test'
import assert from 'node:assert/strict'
import { matchLanguageTag, resolveBrowserLocale } from './matching'

test('English variants match en', () => {
  assert.equal(matchLanguageTag('en'), 'en')
  assert.equal(matchLanguageTag('en-US'), 'en')
  assert.equal(matchLanguageTag('en-GB'), 'en')
  assert.equal(matchLanguageTag('en-CA'), 'en')
  assert.equal(matchLanguageTag('en-AU'), 'en')
  assert.equal(matchLanguageTag('EN-us'), 'en')
  assert.equal(matchLanguageTag('en_US'), 'en')
})

test('Chinese Hans and regional tags match zh-CN', () => {
  // Bare zh
  assert.equal(matchLanguageTag('zh'), 'zh-CN')
  assert.equal(matchLanguageTag('ZH'), 'zh-CN')

  // Explicit Hans script
  assert.equal(matchLanguageTag('zh-Hans'), 'zh-CN')
  assert.equal(matchLanguageTag('zh-Hans-CN'), 'zh-CN')
  assert.equal(matchLanguageTag('zh-Hans-SG'), 'zh-CN')
  assert.equal(matchLanguageTag('zh-Hans-TW'), 'zh-CN')
  assert.equal(matchLanguageTag('zh-Hans-HK'), 'zh-CN')

  // CN or SG region without explicit Hant
  assert.equal(matchLanguageTag('zh-CN'), 'zh-CN')
  assert.equal(matchLanguageTag('zh-SG'), 'zh-CN')
  assert.equal(matchLanguageTag('zh_CN'), 'zh-CN')
})

test('Traditional Chinese, Hant, and TW/HK/MO variants without Hans remain unmatched', () => {
  // Explicit Hant
  assert.equal(matchLanguageTag('zh-Hant'), null)
  assert.equal(matchLanguageTag('zh-Hant-TW'), null)
  assert.equal(matchLanguageTag('zh-Hant-HK'), null)
  assert.equal(matchLanguageTag('zh-Hant-MO'), null)
  assert.equal(matchLanguageTag('zh-Hant-CN'), null)

  // TW, HK, MO without Hans
  assert.equal(matchLanguageTag('zh-TW'), null)
  assert.equal(matchLanguageTag('zh-HK'), null)
  assert.equal(matchLanguageTag('zh-MO'), null)
})

test('invalid and unsupported language tags return null', () => {
  assert.equal(matchLanguageTag(''), null)
  assert.equal(matchLanguageTag('  '), null)
  assert.equal(matchLanguageTag('fr'), null)
  assert.equal(matchLanguageTag('fr-FR'), null)
  assert.equal(matchLanguageTag('ja'), null)
  assert.equal(matchLanguageTag('de-DE'), null)
  assert.equal(matchLanguageTag(null as any), null)
  assert.equal(matchLanguageTag(undefined as any), null)
})

test('resolveBrowserLocale respects order of preferences and skips unsupported', () => {
  // Scenario: browser prefers zh-SG followed by en -> zh-CN
  assert.equal(resolveBrowserLocale(['zh-SG', 'en']), 'zh-CN')

  // Scenario: unsupported Chinese variant zh-TW followed by en-US -> en
  assert.equal(resolveBrowserLocale(['zh-TW', 'en-US']), 'en')

  // Scenario: unsupported language followed by zh-CN -> zh-CN
  assert.equal(resolveBrowserLocale(['ja-JP', 'fr-FR', 'zh-Hans', 'en']), 'zh-CN')

  // First supported wins
  assert.equal(resolveBrowserLocale(['en-US', 'zh-CN']), 'en')
  assert.equal(resolveBrowserLocale(['zh-CN', 'en-US']), 'zh-CN')
})

test('resolveBrowserLocale falls back to en when empty or unmatched', () => {
  // Scenario: no matching preference or empty array -> en
  assert.equal(resolveBrowserLocale([]), 'en')
  assert.equal(resolveBrowserLocale(['fr-FR', 'de-DE', 'es-ES']), 'en')
  assert.equal(resolveBrowserLocale(['']), 'en')
})
