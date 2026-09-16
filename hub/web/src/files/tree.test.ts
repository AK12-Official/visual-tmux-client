import test from 'node:test'
import assert from 'node:assert/strict'

import { clearToken } from '../api'
import {
  cachedChildren,
  createTreeState,
  forgetDirectory,
  isExpanded,
  isTruncated,
  loadDirectory,
} from './tree'
import type { Entry } from './api'

interface Call {
  url: string
}

function setup() {
  const store = new Map<string, string>()
  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => store.set(key, val),
    removeItem: (key: string) => store.delete(key),
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    length: store.size,
  } as Storage
  clearToken()
}

function entry(name: string, isDir = false): Entry {
  return { name, is_dir: isDir, size: 0, mtime: 0 }
}

function mockList(pages: Record<string, { entries: Entry[]; truncated: boolean }>): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input)
    calls.push({ url })
    const path = new URL(url, 'http://localhost').searchParams.get('path') ?? ''
    const page = pages[path]
    if (!page) {
      return new Response(JSON.stringify({ error: 'not_found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(JSON.stringify({ path, ...page }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof fetch
  return calls
}

// Expanding a directory is what fetches it: nothing walks the tree up front.
test('a directory is fetched the first time it is asked for, then cached', async () => {
  setup()
  const calls = mockList({ '/srv': { entries: [entry('app', true)], truncated: false } })
  const state = createTreeState()

  assert.equal(cachedChildren(state, '/srv'), undefined)
  assert.equal(isExpanded(state, '/srv'), false)

  const first = await loadDirectory(state, '/srv')
  assert.equal(first.length, 1)
  assert.equal(calls.length, 1)

  const second = await loadDirectory(state, '/srv')
  assert.deepEqual(second, first)
  assert.equal(calls.length, 1, 'a second read should have come from the cache')
})

test('force re-reads a directory whose contents may have changed', async () => {
  setup()
  const calls = mockList({ '/srv': { entries: [], truncated: false } })
  const state = createTreeState()

  await loadDirectory(state, '/srv')
  await loadDirectory(state, '/srv', true)
  assert.equal(calls.length, 2)
})

test('a truncated listing is remembered', async () => {
  setup()
  mockList({ '/big': { entries: [entry('a.txt')], truncated: true } })
  const state = createTreeState()

  await loadDirectory(state, '/big')
  assert.equal(isTruncated(state, '/big'), true)

  mockList({ '/small': { entries: [], truncated: false } })
  await loadDirectory(state, '/small')
  assert.equal(isTruncated(state, '/small'), false)
})

// A directory that is deleted or renamed must not be served from the cache the
// next time it is opened.
test('forgetDirectory drops what was cached for a path', async () => {
  setup()
  const pages = { '/srv': { entries: [entry('app', true)], truncated: false } }
  const calls = mockList(pages)
  const state = createTreeState()

  await loadDirectory(state, '/srv')
  forgetDirectory(state, '/srv')
  assert.equal(cachedChildren(state, '/srv'), undefined)

  delete pages['/srv']
  await assert.rejects(() => loadDirectory(state, '/srv'))
  assert.equal(calls.length, 2)
})

test('a refused directory does not leave a partial cache entry', async () => {
  setup()
  const pages = { '/srv': { entries: [entry('secret.txt')], truncated: false } }
  delete pages['/srv']
  mockList(pages)
  const state = createTreeState()

  await assert.rejects(() => loadDirectory(state, '/srv'))
  assert.equal(cachedChildren(state, '/srv'), undefined)
})
