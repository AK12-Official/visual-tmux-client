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
  visibleRows,
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

// The tree is rendered from this, so it decides what a user can actually see.
test('visibleRows shows the root plus whatever is expanded beneath it', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true), entry('notes.txt')])
  state.children.set('/srv/app', [entry('main.go'), entry('vendor', true)])
  state.children.set('/srv/app/vendor', [entry('dep.go')])

  // Nothing expanded: only the root's own children, in listing order.
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => [row.path, row.depth]),
    [
      ['/srv/app', 0],
      ['/srv/notes.txt', 0],
    ],
  )

  state.expanded.add('/srv/app')
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => [row.path, row.depth]),
    [
      ['/srv/app', 0],
      ['/srv/app/main.go', 1],
      ['/srv/app/vendor', 1],
      ['/srv/notes.txt', 0],
    ],
  )

  state.expanded.add('/srv/app/vendor')
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => [row.path, row.depth]),
    [
      ['/srv/app', 0],
      ['/srv/app/main.go', 1],
      ['/srv/app/vendor', 1],
      ['/srv/app/vendor/dep.go', 2],
      ['/srv/notes.txt', 0],
    ],
  )
})

test('visibleRows marks which rows are open', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true), entry('notes.txt')])
  state.children.set('/srv/app', [])
  state.expanded.add('/srv/app')

  const rows = visibleRows(state, '/srv')
  assert.equal(rows[0].expanded, true)
  // A file is never "expanded", however it is named.
  assert.equal(rows[1].expanded, false)

  state.expanded.delete('/srv/app')
  assert.equal(visibleRows(state, '/srv')[0].expanded, false)
})

// A directory whose children have not been fetched yet shows as collapsed and
// contributes nothing, rather than inventing rows.
test('visibleRows ignores an expanded directory that was never loaded', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true)])
  state.expanded.add('/srv/app')

  const rows = visibleRows(state, '/srv')
  assert.equal(rows.length, 1)
  assert.equal(rows[0].path, '/srv/app')
  assert.equal(rows[0].expanded, true)
})

test('visibleRows joins paths from the filesystem root', () => {
  const state = createTreeState()
  state.children.set('/', [entry('srv', true)])
  state.children.set('/srv', [entry('a.txt')])
  state.expanded.add('/srv')

  assert.deepEqual(
    visibleRows(state, '/').map((row) => row.path),
    ['/srv', '/srv/a.txt'],
  )
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
