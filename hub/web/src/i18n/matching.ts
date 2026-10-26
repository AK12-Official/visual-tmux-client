import type { SupportedLocale } from './types'

/**
 * Matches a BCP-47 language tag against supported locales.
 *
 * Rules:
 * - English variants match English ('en').
 * - Chinese tags with an explicit Hans script match Simplified Chinese ('zh-CN').
 * - Chinese tags with CN or SG region without an explicit Hant script match Simplified Chinese ('zh-CN').
 * - Bare 'zh' matches Simplified Chinese ('zh-CN').
 * - Explicit Hant and Chinese TW/HK/MO variants without an explicit Hans script remain unmatched.
 * - All other tags return null.
 */
export function matchLanguageTag(rawTag: string): SupportedLocale | null {
  if (!rawTag || typeof rawTag !== 'string') return null
  const tag = rawTag.trim().toLowerCase()
  if (!tag) return null

  const subtags = tag.split(/[-_]/)
  const primary = subtags[0]

  if (primary === 'en') {
    return 'en'
  }

  if (primary === 'zh') {
    const hasHans = subtags.includes('hans')
    const hasHant = subtags.includes('hant')
    const hasCnOrSg = subtags.includes('cn') || subtags.includes('sg')

    if (hasHans) {
      return 'zh-CN'
    }

    if (hasHant) {
      return null
    }

    if (hasCnOrSg) {
      return 'zh-CN'
    }

    if (subtags.length === 1) {
      return 'zh-CN'
    }

    return null
  }

  return null
}

/**
 * Pure resolver: evaluates browser language preferences in order and selects the first supported match.
 * Falls back to 'en' when empty or unmatched.
 */
export function resolveBrowserLocale(languages?: readonly string[]): SupportedLocale {
  const list = languages ?? (typeof navigator !== 'undefined' ? (navigator.languages || [navigator.language]) : [])
  if (list && Array.isArray(list)) {
    for (const tag of list) {
      if (typeof tag === 'string') {
        const match = matchLanguageTag(tag)
        if (match) {
          return match
        }
      }
    }
  }
  return 'en'
}
