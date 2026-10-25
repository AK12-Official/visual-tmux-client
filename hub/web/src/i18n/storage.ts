import type { LanguagePreference } from './types'

export const LOCALE_STORAGE_KEY = 'vtc:locale'

function getStorage(storage?: Storage | null): Storage | null {
  if (storage !== undefined) return storage
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage
    }
  } catch {
    // storage access blocked or throws (e.g. security policy, private mode)
  }
  return null
}

/**
 * Loads stored language preference.
 * Returns 'auto' if absent, invalid, or storage access throws.
 */
export function loadStoredPreference(storage?: Storage | null): LanguagePreference {
  const s = getStorage(storage)
  if (!s) return 'auto'
  try {
    const raw = s.getItem(LOCALE_STORAGE_KEY)
    if (raw === 'zh-CN' || raw === 'en' || raw === 'auto') {
      return raw
    }
    return 'auto'
  } catch {
    return 'auto'
  }
}

/**
 * Saves language preference to storage.
 * Handles storage exceptions gracefully and returns whether the write succeeded.
 */
export function saveStoredPreference(pref: LanguagePreference, storage?: Storage | null): boolean {
  const s = getStorage(storage)
  if (!s) return false
  try {
    s.setItem(LOCALE_STORAGE_KEY, pref)
    return true
  } catch {
    return false
  }
}
