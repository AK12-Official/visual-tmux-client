import test from 'node:test'
import assert from 'node:assert/strict'
import '../test/dom.mjs'
import { enterFullscreen, leaveFullscreen, isFullscreen } from './platform'
import { initializeDesktop } from './desktop'
import { syncDesktopFullscreen } from './desktop-window'

test('browser fullscreen keeps DOM API; native fullscreen restores terminal layout on OS exit', async () => {
  const region = document.createElement('main')
  let browserEntries = 0
  let browserExits = 0
  region.requestFullscreen = async () => { browserEntries++ }
  document.exitFullscreen = async () => { browserExits++ }
  await enterFullscreen(region)
  await leaveFullscreen()
  assert.equal(browserEntries, 1)
  assert.equal(browserExits, 1)

  const originalFetch = globalThis.fetch
  const requests: { name: string; body: unknown }[] = []
  const fetchFixture: typeof fetch = async (input, init) => {
    const name = String(input).split('/').at(-1) ?? ''
    requests.push({ name, body: JSON.parse(String(init?.body)) })
    return new Response(JSON.stringify(name === 'bootstrap' ? { token: 'fixture' }
      : name === 'preferences-read' ? { version: 1, revision: 0, values: {} } : { ok: true }))
  }
  try {
    await initializeDesktop({ search: '?desktop=1', hash: '#launch=fixture', pathname: '/' },
      { replaceState() {} }, window.sessionStorage, fetchFixture)
    globalThis.fetch = fetchFixture
    await enterFullscreen(region)
    assert.equal(isFullscreen(), true)
    assert.equal(region.classList.contains('app__main--desktop-fullscreen'), true)
    syncDesktopFullscreen(false) // e.g. Cocoa's system fullscreen exit
    assert.equal(isFullscreen(), false)
    assert.equal(region.classList.contains('app__main--desktop-fullscreen'), false)
    await enterFullscreen(region)
    await leaveFullscreen()
    assert.deepEqual(requests.filter(r => r.name === 'fullscreen').map(r => r.body),
      [{ enabled: true }, { enabled: true }, { enabled: false }])
    assert.equal(browserEntries, 1)
    assert.equal(browserExits, 1)
  } finally {
    globalThis.fetch = originalFetch
    syncDesktopFullscreen(false)
  }
})
