import test from 'node:test'
import assert from 'node:assert/strict'

import { clearToken } from '../api'
import { FileApiError } from './api'
import { anyDirty, applySaved, isDirty, saveOpenFile, type OpenFile } from './tabs'

interface Call {
  url: string
  init?: RequestInit
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

// mockFetch installs a fetch that records its calls. It takes a factory rather
// than a response because a Response body can only be read once, so a test that
// makes more than one request needs a fresh one each time.
function mockFetch(makeResponse: () => Response): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init })
    return makeResponse()
  }) as typeof fetch
  return calls
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function openFile(overrides: Partial<OpenFile> = {}): OpenFile {
  return {
    id: 1,
    path: '/home/user/a.txt',
    name: 'a.txt',
    text: 'hello',
    saved: 'hello',
    mtime: 100,
    size: 5,
    binary: false,
    ...overrides,
  }
}

test('dirty is a difference from the last read or write', () => {
  assert.equal(isDirty(openFile()), false)
  assert.equal(isDirty(openFile({ text: 'changed' })), true)
  assert.equal(anyDirty([openFile(), openFile({ text: 'changed' })]), true)
  assert.equal(anyDirty([openFile(), openFile()]), false)
})

test('a save sends the observed modification time', async () => {
  setup()
  const calls = mockFetch(() => jsonResponse({ mtime: 250 }))
  const file = openFile({ text: 'edited' })

  const mtime = await saveOpenFile(file)
  assert.equal(mtime, 250)

  const url = new URL(calls[0].url, 'http://localhost')
  assert.equal(url.searchParams.get('expected_mtime'), '100')
  assert.equal(url.searchParams.get('size'), '6')
})

// Confirming an overwrite after a conflict drops the observed time: that is the
// only thing that asks the hub to replace the file regardless of who changed it.
test('a forced save sends no observed modification time', async () => {
  setup()
  const calls = mockFetch(() => jsonResponse({ mtime: 300 }))

  await saveOpenFile(openFile({ text: 'mine' }), true)

  const url = new URL(calls[0].url, 'http://localhost')
  assert.equal(url.searchParams.has('expected_mtime'), false)
})

test('a lost race surfaces as a conflict the caller can match', async () => {
  setup()
  mockFetch(() => jsonResponse({ error: 'conflict' }, 409))

  await assert.rejects(
    () => saveOpenFile(openFile({ text: 'mine' })),
    (err: unknown) => {
      assert.ok(err instanceof FileApiError)
      assert.equal((err as FileApiError).code, 'conflict')
      return true
    },
  )
})

// The hub returns the real modification time, and adopting it is what keeps a
// second save from looking like a conflict.
test('adopting the returned mtime makes the next save clean', async () => {
  setup()
  const calls = mockFetch(() => jsonResponse({ mtime: 777 }))
  const file = openFile({ text: 'first' })

  const saved = applySaved(file, await saveOpenFile(file), file.text)
  assert.equal(isDirty(saved), false)
  assert.equal(saved.mtime, 777)

  await saveOpenFile(saved)
  const url = new URL(calls[1].url, 'http://localhost')
  assert.equal(url.searchParams.get('expected_mtime'), '777')
})

// A hub that did not report the new modification time must not stop the tab
// recording that it saved: the write happened, so the tab is clean, and the time
// left in place is what makes the next save ask before overwriting.
test('a save that reported no modification time still counts as saved', () => {
  const file = openFile({ text: 'edited', saved: 'original', mtime: 100 })
  const saved = applySaved(file, null, 'edited')
  assert.equal(isDirty(saved), false)
  assert.equal(saved.mtime, 100)
})

// An answer to a save is matched to the session that asked for it, so folding a
// save back in must not change which session the tab is.
test('applySaved keeps the tab identity it was given', () => {
  const file = openFile({ id: 7 })
  assert.equal(applySaved(file, 123, file.text).id, 7)
})

// The bug this pins: `saved` used to be read from the live object when the
// response landed, so a keystroke typed while the write was in flight was
// recorded as saved without ever reaching the disk. The tab then reported itself
// clean, and the edit could be thrown away with no warning.
test('a keystroke typed during a save is still unsaved afterwards', async () => {
  setup()
  mockFetch(() => jsonResponse({ mtime: 777 }))
  const file = openFile({ text: 'first' })

  const sent = file.text
  const mtime = await saveOpenFile(file)
  // The user keeps typing before the response is folded back in.
  const edited = { ...file, text: 'first and more' }

  const saved = applySaved(edited, mtime, sent)
  assert.equal(isDirty(saved), true)
  assert.equal(saved.text, 'first and more')
  assert.equal(saved.saved, 'first')
})
