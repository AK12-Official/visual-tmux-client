import type { LanguagePreference } from './types'
import { getSafeStorage, safeStorageGet, safeStorageSet } from '../storage'

export const LOCALE_STORAGE_KEY = 'vtc:locale'

/**
 * Loads stored language preference.
 * Returns 'auto' if absent, invalid, or storage access throws.
 */
export function loadStoredPreference(storage?: Storage | null): LanguagePreference {
  const raw = safeStorageGet(LOCALE_STORAGE_KEY, storage)
  if (raw === 'zh-CN' || raw === 'en' || raw === 'auto') {
    return raw
  }
  return 'auto'
}

/**
 * Saves language preference to storage.
 * Handles storage exceptions gracefully and returns whether the write succeeded.
 */
export function saveStoredPreference(pref: LanguagePreference, storage?: Storage | null): boolean {
  return safeStorageSet(LOCALE_STORAGE_KEY, pref, storage)
}
