import test from 'node:test'
import assert from 'node:assert/strict'

import { clearToken, setToken } from '../api'
import {
  FileApiError,
  createEntry,
  deleteEntry,
  downloadFile,
  fetchWorkingDirectory,
  isFileApiError,
  listDirectory,
  readFile,
  probeFile,
  readFileBytes,
  renameEntry,
  writeFile,
  type Stamp,
} from './api'

/** stamp is an observed modification time with milliseconds only, which is what
 * a caller that has nothing more precise can report. */
function stamp(millis: number): Stamp {
  return { millis, nanos: null }
}

interface Call {
  url: string
  init?: RequestInit
}

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

/** mockFetch installs a fetch that returns `response` and records its calls. */
function mockFetch(response: Response): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init })
    return response
  }) as typeof fetch
  return calls
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function withoutStorage<T>(fn: () => Promise<T>): Promise<T> {
  setupMockStorage()
  clearToken()
  return fn()
}

test('listDirectory returns typed entries and encodes the path', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(
      jsonResponse({
        path: '/home/user',
        entries: [{ name: 'a.txt', is_dir: false, size: 3, mtime: 11 }],
        truncated: false,
      }),
    )

    const result = await listDirectory('/home/user with space')
    assert.equal(result.path, '/home/user')
    assert.equal(result.entries.length, 1)
    assert.equal(result.entries[0].name, 'a.txt')
    assert.equal(result.truncated, false)

    assert.equal(calls.length, 1)
    assert.equal(
      calls[0].url,
      '/api/hosts/local/files/list?path=%2Fhome%2Fuser%20with%20space',
    )
  })
})

// The save flow tells a lost race from every other failure by the code alone, so
// the code has to survive as a matchable value rather than a message.
test('a refused call carries the hub error code', async () => {
  await withoutStorage(async () => {
    mockFetch(jsonResponse({ error: 'conflict' }, 409))

    await assert.rejects(
      () => writeFile('/home/user/a.txt', 'body', stamp(1)),
      (err: unknown) => {
        assert.ok(err instanceof FileApiError)
        assert.equal((err as FileApiError).code, 'conflict')
        assert.equal(isFileApiError(err, 'conflict'), true)
        assert.equal(isFileApiError(err, 'not_found'), false)
        return true
      },
    )
  })
})

// Zero is a real modification time, so a value that merely *coerces* to zero must
// not be adopted as an observation nobody made: every later save would carry an
// expected time that never matches, and the user would be asked to confirm an
// overwrite on every save with nothing on screen explaining why.
//
// A write that reported no time at all is different: the bytes are on disk by
// then, so the answer is "no time" rather than a refusal -- refusing would leave
// the tab unable to record that it saved, and every later attempt would write the
// file again and fail the same way.
test('the write path refuses a coerced modification time but tolerates a missing one', async () => {
  await withoutStorage(async () => {
    for (const body of [{ mtime: false }, { mtime: true }, { mtime: [] }, { mtime: null }, {}]) {
      mockFetch(jsonResponse(body))
      assert.equal(
        await writeFile('/home/user/a.txt', 'hello', stamp(100)),
        null,
        `an mtime of ${JSON.stringify(body)} must not be adopted`,
      )
    }

    // A reported zero is a number the hub chose, and is adopted.
    mockFetch(jsonResponse({ mtime: 0 }))
    assert.equal((await writeFile('/home/user/a.txt', 'hello', stamp(100)))?.millis, 0)

    // A numeric string is what the read path already accepts, and is read the
    // same way here so the two cannot disagree about a hub's answer.
    mockFetch(jsonResponse({ mtime: '250' }))
    assert.equal((await writeFile('/home/user/a.txt', 'hello', stamp(100)))?.millis, 250)
  })
})

test('writeFile declares a byte length rather than a character count', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(jsonResponse({ mtime: 99 }))

    // "é" is two bytes in UTF-8, so a character count would be wrong here.
    const written = await writeFile('/home/user/a.txt', 'é', stamp(7))
    assert.equal(written?.millis, 99)

    const url = new URL(calls[0].url, 'http://localhost')
    assert.equal(url.searchParams.get('size'), '2')
    assert.equal(url.searchParams.get('expected_mtime'), '7')
    assert.equal(calls[0].init?.method, 'PUT')
  })
})

