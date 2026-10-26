import test from 'node:test'
import assert from 'node:assert/strict'
import {
  AuthError,
  clearToken,
  createSession,
  errorText,
  getHostSettings,
  getToken,
  isHeaderSafeToken,
  issueTicket,
  killSession,
  listSessions,
  renameSession,
  setToken,
  updateHostSettings,
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

test('I18N-R5-01: setToken overrides old token when setItem throws and clearToken clears even when removeItem throws', () => {
  const store = new Map<string, string>()
  store.set('visual-tmux-client-token', 'old-persisted-token')

  let failSet = false
  let failRemove = false

  globalThis.localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, val: string) => {
      if (failSet) throw new DOMException('QuotaExceeded', 'QuotaExceededError')
      store.set(key, val)
    },
    removeItem: (key: string) => {
      if (failRemove) throw new DOMException('Forbidden', 'SecurityError')
      store.delete(key)
    },
    clear: () => store.clear(),
  } as any

  // 1. Initial state reads pre-existing storage token
  clearToken()
  store.set('visual-tmux-client-token', 'old-persisted-token')
  assert.equal(getToken(), 'old-persisted-token')

  // 2. setItem fails with QuotaExceededError
  failSet = true
  setToken('new-active-token')
  // Must return new-active-token, not the old-persisted-token
  assert.equal(getToken(), 'new-active-token')

  // 3. removeItem fails with SecurityError
  failRemove = true
  clearToken()
  // Must return null, not old-persisted-token
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
  let lastHeaders = new Headers()

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    lastUrl = String(input)
    lastMethod = init?.method ?? 'GET'
    lastBody = init?.body ? JSON.parse(String(init.body)) : null
    lastHeaders = new Headers(init?.headers)
    lastAuth = lastHeaders.get('Authorization') ?? ''

    if (lastUrl.match(/^\/api\/hosts\/[^/]+\/sessions$/) && lastMethod === 'GET') {
      return new Response(JSON.stringify({
        sessions: [{ name: 's1', windows: 1, attached: 0, created: 1000 }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl.match(/^\/api\/hosts\/[^/]+\/sessions$/) && lastMethod === 'POST') {
      const name = (lastBody as { name?: string } | null)?.name || 'session-auto-1'
      return new Response(JSON.stringify({
        name,
        windows: 1,
        attached: 0,
        created: 2000,
      }), { status: 201, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl.match(/^\/api\/hosts\/[^/]+\/sessions\/[^/]+$/) && lastMethod === 'PATCH') {
      const name = (lastBody as { name?: string } | null)?.name || 'new-sess'
      return new Response(JSON.stringify({
        name,
        windows: 1,
        attached: 0,
        created: 2000,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }

    if (lastUrl.match(/^\/api\/hosts\/[^/]+\/sessions\/[^/]+$/) && lastMethod === 'DELETE') {
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
  const sessions = await listSessions('local')
  assert.equal(sessions.length, 1)
  assert.equal(sessions[0].name, 's1')
  assert.equal(lastAuth, 'Bearer test-token')

  // createSession (nameless)
  const autoCreated = await createSession(undefined, 'local')
  assert.equal(autoCreated.name, 'session-auto-1')
  assert.deepEqual(lastBody, {})

  // createSession (explicit name)
  const explicitCreated = await createSession('my-sess', 'local')
  assert.equal(explicitCreated.name, 'my-sess')
  assert.deepEqual(lastBody, { name: 'my-sess' })

  // renameSession
  const renamed = await renameSession('old-sess', 'new-sess', 'local')
  assert.equal(renamed.name, 'new-sess')
  assert.deepEqual(lastBody, { name: 'new-sess' })

  // killSession
  await killSession('kill-sess', 'local')
  assert.equal(lastUrl, '/api/hosts/local/sessions/kill-sess')
  assert.equal(lastMethod, 'DELETE')

  // issueTicket
  const ticket = await issueTicket('my-sess', 'local')
  assert.equal(ticket, 'secret-ticket-123')
  assert.deepEqual(lastBody, { hostId: 'local', session: 'my-sess' })

  // Remote host-scoped API operations
  await listSessions('remote-1')
  assert.equal(lastUrl, '/api/hosts/remote-1/sessions')
  assert.equal(lastMethod, 'GET')

  await createSession('r-sess', 'remote-1', 42)
  assert.equal(lastUrl, '/api/hosts/remote-1/sessions')
  assert.equal(lastMethod, 'POST')
  assert.equal(lastHeaders.get('X-Expected-Connection-Revision'), '42')
  assert.equal((lastBody as { expectedConnectionRevision?: number })?.expectedConnectionRevision, 42)

  await renameSession('old-r', 'new-r', 'remote-1', 42)
  assert.equal(lastUrl, '/api/hosts/remote-1/sessions/old-r')
  assert.equal(lastMethod, 'PATCH')
  assert.equal(lastHeaders.get('X-Expected-Connection-Revision'), '42')
  assert.equal((lastBody as { expectedConnectionRevision?: number })?.expectedConnectionRevision, 42)

  await killSession('dead-r', 'remote-1')
  assert.equal(lastUrl, '/api/hosts/remote-1/sessions/dead-r')
  assert.equal(lastMethod, 'DELETE')

  const remoteTicket = await issueTicket('r-sess', 'remote-1')
  assert.equal(remoteTicket, 'secret-ticket-123')
  assert.deepEqual(lastBody, { hostId: 'remote-1', session: 'r-sess' })
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
    await listSessions('local')
  }, AuthError)

  assert.equal(getToken(), null) // Token cleared on 401
})

test('API supports getHostSettings and updateHostSettings', async () => {
  setupMockStorage()
  setToken('test-token')

  let lastUrl = ''
  let lastMethod = ''
  let lastBody: unknown = null

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    lastUrl = String(input)
    lastMethod = init?.method ?? 'GET'
    if (init?.body) lastBody = JSON.parse(String(init.body))

    if (lastUrl === '/api/host-settings' && lastMethod === 'GET') {
      return new Response(JSON.stringify({ defaultHostId: 'server-1', revision: 2 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (lastUrl.startsWith('/api/host-settings?revision=2') && lastMethod === 'PUT') {
      return new Response(JSON.stringify({ defaultHostId: 'local', revision: 3 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(null, { status: 404 })
  }

  const settings = await getHostSettings()
  assert.equal(settings.defaultHostId, 'server-1')
  assert.equal(settings.revision, 2)
  assert.equal(lastUrl, '/api/host-settings')
  assert.equal(lastMethod, 'GET')

  const updated = await updateHostSettings('local', 2)
  assert.equal(updated.defaultHostId, 'local')
  assert.equal(updated.revision, 3)
  assert.equal(lastUrl, '/api/host-settings?revision=2')
  assert.equal(lastMethod, 'PUT')
  assert.deepEqual(lastBody, { defaultHostId: 'local', revision: 2 })
})
