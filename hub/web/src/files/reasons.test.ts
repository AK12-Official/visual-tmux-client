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
 * where a sentence should be. Reading the Go file holds both halves together in
 * one place, which a list on either side cannot do -- a generated artifact would,
 * at the cost of a build step and a checked-in file for eleven strings.
 *
 * What it does not catch: an arm written in a shape the pattern below does not
 * match, such as a table-driven mapper. The count assertion is what turns that
 * from a test that quietly checks nothing into a failure.
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
  // mapFileError takes. Two codes a file route can still answer with are raised
  // elsewhere and are named by the test below rather than reached by this scan.
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

test('the codes a file route can answer with that its mapper does not mint', () => {
  // requireMtime raises this one in files/api.ts; no response carries it.
  //
  // host_not_found is raised by requireLocalHost, which wraps every file route --
  // so a request to a host that is not this one reaches the file client with it.
  // It is named here rather than reached by the scan above because it lives in
  // router.go, whose other codes belong to the session and terminal routes the
  // file client never calls; reading that file would demand words for those.
  for (const code of ['mtime_unavailable', 'host_not_found']) {
    assert.equal(typeof REASONS[code], 'string', `no words for ${code}`)
    assert.ok(REASONS[code].length > 0, `the entry for ${code} is empty`)
  }
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