// Omitting the observed mtime is what asks the hub for a forced overwrite, so it
// must be absent rather than sent as zero.
test('writeFile omits expected_mtime when there is nothing to compare', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(jsonResponse({ mtime: 5 }))

    await writeFile('/home/user/a.txt', 'x', null)

    const url = new URL(calls[0].url, 'http://localhost')
    assert.equal(url.searchParams.has('expected_mtime'), false)
    assert.equal(url.searchParams.get('size'), '1')
  })
})

test('readFile reports the modification time the hub sent', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('hello', {
        status: 200,
        headers: { 'X-File-Size': '5', 'X-File-Mtime': '1737000000000' },
      }),
    )

    const contents = await readFile('/home/user/a.txt')
    assert.equal(contents.text, 'hello')
    assert.equal(contents.stamp.millis, 1737000000000)
    assert.equal(contents.stamp.nanos, null)
    assert.equal(contents.binary, false)
  })
})

// Decoding a binary file's bytes is what turns them into replacement characters,
// and saving that text back is what destroys the original. The hub classifies the
// contents, so the client never decodes them at all.
test('readFile does not decode contents the hub reported as binary', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('not really decoded', {
        status: 200,
        headers: {
          'X-File-Size': '19',
          'X-File-Mtime': '1737000000000',
          'X-File-Binary': '1',
        },
      }),
    )

    const contents = await readFile('/home/user/blob.bin')
    assert.equal(contents.binary, true)
    assert.equal(contents.text, '')
    assert.equal(contents.stamp.millis, 1737000000000)
  })
})

// The exact modification time is what the next save is checked against, and it
// travels as the string it arrived as. It does not fit in a JavaScript number --
// Number('1760000000123456789') is 1760000000123456800 -- and a rounded value
// matches no file, so adopting one would turn every save into a conflict with
// nothing on screen explaining why.
test('readFile carries the exact modification time as an opaque string', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('hello', {
        status: 200,
        headers: {
          'X-File-Size': '5',
          'X-File-Mtime': '1760000000123',
          'X-File-Mtime-Nanos': '1760000000123456789',
        },
      }),
    )

    const contents = await readFile('/home/user/a.txt')
    assert.equal(contents.stamp.millis, 1760000000123)
    assert.equal(contents.stamp.nanos, '1760000000123456789')
    assert.equal(typeof contents.stamp.nanos, 'string')
  })
})

// A hub that reports only milliseconds is still one this client can edit
// against: the weaker comparison is what it gave us to compare.
test('readFile tolerates a hub that reports only milliseconds', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('hello', {
        status: 200,
        headers: { 'X-File-Size': '5', 'X-File-Mtime': '1737000000000' },
      }),
    )

    const contents = await readFile('/home/user/a.txt')
    assert.equal(contents.stamp.millis, 1737000000000)
    assert.equal(contents.stamp.nanos, null)
  })
})

// The exact time is carried back to the hub verbatim, so a value the hub would
// reject must not be adopted as an observation: echoing it would make every
// later save answer invalid-body, and reopening the file would be the only way
// out. Anything that is not a plain decimal count is treated as the hub not
// reporting one, which costs the finer comparison and nothing else.
test('an exact modification time is adopted only when it can be carried back', async () => {
  await withoutStorage(async () => {
    const cases: Array<[string, string | null]> = [
      ['1760000000123456789', '1760000000123456789'],
      ['0', '0'],
      ['-1', '-1'],
      ['', null],
      ['abc', null],
      ['1e9', null],
      ['12 34', null],
      ['1760000000123456789.5', null],
      // All digits, and still more than the hub can parse: this goes back on the
      // next save, so a shape check that stops at "digits" is not enough.
      ['9223372036854775808', null],
      ['111111111111111111111111111111', null],
      // And the bounds themselves are accepted, since they are exactly what a
      // signed 64-bit integer holds.
      ['9223372036854775807', '9223372036854775807'],
      ['-9223372036854775808', '-9223372036854775808'],
    ]
    for (const [header, want] of cases) {
      mockFetch(
        new Response('hello', {
          status: 200,
          headers: {
            'X-File-Size': '5',
            'X-File-Mtime': '1737000000000',
            'X-File-Mtime-Nanos': header,
          },
        }),
      )
      const contents = await readFile('/home/user/a.txt')
      assert.equal(contents.stamp.nanos, want, `a header of ${JSON.stringify(header)}`)
    }

    // The write path adopts it the same way, since that value is what the next
    // save sends.
    mockFetch(jsonResponse({ mtime: 1, mtime_nanos: 'not a count' }))
    assert.equal((await writeFile('/home/user/a.txt', 'x', stamp(1)))?.nanos, null)
  })
})

