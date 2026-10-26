import test from 'node:test'
import assert from 'node:assert/strict'
import { loadStoredPreference, saveStoredPreference, LOCALE_STORAGE_KEY } from './storage'

class MockStorage implements Storage {
  private map = new Map<string, string>()

  get length(): number {
    return this.map.size
  }

  clear(): void {
    this.map.clear()
  }

  getItem(key: string): string | null {
    return this.map.get(key) ?? null
  }

  key(index: number): string | null {
    return [...this.map.keys()][index] ?? null
  }

  removeItem(key: string): void {
    this.map.delete(key)
  }

  setItem(key: string, value: string): void {
    this.map.set(key, value)
  }
}

test('storage persistence: saves and restores preferences', () => {
  const storage = new MockStorage()

  // Default is auto when empty
  assert.equal(loadStoredPreference(storage), 'auto')

  // Save zh-CN
  assert.equal(saveStoredPreference('zh-CN', storage), true)
  assert.equal(storage.getItem(LOCALE_STORAGE_KEY), 'zh-CN')
  assert.equal(loadStoredPreference(storage), 'zh-CN')

  // Save en
  assert.equal(saveStoredPreference('en', storage), true)
  assert.equal(storage.getItem(LOCALE_STORAGE_KEY), 'en')
  assert.equal(loadStoredPreference(storage), 'en')

  // Save auto
  assert.equal(saveStoredPreference('auto', storage), true)
  assert.equal(storage.getItem(LOCALE_STORAGE_KEY), 'auto')
  assert.equal(loadStoredPreference(storage), 'auto')
})

test('invalid stored preferences behave as auto', () => {
  const storage = new MockStorage()

  storage.setItem(LOCALE_STORAGE_KEY, 'invalid-lang')
  assert.equal(loadStoredPreference(storage), 'auto')

  storage.setItem(LOCALE_STORAGE_KEY, 'zh-TW')
  assert.equal(loadStoredPreference(storage), 'auto')

  storage.setItem(LOCALE_STORAGE_KEY, '{"foo":"bar"}')
  assert.equal(loadStoredPreference(storage), 'auto')
})

test('handles storage access failures gracefully', () => {
  const failingStorage: Storage = {
    length: 0,
    clear() {
      throw new Error('Access denied')
    },
    getItem() {
      throw new Error('SecurityError: The operation is insecure.')
    },
    key() {
      throw new Error('Access denied')
    },
    removeItem() {
      throw new Error('Access denied')
    },
    setItem() {
      throw new Error('QuotaExceededError')
    },
  }

  // Reading from failing storage falls back to auto without throwing
  assert.equal(loadStoredPreference(failingStorage), 'auto')

  // Writing to failing storage returns false without throwing
  assert.equal(saveStoredPreference('zh-CN', failingStorage), false)
})
