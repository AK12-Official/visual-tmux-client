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
  renameEntry,
  writeFile,
} from './api'

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
      () => writeFile('/home/user/a.txt', 'body', 1),
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

test('writeFile declares a byte length rather than a character count', async () => {
  await withoutStorage(async () => {
    const calls = mockFetch(jsonResponse({ mtime: 99 }))

    // "é" is two bytes in UTF-8, so a character count would be wrong here.
    const mtime = await writeFile('/home/user/a.txt', 'é', 7)
    assert.equal(mtime, 99)

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
    assert.equal(contents.mtime, 1737000000000)
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

    const dir = await fetchWorkingDirectory('my session')
    assert.equal(dir, '/srv/project')
    assert.equal(calls[0].url, '/api/hosts/local/sessions/my%20session/working-directory')
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
