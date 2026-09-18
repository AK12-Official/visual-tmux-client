import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { clearToken } from '../api'
import { FileApiError } from './api'
import {
  anyDirty,
  applySaved,
  beginSave,
  isDirty,
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

  const stamp = await saveOpenFile(beginSave(file))
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

  await saveOpenFile(beginSave(openFile({ text: 'mine' }), true))

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

  const stamp = await saveOpenFile(beginSave(file))
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

  const stamp = await saveOpenFile(beginSave(openFile({ text: 'edited' })))

  assert.equal(stamp?.millis, 250)
  assert.equal(stamp?.nanos, null)
})

test('a lost race surfaces as a conflict the caller can match', async () => {
  setup()
  mockFetch(() => jsonResponse({ error: 'conflict' }, 409))

  await assert.rejects(
    () => saveOpenFile(beginSave(openFile({ text: 'mine' }))),
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

  const saved = applySaved(file, await saveOpenFile(beginSave(file)), file.text)
  assert.equal(isDirty(saved), false)
  assert.equal(saved.stamp.millis, 777)

  await saveOpenFile(beginSave(saved))
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

  const request = beginSave(file)
  const stamp = await saveOpenFile(request)
  // The user keeps typing before the response is folded back in.
  const edited = { ...file, text: 'first and more' }

  const saved = applySaved(edited, stamp, request.text)
  assert.equal(isDirty(saved), true)
  assert.equal(saved.text, 'first and more')
  assert.equal(saved.saved, 'first')
})

// The path, the text, and the session are captured once, together, and the
// answer is matched against that same object. The bug this pins: the path was
// read again after the await, so a rename that completed during the write -- and
// which replaces the tab object -- left the comparison agreeing with itself, and
// the tab at the *new* name was marked saved by a write that named the old one.
test('a save request captures the path it will name', () => {
  const file = openFile({ text: 'edited', saved: 'original' })

  const request = beginSave(file)

  assert.equal(request.path, '/home/user/a.txt')
  assert.equal(request.text, 'edited')
  assert.equal(request.id, file.id)
  assert.deepEqual(request.expected, file.stamp)
})

test('an answer for a tab that still holds the file it wrote marks it saved', () => {
  const file = openFile({ text: 'edited', saved: 'original' })

  const settled = settleSave([file], beginSave(file), { millis: 900, nanos: '900' })

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
  const file = openFile({ text: 'edited', saved: 'original' })
  const request = beginSave(file)
  // The rename that landed while the write was travelling.
  const renamed = { ...file, path: '/home/user/b.txt', name: 'b.txt' }

  const settled = settleSave([renamed], request, { millis: 900, nanos: null })

  assert.equal(settled.outcome, 'moved')
  assert.equal(isDirty(settled.files[0]), true)
  // Nothing about the tab was rewritten: not its text, not its observed time.
  assert.deepEqual(settled.files[0], renamed)
})

test('an answer for a tab that was closed has nowhere to land', () => {
  const file = openFile()

  const settled = settleSave([], beginSave(file), { millis: 900, nanos: null })

  assert.equal(settled.outcome, 'closed')
  assert.deepEqual(settled.files, [])
})

// A rename replaces the tab object, so the tab that is looked up afterwards is a
// different object holding the same session. Matching by identity rather than by
// session would have found nothing and reported the file closed.
test('a retargeted tab is still the same session', () => {
  const file = openFile({ text: 'edited', saved: 'original' })
  const rename = { ...file, path: '/home/user/b.txt', name: 'b.txt' }

  const settled = settleSave([rename], beginSave(rename), { millis: 5, nanos: null })

  assert.equal(settled.outcome, 'saved')
  assert.equal(settled.files[0].path, '/home/user/b.txt')
  assert.equal(isDirty(settled.files[0]), false)
})

// A save answer that arrives while a rename of its path or of a directory above it is outstanding describes
// a write whose fate the browser cannot know: the write may have landed before
// the rename carried the entry away -- in which case the tab is saved -- or the
// rename may have landed first and the write recreated the old name, in which
// case the entry the tab now holds was never written. Recording it as saved
// would be a lie in the second case, so the answer is not recorded.
test('an answer that crossed a rename is not recorded', () => {
  const file = openFile({ text: 'edited', saved: 'original' })

  const settled = settleSave([file], beginSave(file), { millis: 900, nanos: null }, true)

  assert.equal(settled.outcome, 'raced')
  assert.equal(isDirty(settled.files[0]), true)
  assert.deepEqual(settled.files[0], file)
})

// A rename is the more specific news: once the tab is at another path, that is
// why the answer does not describe it, whether or not a rename is still running.
test('a moved tab is reported as moved even while a rename is outstanding', () => {
  const file = openFile({ text: 'edited', saved: 'original' })
  const renamed = { ...file, path: '/home/user/b.txt', name: 'b.txt' }

  const settled = settleSave([renamed], beginSave(file), null, true)

  assert.equal(settled.outcome, 'moved')
})

// And a rename that has already finished leaves nothing to race with: the answer
// describes the file the tab holds, because the tab was never moved.
test('an answer is recorded normally once no rename is outstanding', () => {
  const file = openFile({ text: 'edited', saved: 'original' })

  const settled = settleSave([file], beginSave(file), { millis: 900, nanos: null }, false)

  assert.equal(settled.outcome, 'saved')
  assert.equal(isDirty(settled.files[0]), false)
})

// The boundary this module's own comment claims -- free of the renderer -- is
// asserted rather than described, because describing it is what failed. The
// second review read this file, saw an `import type` naming the renderer's
// module, and recorded the boundary as closed: a type import erases at compile
// time, so nothing was broken at runtime and nothing failed either. A reading is
// not evidence; this is.
//
// The specifier is what is searched for, not the statement, because every way of
// naming a module names it as a string: a single-line import, a multi-line one
// (which this repository writes often), `import './preview'` for its side effect,
// `import('./preview')`, `import(/* @vite-ignore */ './preview')`, and
// `export ... from './preview'`. Searching for the
// statement shape catches only the first of those -- which is how this test was
// written first, and what a reviewer walked through five shapes to show.
//
// What it still does not catch, and the reason it is not a graph walk: a module
// that reaches the renderer through a barrel file under another name. A module
// resolver would be needed for that, and this test has no bundler.
test('tabs.ts does not name the renderer', () => {
  const source = readFileSync(fileURLToPath(new URL('./tabs.ts', import.meta.url)), 'utf8')
  const specifiers = [
    // import ... from '<specifier>'  |  export ... from '<specifier>'
    ...[...source.matchAll(/\bfrom\s*(?:\/\*[\s\S]*?\*\/\s*)?['"]([^'"]+)['"]/g)].map((m) => m[1]),
    // import '<specifier>'  |  import('<specifier>')
    ...[...source.matchAll(/\bimport\s*(?:\/\*[\s\S]*?\*\/\s*)?\(?\s*['"]([^'"]+)['"]/g)].map((m) => m[1]),
  ]

  assert.ok(specifiers.length > 0, 'no specifiers were read, so this proves nothing')
  for (const specifier of specifiers) {
    // The module itself, not merely a name containing it: ./previewPrefs.ts is
    // not the renderer.
    const named = specifier.replace(/\.(ts|js)$/, '')
    assert.ok(
      !/(^|\/)preview$/.test(named),
      `tabs.ts reaches ${specifier}; the renderer runs the Markdown sanitizer at module scope`,
    )
  }
})
