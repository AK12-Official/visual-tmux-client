// Safe wrappers for Web Storage API access.
// Browsers in private browsing modes, restricted security policies, or embedded
// contexts may throw SecurityError on property access (globalThis.localStorage)
// or method calls (getItem, setItem, removeItem).

export function getSafeStorage(storage?: Storage | null): Storage | null {
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

export function safeStorageGet(key: string, storage?: Storage | null): string | null {
  const s = getSafeStorage(storage)
  if (!s) return null
  try {
    return s.getItem(key)
  } catch {
    return null
  }
}

export function safeStorageSet(key: string, value: string, storage?: Storage | null): boolean {
  const s = getSafeStorage(storage)
  if (!s) return false
  try {
    s.setItem(key, value)
    return true
  } catch {
    return false
  }
}

export function safeStorageRemove(key: string, storage?: Storage | null): boolean {
  const s = getSafeStorage(storage)
  if (!s) return false
  try {
    s.removeItem(key)
    return true
  } catch {
    return false
  }
}
