import test from 'node:test'
import assert from 'node:assert/strict'
import { desktopLinkTarget } from './desktop-links'

test('desktop link routing keeps owned links and rejects unsupported schemes/credentials', () => {
  const origin = 'http://127.0.0.1:38123'
  assert.equal(desktopLinkTarget('#section', origin), 'internal')
  assert.equal(desktopLinkTarget('/?desktop=1', origin), 'internal')
  assert.equal(desktopLinkTarget('https://example.test/docs', origin), 'external')
  assert.equal(desktopLinkTarget('http://127.0.0.1:38124', origin), 'external')
  for (const href of ['javascript:alert(1)', 'file:///tmp/data', 'mailto:test@example.test', 'https://user:secret@example.test']) {
    assert.equal(desktopLinkTarget(href, origin), 'blocked')
  }
})
