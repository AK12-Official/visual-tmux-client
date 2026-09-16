import test from 'node:test'
import assert from 'node:assert/strict'

import { clearToken } from '../api'
import { FileApiError } from './api'
import {
  anyDirty,
  applySaved,
  editable,
  isDirty,
  presentation,
  saveOpenFile,
  settleSave,
  type OpenFile,
} from './tabs'

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
    stamp: { millis: 100, nanos: null },
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

  const stamp = await saveOpenFile(file)
  assert.equal(stamp?.millis, 250)

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
  assert.equal(url.searchParams.has('expected_mtime_nanos'), false)
})

// The exact modification time is what a save is actually checked against, so it
// travels as well -- and it travels as the string it arrived as, because it does
// not fit in a JavaScript number and a rounded value matches no file.
test('a save sends the exact modification time it was given', async () => {
  setup()
  const calls = mockFetch(() => jsonResponse({ mtime: 250, mtime_nanos: '250500000' }))
  const file = openFile({
    text: 'edited',
    stamp: { millis: 1760000000123, nanos: '1760000000123456789' },
  })

  const stamp = await saveOpenFile(file)
  const url = new URL(calls[0].url, 'http://localhost')
  assert.equal(url.searchParams.get('expected_mtime'), '1760000000123')
  assert.equal(url.searchParams.get('expected_mtime_nanos'), '1760000000123456789')
  // And what comes back is adopted verbatim, ready for the next save.
  assert.equal(stamp?.nanos, '250500000')
})

// A hub that reports only milliseconds is still usable: the weaker comparison is
// all it has given the client to compare against.
test('a save survives a hub that reports only milliseconds', async () => {
  setup()
  mockFetch(() => jsonResponse({ mtime: 250 }))

  const stamp = await saveOpenFile(openFile({ text: 'edited' }))

  assert.equal(stamp?.millis, 250)
  assert.equal(stamp?.nanos, null)
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
  assert.equal(saved.stamp.millis, 777)

  await saveOpenFile(saved)
  const url = new URL(calls[1].url, 'http://localhost')
  assert.equal(url.searchParams.get('expected_mtime'), '777')
})

// A hub that did not report the new modification time must not stop the tab
// recording that it saved: the write happened, so the tab is clean, and the time
// left in place is what makes the next save ask before overwriting.
test('a save that reported no modification time still counts as saved', () => {
  const file = openFile({ text: 'edited', saved: 'original' })
  const saved = applySaved(file, null, 'edited')
  assert.equal(isDirty(saved), false)
  assert.deepEqual(saved.stamp, { millis: 100, nanos: null })
})

// An answer to a save is matched to the session that asked for it, so folding a
// save back in must not change which session the tab is.
test('applySaved keeps the tab identity it was given', () => {
  const file = openFile({ id: 7 })
  assert.equal(applySaved(file, { millis: 123, nanos: null }, file.text).id, 7)
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
  const stamp = await saveOpenFile(file)
  // The user keeps typing before the response is folded back in.
  const edited = { ...file, text: 'first and more' }

  const saved = applySaved(edited, stamp, sent)
  assert.equal(isDirty(saved), true)
  assert.equal(saved.text, 'first and more')
  assert.equal(saved.saved, 'first')
})

test('an answer for a tab that still holds the file it wrote marks it saved', () => {
  const file = openFile({ text: 'edited', saved: 'original' })

  const settled = settleSave([file], file.id, 'edited', file.path, { millis: 900, nanos: '900' })

  assert.equal(settled.outcome, 'saved')
  assert.equal(isDirty(settled.files[0]), false)
  assert.equal(settled.files[0].stamp.millis, 900)
})

// The bug this pins: a rename completing while a save is in flight moves the tab
// to the new path, and the answer that arrives afterwards describes the file the
// write named. Folding it in marked the tab clean at the new path, having never
// written a byte there -- the edit was left only at the old name, while the tab
// reported no unsaved changes and could be closed without a warning.
test('an answer for a path the tab has left does not mark it saved', () => {
  const sentPath = '/home/user/a.txt'
  const file = openFile({ text: 'edited', saved: 'original' })
  // The rename that landed while the write was travelling.
  const renamed = { ...file, path: '/home/user/b.txt', name: 'b.txt' }

  const settled = settleSave([renamed], file.id, 'edited', sentPath, { millis: 900, nanos: null })

  assert.equal(settled.outcome, 'moved')
  assert.equal(isDirty(settled.files[0]), true)
  // Nothing about the tab was rewritten: not its text, not its observed time.
  assert.deepEqual(settled.files[0], renamed)
})

test('an answer for a tab that was closed has nowhere to land', () => {
  const file = openFile()

  const settled = settleSave([], file.id, file.text, file.path, { millis: 900, nanos: null })

  assert.equal(settled.outcome, 'closed')
  assert.deepEqual(settled.files, [])
})

// A rename replaces the tab object, so the tab that is looked up afterwards is a
// different object holding the same session. Matching by identity rather than by
// session would have found nothing and reported the file closed.
test('a retargeted tab is still the same session', () => {
  const file = openFile({ text: 'edited', saved: 'original' })
  const renamed = { ...file, path: '/home/user/b.txt', name: 'b.txt' }

  const settled = settleSave([renamed], file.id, 'edited', renamed.path, { millis: 5, nanos: null })

  assert.equal(settled.outcome, 'saved')
  assert.equal(settled.files[0].path, '/home/user/b.txt')
  assert.equal(isDirty(settled.files[0]), false)
})

// The editor has to hold every file whose contents it is showing, and only
// those: a file it is not showing must not be mounted, and a file it is showing
// must not be unmounted just because the user looked elsewhere.
test('the editor holds source, and nothing it must not render as text', () => {
  assert.equal(editable(openFile({ name: 'main.go' })), true)
  assert.equal(editable(openFile({ name: 'notes.md' })), true)
  assert.equal(editable(openFile({ name: 'photo.png' })), false)
  assert.equal(editable(openFile({ name: 'archive.zip' })), false)
  // The hub's answer outranks the name in both directions.
  assert.equal(editable(openFile({ name: 'main.go', binary: true })), false)
  assert.equal(editable(openFile({ name: 'notes.md', binary: true })), false)
})

test('presentation follows the hub over the file name', () => {
  assert.equal(presentation(openFile({ name: 'photo.png' })), 'image')
  assert.equal(presentation(openFile({ name: 'notes.md' })), 'markdown')
  assert.equal(presentation(openFile({ name: 'main.go' })), 'editor')
  assert.equal(presentation(openFile({ name: 'main.go', binary: true })), 'info')
  assert.equal(presentation(openFile({ name: 'photo.png', binary: true })), 'info')
})
