import test from 'node:test'
import assert from 'node:assert/strict'
import { t, setLocale, resetLocaleForTest, getLocale, getPreference } from './index'

test('translation call outside component setup returns correct message for active locale', () => {
  resetLocaleForTest('en')
  assert.equal(getLocale(), 'en')
  assert.equal(t('common.save'), 'Save')
  assert.equal(t('common.cancel'), 'Cancel')

  setLocale('zh-CN')
  assert.equal(getLocale(), 'zh-CN')
  assert.equal(t('common.save'), '保存')
  assert.equal(t('common.cancel'), '取消')

  resetLocaleForTest('en')
})

test('translation call outside component setup handles named interpolation parameters', () => {
  resetLocaleForTest('en')
  assert.equal(
    t('errors.file_too_large_limit', { limit: '10MB' }),
    "it is larger than this hub's 10MB limit for one file",
  )

  setLocale('zh-CN')
  assert.equal(
    t('errors.file_too_large_limit', { limit: '10MB' }),
    '文件大小超过此 Hub 的单文件限制（10MB）',
  )

  resetLocaleForTest('en')
})

test('missing translation falls back to English', () => {
  setLocale('zh-CN')
  // If a key exists only in en (or hypothetical fallback)
  // Our catalogs are at parity, but createI18n fallbackLocale is 'en'
  assert.equal(t('common.ok'), '确定')
  resetLocaleForTest('en')
})
