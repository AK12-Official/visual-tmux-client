import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { REASONS, reasonFor } from './reasons'

/**
 * HUB_CODES is read out of the hub's own error mapper rather than listed here.
 *
 * A list on this side would be a second thing to keep in step, and the failure it
 * would fail to catch is the one that matters: a code added to the hub and not to
 * the browser does not break anything, it just shows the user a bare identifier
 * where a sentence should be. Reading the Go file is the only way one test can
 * hold both halves of that.
 */
function hubCodes(): string[] {
  const source = readFileSync(
    fileURLToPath(
      new URL('../../../internal/transport/http/files.go', import.meta.url),
    ),
    'utf8',
  )
  const codes = new Set<string>()
  // writeError(w, <status expression>, "code") -- the shape every arm of
  // mapFileError and every refusal in the file routes takes.
  for (const match of source.matchAll(/writeError\(\s*w,\s*[^,]+,\s*"([^"]+)"\)/g)) {
    codes.add(match[1])
  }
  return [...codes]
}

test('the hub file routes answer with codes this browser can explain', () => {
  const codes = hubCodes()
  // The reading itself is the first thing to fail if the hub's mapper changes
  // shape, and a test that quietly checked nothing would be worse than none.
  assert.ok(codes.length >= 8, `expected to read the hub's codes, found ${codes.length}`)

  for (const code of codes) {
    assert.equal(
      typeof REASONS[code],
      'string',
      `the hub answers with ${code} and the browser has no words for it`,
    )
    assert.ok(REASONS[code].length > 0, `the entry for ${code} is empty`)
  }
})

test('a code the hub raises that no route mints is explained too', () => {
  // requireMtime raises this one in files/api.ts; no response carries it.
  assert.equal(typeof REASONS['mtime_unavailable'], 'string')
})

test('a code with no entry falls through to itself', () => {
  assert.equal(reasonFor('something_new', 1024, String), 'something_new')
})

test('the too-large case names the limit instead of the refusal', () => {
  assert.equal(
    reasonFor('file_too_large', 104857600, (bytes) => `${bytes}B`),
    "it is larger than this hub's 104857600B limit for one file",
  )
  // A hub that did not say what its limit is has nothing to name, so the
  // refusal's own words are what is left.
  assert.equal(reasonFor('file_too_large', 0, String), REASONS['file_too_large'])
})
