import test from 'node:test'
import assert from 'node:assert/strict'

import { clearToken } from '../api'
import {
  cachedChildren,
  createTreeState,
  forgetDirectory,
  invalidateDirectory,
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

/**
 * deferredList answers each listing only when the test says so, so two waits for
 * one directory can be made to finish in the order the test chooses rather than
 * the order they were sent.
 */
function deferredList() {
  const waiting: { path: string; release: (entries: Entry[]) => void }[] = []
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input)
    const path = new URL(url, 'http://localhost').searchParams.get('path') ?? ''
    return await new Promise<Response>((resolve) => {
      waiting.push({
        path,
        release: (entries) =>
          resolve(
            new Response(JSON.stringify({ path, entries, truncated: false }), {
              status: 200,
              headers: { 'Content-Type': 'application/json' },
            }),
          ),
      })
    })
  }) as typeof fetch
  return waiting
}

// Two waits for one directory overlap whenever a navigation and a refresh name
// it, and the header stays live while a load is in flight. The older answer can
// therefore land last, and it describes the disk as it was when it was read --
// so what the tree renders is decided by which of the two is allowed to write.
test('the older of two waits for one directory does not write it', async () => {
  setup()
  const waits = deferredList()
  const state = createTreeState()

  const older = loadDirectory(state, '/srv', true)
  const newer = loadDirectory(state, '/srv', true)
  assert.equal(waits.length, 2, 'both waits should have gone to the hub')

  waits[1].release([entry('newer.txt')])
  assert.deepEqual(await newer, [entry('newer.txt')])
  waits[0].release([entry('older.txt')])
  // The superseded wait still answers its caller -- what it read is what that
  // caller asked for -- and only the cache is kept from it.
  assert.deepEqual(await older, [entry('older.txt')])

  assert.deepEqual(cachedChildren(state, '/srv'), [entry('newer.txt')])
})

// The other half of the same rule: a listing that was dropped while it was
// travelling must not be put back by its own answer. What dropped it is what
// knows the directory no longer describes anything.
test('a directory forgotten while its listing travelled does not get it back', async () => {
  setup()
  const waits = deferredList()
  const state = createTreeState()

  const travelling = loadDirectory(state, '/srv', true)
  forgetDirectory(state, '/srv')
  waits[0].release([entry('gone.txt')])
  await travelling

  assert.equal(cachedChildren(state, '/srv'), undefined)
  assert.equal(isExpanded(state, '/srv'), false)
})

// A call answered from the cache makes no claim about the disk, so it must not
// retire a wait that does. Issuing the ticket before the cache check would leave
// the stale listing in place and drop the answer that was coming.
test('an answer from the cache does not retire a listing that is travelling', async () => {
  setup()
  const waits = deferredList()
  const state = createTreeState()

  const first = loadDirectory(state, '/srv')
  waits[0].release([entry('old.txt')])
  await first

  const forced = loadDirectory(state, '/srv', true)
  assert.deepEqual(
    await loadDirectory(state, '/srv'),
    [entry('old.txt')],
    'the second read should have been served from the cache',
  )

  waits[1].release([entry('new.txt')])
  await forced
  assert.deepEqual(cachedChildren(state, '/srv'), [entry('new.txt')])
})

// And the rule does not outlive what it is for: a directory that was dropped and
// then read again is written by the read that follows.
test('a directory invalidated and read again is written by the later read', async () => {
  setup()
  const waits = deferredList()
  const state = createTreeState()

  const first = loadDirectory(state, '/srv', true)
  waits[0].release([entry('before.txt')])
  await first
  invalidateDirectory(state, '/srv')

  const again = loadDirectory(state, '/srv', true)
  waits[1].release([entry('after.txt')])
  await again
  assert.deepEqual(cachedChildren(state, '/srv'), [entry('after.txt')])
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

// A directory whose listing was dropped is not open, whatever the set still
// says: claiming otherwise would render it as open with nothing beneath it,
// which reads as an empty directory, and the next click would collapse
// something that was not showing.
test('visibleRows ignores an expanded directory that was never loaded', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true)])
  state.expanded.add('/srv/app')

  const rows = visibleRows(state, '/srv')
  assert.equal(rows.length, 1)
  assert.equal(rows[0].path, '/srv/app')
  assert.equal(rows[0].expanded, false)
  assert.equal(isExpanded(state, '/srv/app'), false)
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

// The bug this pins: forgetting a directory dropped only its own cache, so a
// directory that was deleted and then created again inherited the old one's
// subtree and rendered rows for entries that were gone.
test('forgetDirectory drops the cached subtree, not just the directory', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true)])
  state.children.set('/srv/app', [entry('gone.txt')])
  state.children.set('/srv/app/vendor', [entry('dep.go')])
  state.expanded.add('/srv/app')
  state.expanded.add('/srv/app/vendor')
  state.truncated.add('/srv/app')

  forgetDirectory(state, '/srv/app')

  assert.equal(cachedChildren(state, '/srv/app'), undefined)
  assert.equal(cachedChildren(state, '/srv/app/vendor'), undefined)
  assert.equal(isTruncated(state, '/srv/app'), false)
  // Nothing beneath it can still be open, and neither can it: a directory is
  // open only while what is inside it is known, and that is what just went.
  assert.equal(isExpanded(state, '/srv/app/vendor'), false)
  assert.equal(isExpanded(state, '/srv/app'), false)
  // The parent keeps its own listing: only what is beneath the named directory
  // goes, and /srv is above it, not below.
  assert.deepEqual(cachedChildren(state, '/srv'), [entry('app', true)])
})

// A change inside a directory says nothing about what is beneath it, so the
// narrow tool has to leave the subtree alone.
test('invalidateDirectory drops one listing and leaves the ones beneath it', () => {
  const state = createTreeState()
  state.children.set('/srv/app', [entry('a.txt')])
  state.children.set('/srv/app/vendor', [entry('dep.go')])
  state.truncated.add('/srv/app')

  invalidateDirectory(state, '/srv/app')

  assert.equal(cachedChildren(state, '/srv/app'), undefined)
  assert.equal(isTruncated(state, '/srv/app'), false)
  assert.deepEqual(cachedChildren(state, '/srv/app/vendor'), [entry('dep.go')])
})

test('a directory recreated after a delete renders no stale rows', () => {
  const state = createTreeState()
  state.children.set('/srv', [entry('app', true)])
  state.children.set('/srv/app', [entry('gone.txt')])
  state.expanded.add('/srv/app')
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => row.path),
    ['/srv/app', '/srv/app/gone.txt'],
  )

  forgetDirectory(state, '/srv/app')
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => row.path),
    ['/srv/app'],
  )

  // The directory comes back, freshly listed and empty.
  state.children.set('/srv/app', [])
  assert.deepEqual(
    visibleRows(state, '/srv').map((row) => row.path),
    ['/srv/app'],
  )
})

test('a refused directory does not leave a partial cache entry', async () => {
  setup()
  mockList({})
  const state = createTreeState()

  await assert.rejects(() => loadDirectory(state, '/srv'))
  assert.equal(cachedChildren(state, '/srv'), undefined)
})