// The write path reports it the same way, so the value a save adopts is the one
// the next save will be compared against.
test('writeFile adopts the exact modification time it is given back', async () => {
  await withoutStorage(async () => {
    mockFetch(jsonResponse({ mtime: 1760000000123, mtime_nanos: '1760000000123456789' }))

    const written = await writeFile('/home/user/a.txt', 'hello', stamp(1))

    assert.equal(written?.millis, 1760000000123)
    assert.equal(written?.nanos, '1760000000123456789')
  })
})

// The millisecond value's decimal form is what goes on the wire and the hub reads
// it back with a 64-bit integer parse, so a fraction or a value past what a double
// holds exactly must not be adopted: it would be sent as something the hub cannot
// read, and every later save would be refused as a malformed request -- which is
// not a conflict the user can answer.
test('readFile adopts only a millisecond time it can send back', async () => {
  await withoutStorage(async () => {
    for (const raw of ['1.5', '1e30', '9.007199254740993e15']) {
      mockFetch(
        new Response('hello', {
          status: 200,
          headers: { 'X-File-Size': '5', 'X-File-Mtime': String(raw) },
        }),
      )
      await assert.rejects(
        () => readFile('/home/user/a.txt'),
        /mtime_unavailable/,
        `an mtime of ${raw} must not be adopted`,
      )
    }
  })
})

// The hub always sends the header; a response without it means something is
// wrong, and adopting zero as the observed time would turn every later save into
// a conflict with nothing on screen explaining why.
test('readFile refuses a response that does not report a modification time', async () => {
  await withoutStorage(async () => {
    mockFetch(new Response('hello', { status: 200, headers: { 'X-File-Size': '5' } }))

    await assert.rejects(() => readFile('/home/user/a.txt'), /mtime_unavailable/)
  })
})

test('the mutation calls send the shapes the hub expects', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(new Response(null, { status: 204 }))

    await createEntry('/home/user/new', true)
    await renameEntry('/home/user/a', '/home/user/b')
    await deleteEntry('/home/user/a', true)

    const bodies = calls.map((call) => JSON.parse(String(call.init?.body)))
    assert.deepEqual(bodies[0], { path: '/home/user/new', kind: 'dir' })
    assert.deepEqual(bodies[1], { path: '/home/user/a', new_path: '/home/user/b' })
    assert.deepEqual(bodies[2], { path: '/home/user/a', recursive: true })
    assert.equal(calls[0].url, '/api/hosts/local/files/create')
  })
})

test('the bearer token accompanies every call', async () => {
  await withoutStorage(async () => {
    setToken('secret-token')
    const calls = mockFetch(new Response(null, { status: 204 }))

    await deleteEntry('/home/user/a', false)

    const headers = new Headers(calls[0].init?.headers)
    assert.equal(headers.get('Authorization'), 'Bearer secret-token')
  })
})

test('fetchWorkingDirectory reads the path the hub answered with', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(jsonResponse({ path: '/srv/project' }))

    const start = await fetchWorkingDirectory('my session')
    assert.equal(start.path, '/srv/project')
    assert.equal(start.substituted, false)
    assert.equal(calls[0].url, '/api/hosts/local/sessions/my%20session/working-directory')
  })
})

// The hub substitutes a directory when the session's own is outside the file
// boundary, and the manager has to tell the user rather than quietly opening
// somewhere else.
test('fetchWorkingDirectory reports a substituted directory', async () => {
  await withoutStorage(async () => {
    mockFetch(jsonResponse({ path: '/srv/projects', substituted: true }))

    const start = await fetchWorkingDirectory('work')
    assert.equal(start.path, '/srv/projects')
    assert.equal(start.substituted, true)
  })
})

