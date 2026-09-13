import test from 'node:test'
import assert from 'node:assert/strict'
import {
  AuthError,
  clearToken,
  createSession,
  errorText,
  getToken,
  isHeaderSafeToken,
  issueTicket,
  killSession,
  listSessions,
  renameSession,
  setToken,
} from './api'

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

test('isHeaderSafeToken validates ISO-8859-1 header characters', () => {
  assert.equal(isHeaderSafeToken('valid-token-1234_abc'), true)
  assert.equal(isHeaderSafeToken('token with space'), true)
  assert.equal(isHeaderSafeToken('token\u00A0latin1'), true)

  // Disallowed characters
  assert.equal(isHeaderSafeToken('token\nwith\nnewline'), false)
  assert.equal(isHeaderSafeToken('token\x00null'), false)
  assert.equal(isHeaderSafeToken('中文令牌'), false)
  assert.equal(isHeaderSafeToken('token：fullwidth'), false)
})

test('token storage operations', () => {
  setupMockStorage()
  clearToken()
  assert.equal(getToken(), null)

  setToken('my-secret-token')
  assert.equal(getToken(), 'my-secret-token')

  clearToken()
  assert.equal(getToken(), null)
})

test('errorText extracts error messages properly', async () => {
  const jsonErr = new Response(JSON.stringify({ error: 'session_name_in_use' }), {
    status: 409,
    statusText: 'Conflict',
    headers: { 'Content-Type': 'application/json' },
  })
  assert.equal(await errorText(jsonErr), 'session_name_in_use')

  const jsonMsg = new Response(JSON.stringify({ message: 'rate limited' }), {
    status: 429,
    statusText: 'Too Many Requests',
    headers: { 'Content-Type': 'application/json' },
  })
  assert.equal(await errorText(jsonMsg), 'rate limited')

  const textErr = new Response('502 Bad Gateway from Nginx', {
    status: 502,
    statusText: 'Bad Gateway',
  })
  assert.equal(await errorText(textErr), '502 Bad Gateway from Nginx')

  const fallbackErr = new Response('', {
    status: 500,
    statusText: 'Internal Server Error',
  })
  assert.equal(await errorText(fallbackErr), '500 Internal Server Error')

  const noStatusTextErr = new Response('', {
    status: 500,
    statusText: '',
  })
  assert.equal(await errorText(noStatusTextErr), '500')
})

test('API endpoints: list, create, rename, kill, ticket', async () => {
  setupMockStorage()
  setToken('test-token')

  let lastUrl = ''
  let lastMethod = ''
  let lastBody: unknown = null
  let lastAuth = ''

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    lastUrl = String(input)
    lastMethod = init?.method ?? 'GET'
    lastBody = init?.body ? JSON.parse(String(init.body)) : null
    lastAuth = new Headers(init?.headers).get('Authorization') ?? ''

    if (lastUrl === '/api/hosts/local/sessions' && lastMethod === 'GET') {
      return new Response(JSON.stringify({
        sessions: [{ name: 's1', windows: 1, attached: 0, created: 1000 }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl === '/api/hosts/local/sessions' && lastMethod === 'POST') {
      const name = (lastBody as { name?: string } | null)?.name || 'session-auto-1'
      return new Response(JSON.stringify({
        name,
        windows: 1,
        attached: 0,
        created: 2000,
      }), { status: 201, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl === '/api/hosts/local/sessions/old-sess' && lastMethod === 'PATCH') {
      return new Response(JSON.stringify({
        name: 'new-sess',
        windows: 1,
        attached: 0,
        created: 2000,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl === '/api/hosts/local/sessions/kill-sess' && lastMethod === 'DELETE') {
      return new Response(null, { status: 204 })
    }

    if (lastUrl === '/api/ws-ticket' && lastMethod === 'POST') {
      return new Response(JSON.stringify({ ticket: 'secret-ticket-123' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }

    return new Response(null, { status: 404 })
  }

  // listSessions
  const sessions = await listSessions()
  assert.equal(sessions.length, 1)
  assert.equal(sessions[0].name, 's1')
  assert.equal(lastAuth, 'Bearer test-token')

  // createSession (nameless)
  const autoCreated = await createSession()
  assert.equal(autoCreated.name, 'session-auto-1')
  assert.deepEqual(lastBody, {})

  // createSession (explicit name)
  const explicitCreated = await createSession('my-sess')
  assert.equal(explicitCreated.name, 'my-sess')
  assert.deepEqual(lastBody, { name: 'my-sess' })

  // renameSession
  const renamed = await renameSession('old-sess', 'new-sess')
  assert.equal(renamed.name, 'new-sess')
  assert.deepEqual(lastBody, { name: 'new-sess' })

  // killSession
  await killSession('kill-sess')
  assert.equal(lastUrl, '/api/hosts/local/sessions/kill-sess')
  assert.equal(lastMethod, 'DELETE')

  // issueTicket
  const ticket = await issueTicket('my-sess')
  assert.equal(ticket, 'secret-ticket-123')
  assert.deepEqual(lastBody, { hostId: 'local', session: 'my-sess' })
})

test('API clears token and throws AuthError on 401', async () => {
  setupMockStorage()
  setToken('expired-token')

  globalThis.fetch = async (): Promise<Response> => {
    return new Response(JSON.stringify({ error: 'unauthorized' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  await assert.rejects(async () => {
    await listSessions()
  }, AuthError)

  assert.equal(getToken(), null) // Token cleared on 401
})
