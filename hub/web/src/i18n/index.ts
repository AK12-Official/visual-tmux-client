import { ref } from 'vue'
import { createI18n } from 'vue-i18n'
import type { SupportedLocale, LanguagePreference } from './types'
import { resolveBrowserLocale } from './matching'
import { loadStoredPreference, saveStoredPreference } from './storage'
import { en } from './locales/en'
import { zhCN } from './locales/zh-CN'

export * from './types'
export * from './matching'
export * from './storage'

let currentPreference: LanguagePreference = loadStoredPreference()

export function resolveActiveLocale(pref: LanguagePreference = currentPreference): SupportedLocale {
  if (pref === 'zh-CN' || pref === 'en') {
    return pref
  }
  return resolveBrowserLocale()
}

const initialLocale = resolveActiveLocale(currentPreference)

export const i18n = createI18n({
  legacy: false,
  locale: initialLocale,
  fallbackLocale: 'en',
  missingWarn: false,
  fallbackWarn: false,
  messages: {
    en: en as any,
    'zh-CN': zhCN as any,
  },
})

/**
 * Narrow translation function for non-component and bootstrap code.
 */
export function t(key: string, named?: Record<string, unknown>): string {
  try {
    const fn = (i18n.global as any).t
    return fn(key, named ?? {})
  } catch {
    return key
  }
}

/**
 * Updates document.documentElement.lang if document is available.
 */
export function updateDocumentLanguage(locale: SupportedLocale): void {
  if (typeof document !== 'undefined' && document.documentElement) {
    document.documentElement.lang = locale
  }
}

// Initial document sync
updateDocumentLanguage(initialLocale)

export function getPreference(): LanguagePreference {
  return currentPreference
}

export function getLocale(): SupportedLocale {
  return (i18n.global.locale as { value: SupportedLocale }).value
}

type LocaleChangeListener = (locale: SupportedLocale, pref: LanguagePreference) => void
const changeListeners = new Set<LocaleChangeListener>()

export function onLocaleChange(listener: LocaleChangeListener): () => void {
  changeListeners.add(listener)
  return () => {
    changeListeners.delete(listener)
  }
}

export function setLocale(pref: LanguagePreference): SupportedLocale {
  currentPreference = pref
  saveStoredPreference(pref)
  const effective = resolveActiveLocale(pref)
  ;(i18n.global.locale as { value: SupportedLocale }).value = effective
  updateDocumentLanguage(effective)
  for (const listener of changeListeners) {
    try {
      listener(effective, pref)
    } catch {
      // ignore listener error
    }
  }
  return effective
}

/**
 * Sets up window 'languagechange' event listener to update active locale when preference is 'auto'.
 */
export function setupLanguageChangeListener(): () => void {
  if (typeof window === 'undefined') {
    return () => {}
  }

  const handler = () => {
    if (currentPreference === 'auto') {
      const effective = resolveBrowserLocale()
      ;(i18n.global.locale as { value: SupportedLocale }).value = effective
      updateDocumentLanguage(effective)
      for (const listener of changeListeners) {
        try {
          listener(effective, 'auto')
        } catch {
          // ignore listener error
        }
      }
    }
  }

  window.addEventListener('languagechange', handler)
  return () => {
    window.removeEventListener('languagechange', handler)
  }
}

/**
 * Test helper to reset locale state between test runs.
 */
export function resetLocaleForTest(pref: LanguagePreference = 'auto', languages?: readonly string[]): void {
  currentPreference = pref
  const effective = pref === 'auto' ? resolveBrowserLocale(languages) : pref
  ;(i18n.global.locale as { value: SupportedLocale }).value = effective
  updateDocumentLanguage(effective)
}