test('downloadFile returns the bytes rather than the response', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(new Response('raw bytes', { status: 200 }))

    const blob = await downloadFile('/home/user/a.txt')
    assert.equal(await blob.text(), 'raw bytes')
    assert.equal(
      calls[0].url,
      '/api/hosts/local/files/download?path=%2Fhome%2Fuser%2Fa.txt',
    )
  })
})

// The hub answers a HEAD on the read route with the same headers and no body,
// which is what lets the browser ask what a file is instead of guessing from its
// name. Nothing but headers crosses the wire.
test('probeFile asks the hub for the classification without the bytes', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(
      new Response(null, {
        status: 200,
        headers: { 'X-File-Size': '4242', 'X-File-Binary': '1' },
      }),
    )

    const probe = await probeFile('/home/user/notes.dat')

    assert.equal(probe.binary, true)
    assert.equal(probe.size, 4242)
    assert.equal(calls[0].init?.method, 'HEAD')
    assert.equal(calls[0].url, '/api/hosts/local/files/read?path=%2Fhome%2Fuser%2Fnotes.dat')
  })
})

// A file the hub read as text is text whatever it is called, and that is the
// answer the editor needs before it decides whether to offer one.
test('probeFile reports a file the hub read as text', async () => {
  await withoutStorage(async () => {
    mockFetch(new Response(null, { status: 200, headers: { 'X-File-Size': '12' } }))

    const probe = await probeFile('/home/user/notes.dat')

    assert.equal(probe.binary, false)
    assert.equal(probe.size, 12)
  })
})

// The bound that decides whether an image is rendered is applied to the bytes
// the hub says it will send, not to the size a directory listing reported
// earlier: a file that grew since it was listed is the case this exists for.
test('readFileBytes refuses a file over the bound before reading it', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('pretend these bytes are huge', {
        status: 200,
        headers: { 'X-File-Size': '9000000' },
      }),
    )

    const fetched = await readFileBytes('/home/user/photo.png', 8 * 1024 * 1024)

    assert.ok('tooLarge' in fetched)
    assert.equal(fetched.tooLarge, 9000000)
  })
})

test('readFileBytes hands back the bytes of a file within the bound', async () => {
  await withoutStorage(async () => {
    mockFetch(
      new Response('pixels', { status: 200, headers: { 'X-File-Size': '6' } }),
    )

    const fetched = await readFileBytes('/home/user/photo.png', 8 * 1024 * 1024)

    assert.ok('blob' in fetched)
    assert.equal(await fetched.blob.text(), 'pixels')
  })
})

// The bound rests on the length the response reports, and the case it exists for
// is a file that grew past the listing's idea of its size. A response that
// reports nothing is the other half of the same guard: `Number(null)` is 0, so a
// missing header read as "nothing to worry about" unless absence is checked as
// absence.
test('readFileBytes measures a response that reports no usable length', async () => {
  await withoutStorage(async () => {
    // No header at all: the body is read, and the size reported back is the one
    // that really arrived.
    mockFetch(new Response('pixels', { status: 200 }))
    const measured = await readFileBytes('/home/user/photo.png', 8 * 1024 * 1024)
    assert.ok('blob' in measured)
    assert.equal(await measured.blob.text(), 'pixels')

    // A body over the bound is refused once it has been measured, rather than
    // rendered and frozen.
    mockFetch(new Response('x'.repeat(64), { status: 200 }))
    const over = await readFileBytes('/home/user/photo.png', 16)
    assert.ok('tooLarge' in over)
    assert.equal(over.tooLarge, 64)

    // And a header that is not a length is treated the same way as no header.
    for (const header of ['', 'abc', '-1', '1.5']) {
      mockFetch(new Response('pixels', { status: 200, headers: { 'X-File-Size': header } }))
      const fetched = await readFileBytes('/home/user/photo.png', 8 * 1024 * 1024)
      assert.ok('blob' in fetched, `a header of ${JSON.stringify(header)} should be measured`)
    }
  })
})

// The probe's size is displayed, so an unreported one must be null rather than
// zero: "0 B" is a claim about the file, and the listing's answer is a better one.
test('probeFile reports no size rather than a zero when the hub did not say', async () => {
  await withoutStorage(async () => {
    mockFetch(new Response(null, { status: 200 }))

    const probe = await probeFile('/home/user/notes.dat')

    assert.equal(probe.size, null)
    assert.equal(probe.binary, false)
  })
})
