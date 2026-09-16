import test from 'node:test'
import assert from 'node:assert/strict'

import { createRenames } from './renames'

// The defect this exists for, which survived two rounds of review because it only
// lives in an interleaving: a rename recorded when no read was travelling must
// not redirect a read that merely overlapped some *other* rename. Consulting the
// record without its generation makes it a history rather than this read's
// business, and the read lands on a different file under a name nobody opened.
test('a read ignores renames that happened before it started', () => {
  const renames = createRenames()
  renames.record('/d/a.txt', '/d/b.txt')

  const startedAt = renames.generation()
  renames.record('/d/c.txt', '/d/e.txt')

  assert.equal(renames.resolve('/d/a.txt', startedAt), '/d/a.txt')
  assert.equal(renames.resolve('/d/c.txt', startedAt), '/d/e.txt')
})

test('a read follows a rename that happened while it was travelling', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/a.txt', '/d/b.txt')

  assert.equal(renames.resolve('/d/a.txt', startedAt), '/d/b.txt')
  assert.equal(renames.resolve('/d/untouched.txt', startedAt), '/d/untouched.txt')
})

// Renames chain, so a read that started before both has to end up at the last
// name -- not the first one it finds.
test('a read follows a chain of renames', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/a.txt', '/d/b.txt')
  renames.record('/d/b.txt', '/d/c.txt')

  assert.equal(renames.resolve('/d/a.txt', startedAt), '/d/c.txt')
})

// A file renamed back to where it started is where it started. Without the
// already-visited guard this walks A→B→A→B forever.
test('a rename back to the original name resolves to the original name', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/a.txt', '/d/b.txt')
  renames.record('/d/b.txt', '/d/a.txt')

  assert.equal(renames.resolve('/d/a.txt', startedAt), '/d/a.txt')
})

test('a rename of a directory carries the paths beneath it', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/old', '/d/new')

  assert.equal(renames.resolve('/d/old/f.txt', startedAt), '/d/new/f.txt')
  assert.equal(renames.resolve('/d/old', startedAt), '/d/new')
})

// A shallow rename and a deeper one can both describe a path, and the order they
// happened in is what decides which one still names it. Picking the deeper match
// regardless would follow a rename of a directory that the earlier shallow rename
// had already carried away.
test('a rename is applied to what the path is by the time it happens', () => {
  // The whole directory moved first, so the later rename of something inside it
  // names a path that is no longer there.
  const shallowFirst = createRenames()
  let startedAt = shallowFirst.generation()
  shallowFirst.record('/d/a', '/d/b')
  shallowFirst.record('/d/a/x', '/d/c')
  assert.equal(shallowFirst.resolve('/d/a/x/y', startedAt), '/d/b/x/y')

  // The inner one moved first, carrying the path out of the directory that the
  // later rename then moved.
  const deepFirst = createRenames()
  startedAt = deepFirst.generation()
  deepFirst.record('/d/a/x', '/d/c')
  deepFirst.record('/d/a', '/d/b')
  assert.equal(deepFirst.resolve('/d/a/x/y', startedAt), '/d/c/y')
})

// A prefix is a path element, not a string: renaming /d/ab must not carry /d/abc.
test('a rename matches whole path elements', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/ab', '/d/xy')

  assert.equal(renames.resolve('/d/abc', startedAt), '/d/abc')
  assert.equal(renames.resolve('/d/ab', startedAt), '/d/xy')
})

// A name can be vacated and taken again inside one read's flight, and both
// renames are then part of what happened to the path. A record keyed by the
// source would keep only the second, which describes a different file.
test('a name taken again during a read keeps both renames apart', () => {
  const renames = createRenames()
  const startedAt = renames.generation()

  renames.record('/d/a', '/d/b')
  renames.record('/d/a', '/d/c')

  // The file the read opened went with the first rename; the second is about
  // the new file that was created at that name afterwards.
  assert.equal(renames.resolve('/d/a', startedAt), '/d/b')
  assert.equal(renames.resolve('/d/a/child', startedAt), '/d/b/child')
})

test('clear forgets every mapping', () => {
  const renames = createRenames()
  const startedAt = renames.generation()
  renames.record('/d/a.txt', '/d/b.txt')

  renames.clear()

  assert.equal(renames.resolve('/d/a.txt', startedAt), '/d/a.txt')
})
