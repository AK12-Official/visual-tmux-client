import test from 'node:test'
import assert from 'node:assert/strict'
import {
  applyOrder,
  loadOrder,
  saveOrder,
  type OrderState,
} from './ordering'

function setupMockStorage() {
  const store = new Map<string, string>()
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => store.set(key, val),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    length: store.size,
  } as Storage
}

test('loadOrder returns default state when storage is empty or invalid', () => {
  setupMockStorage()
  localStorage.clear()

  const def = loadOrder()
  assert.equal(def.mode, 'default')
  assert.deepEqual(def.order, [])
  assert.deepEqual(def.pinned, [])

  localStorage.setItem('vtc:order', 'invalid json {{{')
  assert.deepEqual(loadOrder(), { mode: 'default', order: [], pinned: [] })

  localStorage.setItem('vtc:order', JSON.stringify({ mode: 'unknown', order: 'bad' }))
  assert.deepEqual(loadOrder(), { mode: 'default', order: [], pinned: [] })
})

test('saveOrder and loadOrder persist and sanitize order state', () => {
  setupMockStorage()
  localStorage.clear()

  const state: OrderState = {
    mode: 'manual',
    order: ['sess-1', 'sess-2', 'sess-1'], // contains duplicate
    pinned: ['sess-2', ''],
  }
  saveOrder(state)

  const loaded = loadOrder()
  assert.equal(loaded.mode, 'manual')
  assert.deepEqual(loaded.order, ['sess-1', 'sess-2']) // duplicates deduplicated
  assert.deepEqual(loaded.pinned, ['sess-2']) // empty string filtered out
})

test('applyOrder returns unmodified list in default mode', () => {
  const sessions = [{ name: 'b' }, { name: 'a' }, { name: 'c' }]
  const state: OrderState = {
    mode: 'default',
    order: ['a', 'b', 'c'],
    pinned: ['c'],
  }

  const result = applyOrder(sessions, state)
  assert.deepEqual(result, sessions)
})

test('applyOrder in manual mode orders by pinned, specified order, and appends unseen', () => {
  const sessions = [
    { name: 'unseen-1' },
    { name: 'alpha' },
    { name: 'beta' },
    { name: 'pinned-1' },
    { name: 'unseen-2' },
    { name: 'pinned-2' },
  ]
  const state: OrderState = {
    mode: 'manual',
    order: ['pinned-2', 'pinned-1', 'beta', 'alpha'],
    pinned: ['pinned-1', 'pinned-2'],
  }

  const result = applyOrder(sessions, state)
  // Pinned items come first in their order relative to state.order:
  // state.order has pinned-2 then pinned-1
  // Unpinned known items come next: beta, alpha
  // Unseen items append at the end preserving their original relative order: unseen-1, unseen-2
  assert.deepEqual(
    result.map((s) => s.name),
    ['pinned-2', 'pinned-1', 'beta', 'alpha', 'unseen-1', 'unseen-2'],
  )
})
