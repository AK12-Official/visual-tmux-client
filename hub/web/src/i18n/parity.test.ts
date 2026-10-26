import test from 'node:test'
import assert from 'node:assert/strict'
import { createI18n } from 'vue-i18n'
import { en } from './locales/en'
import { zhCN } from './locales/zh-CN'

function extractParams(msg: string): string[] {
  const params = new Set<string>()
  const matches = msg.matchAll(/\{([a-zA-Z0-9_]+)\}/g)
  for (const m of matches) {
    params.add(m[1])
  }
  return [...params].sort()
}

test('en and zh-CN catalogs have identical namespaces', () => {
  const enNamespaces = Object.keys(en).sort()
  const zhNamespaces = Object.keys(zhCN).sort()
  assert.deepEqual(zhNamespaces, enNamespaces, 'Namespaces must match between en and zh-CN')
})

test('en and zh-CN catalogs have 100% key and parameter parity', () => {
  const requiredNamespaces = ['common', 'auth', 'hosts', 'sessions', 'terminal', 'files', 'help', 'errors']
  const namespaces = Object.keys(en) as (keyof typeof en)[]

  for (const ns of requiredNamespaces) {
    assert.ok(namespaces.includes(ns as any), `Required namespace "${ns}" is missing`)
  }

  for (const ns of namespaces) {
    const enMap = en[ns]
    const zhMap = zhCN[ns]

    const enKeys = Object.keys(enMap).sort()
    const zhKeys = Object.keys(zhMap).sort()

    assert.deepEqual(
      zhKeys,
      enKeys,
      `Keys in namespace "${ns}" must match exactly between en and zh-CN`,
    )

    for (const key of enKeys) {
      const enText = enMap[key]
      const zhText = zhMap[key]

      assert.ok(enText.length > 0, `en.${ns}.${key} must not be empty`)
      assert.ok(zhText.length > 0, `zh-CN.${ns}.${key} must not be empty`)

      const enParams = extractParams(enText)
      const zhParams = extractParams(zhText)

      assert.deepEqual(
        zhParams,
        enParams,
        `Interpolation parameters for "${ns}.${key}" must match: en has ${JSON.stringify(enParams)}, zh-CN has ${JSON.stringify(zhParams)}`,
      )
    }
  }
})

test('missing translations fall back to English', () => {
  const testI18n = createI18n({
    legacy: false,
    locale: 'zh-CN',
    fallbackLocale: 'en',
    missingWarn: false,
    fallbackWarn: false,
    messages: {
      en: {
        test: {
          presentInBoth: 'English Present',
          onlyInEnglish: 'English Fallback Value',
        },
      },
      'zh-CN': {
        test: {
          presentInBoth: '中文 Present',
        },
      },
    },
  })

  const tFn = (testI18n.global as any).t
  assert.equal(tFn('test.presentInBoth'), '中文 Present')
  assert.equal(tFn('test.onlyInEnglish'), 'English Fallback Value')
})
