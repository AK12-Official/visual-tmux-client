import test from 'node:test'
import assert from 'node:assert/strict'
import { initializeDesktop, DESKTOP_TOKEN_KEY, flushDesktopPreferences } from './desktop'
import { getSafeStorage, safeStorageGet, safeStorageSet, setPlatformStorage } from './storage'

function memoryStorage(): Storage {
  const data = new Map<string,string>()
  return { get length() { return data.size }, key: i => [...data.keys()][i] ?? null,
    getItem: key => data.get(key) ?? null, setItem: (key,value) => { data.set(key,value) },
    removeItem: key => { data.delete(key) }, clear: () => data.clear() }
}

test('desktop handoff clears launch address, hydrates before consumers, serializes writes and reloads', async () => {
  const session = memoryStorage()
  const calls: string[] = []
  let revision = 3
  const values = { 'vtc:locale':'zh-CN', 'vtc:font-size':'16', 'vtc:order:local':'{"mode":"manual","order":["dev"],"pinned":["dev"]}' }
  const fetchFn = (async (path, init) => {
    calls.push(String(path))
    assert.equal(init?.method, 'POST')
    if (String(path).endsWith('bootstrap')) {
      assert.deepEqual(JSON.parse(String(init?.body)), {code:'short-code'})
      return new Response(JSON.stringify({token:'process-secret'}))
    }
    assert.equal((init?.headers as Record<string,string>).Authorization,'Bearer process-secret')
    if (String(path).endsWith('preferences-read')) return new Response(JSON.stringify({version:1,revision,values}))
    const body = JSON.parse(String(init?.body))
    assert.equal(body.revision, revision++)
    assert.equal(body.values['visual-tmux-client-token'],undefined)
    return new Response(JSON.stringify({...body,revision}))
  }) as typeof fetch
  let scrubbed = false
  const history = { replaceState(_state:unknown,_title:string,address?:string|URL|null) {
    assert.equal(address,'/?desktop=1'); scrubbed=true
  } }
  const result = await initializeDesktop({search:'?desktop=1',hash:'#launch=short-code',pathname:'/'},history,session,fetchFn)
  assert.equal(result,true); assert.equal(scrubbed,true)
  assert.equal(safeStorageGet('vtc:locale'),'zh-CN')
  const { getLocale } = await import('./i18n/index')
  assert.equal(getLocale(),'zh-CN', 'locale must be hydrated before the first i18n import')
  assert.equal(safeStorageGet('visual-tmux-client-token'),'process-secret')
  assert.equal(session.getItem(DESKTOP_TOKEN_KEY),'process-secret')
  safeStorageSet('vtc:font-size','17'); safeStorageSet('vtc:font-size','18')
  await flushDesktopPreferences()
  assert.equal(revision,5)
  assert.equal(safeStorageGet('vtc:font-size'),'18')
  await initializeDesktop({search:'?desktop=1',hash:'',pathname:'/'},history,session,fetchFn)
  assert.equal(calls.filter(c=>c.endsWith('bootstrap')).length,1)
  setPlatformStorage()
})

test('ordinary browser does not call desktop APIs or override explicit storage', async () => {
  let called=false
  const browser=memoryStorage()
  const result=await initializeDesktop({search:'',hash:'',pathname:'/'},{replaceState(){}},browser, (async()=>{called=true;throw Error()}) as typeof fetch)
  assert.equal(result,false);assert.equal(called,false)
  assert.equal(getSafeStorage(browser),browser)
})

test('missing/invalid desktop authorization fails before installing preference adapter', async () => {
  setPlatformStorage()
  const session=memoryStorage()
  await assert.rejects(initializeDesktop({search:'?desktop=1',hash:'',pathname:'/'},{replaceState(){}},session,fetch),/authorization unavailable/)
  const refused=(async()=>new Response('refused',{status:401})) as typeof fetch
  await assert.rejects(initializeDesktop({search:'?desktop=1',hash:'#launch=expired',pathname:'/'},{replaceState(){}},session,refused),/401/)
})

test('failed preference persistence is not reported as durable success', async () => {
  const session=memoryStorage();session.setItem(DESKTOP_TOKEN_KEY,'process-secret')
  const fetchFn=(async(path)=>String(path).endsWith('preferences-read')
    ? new Response(JSON.stringify({version:1,revision:0,values:{}}))
    : new Response('disk unavailable',{status:500})) as typeof fetch
  await initializeDesktop({search:'?desktop=1',hash:'',pathname:'/'},{replaceState(){}},session,fetchFn)
  safeStorageSet('vtc:locale','en')
  await assert.rejects(flushDesktopPreferences(),/500/)
  assert.equal(safeStorageGet('vtc:locale'),'en')
  setPlatformStorage()
})

test('stale document credential gives native reload recovery before mounting', async () => {
  setPlatformStorage()
  const session = memoryStorage()
  session.setItem(DESKTOP_TOKEN_KEY, 'stale-process-token')
  const fetchFn = (async () => new Response('unauthorized', { status: 401 })) as typeof fetch
  await assert.rejects(initializeDesktop({ search: '?desktop=1', hash: '', pathname: '/' },
    { replaceState() {} }, session, fetchFn), /Application > Reload workspace/)
  assert.equal(session.getItem(DESKTOP_TOKEN_KEY), 'stale-process-token')
  setPlatformStorage()
})
