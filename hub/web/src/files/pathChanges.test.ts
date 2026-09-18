import test from 'node:test'
import assert from 'node:assert/strict'

import { createPathChanges } from './pathChanges'

// The defect this exists for, which survived two rounds of review because it only
// lives in an interleaving: a rename recorded when no read was travelling must
// not redirect a read that merely overlapped some *other* rename. Consulting the
// record without its generation makes it a history rather than this read's
// business, and the read lands on a different file under a name nobody opened.
test('a read ignores renames that happened before it started', () => {
  const changes = createPathChanges()
  changes.record('/d/a.txt', '/d/b.txt')

  const startedAt = changes.generation()
  changes.record('/d/c.txt', '/d/e.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/a.txt')
  assert.equal(changes.resolve('/d/c.txt', startedAt), '/d/e.txt')
})

test('a read follows a rename that happened while it was travelling', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/a.txt', '/d/b.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/b.txt')
  assert.equal(changes.resolve('/d/untouched.txt', startedAt), '/d/untouched.txt')
})

// Renames chain, so a read that started before both has to end up at the last
// name -- not the first one it finds.
test('a read follows a chain of renames', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/a.txt', '/d/b.txt')
  changes.record('/d/b.txt', '/d/c.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/c.txt')
})

// A file renamed back to where it started is where it started. The changes are
// applied one at a time, in the order they happened, which is what makes that
// terminate: each is matched against what the path is by the time it is the
// change's turn, not against the name the read started with.
test('a rename back to the original name resolves to the original name', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/a.txt', '/d/b.txt')
  changes.record('/d/b.txt', '/d/a.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/a.txt')
})

test('a rename of a directory carries the paths beneath it', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/old', '/d/new')

  assert.equal(changes.resolve('/d/old/f.txt', startedAt), '/d/new/f.txt')
  assert.equal(changes.resolve('/d/old', startedAt), '/d/new')
})

// A shallow rename and a deeper one can both describe a path, and the order they
// happened in is what decides which one still names it. Picking the deeper match
// regardless would follow a rename of a directory that the earlier shallow rename
// had already carried away.
test('a rename is applied to what the path is by the time it happens', () => {
  // The whole directory moved first, so the later rename of something inside it
  // names a path that is no longer there.
  const shallowFirst = createPathChanges()
  let startedAt = shallowFirst.generation()
  shallowFirst.record('/d/a', '/d/b')
  shallowFirst.record('/d/a/x', '/d/c')
  assert.equal(shallowFirst.resolve('/d/a/x/y', startedAt), '/d/b/x/y')

  // The inner one moved first, carrying the path out of the directory that the
  // later rename then moved.
  const deepFirst = createPathChanges()
  startedAt = deepFirst.generation()
  deepFirst.record('/d/a/x', '/d/c')
  deepFirst.record('/d/a', '/d/b')
  assert.equal(deepFirst.resolve('/d/a/x/y', startedAt), '/d/c/y')
})

// A prefix is a path element, not a string: renaming /d/ab must not carry /d/abc.
test('a rename matches whole path elements', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/ab', '/d/xy')

  assert.equal(changes.resolve('/d/abc', startedAt), '/d/abc')
  assert.equal(changes.resolve('/d/ab', startedAt), '/d/xy')
})

// A name can be vacated and taken again inside one read's flight, and both
// renames are then part of what happened to the path. A record keyed by the
// source would keep only the second, which describes a different file.
test('a name taken again during a read keeps both renames apart', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.record('/d/a', '/d/b')
  changes.record('/d/a', '/d/c')

  // The file the read opened went with the first rename; the second is about
  // the new file that was created at that name afterwards.
  assert.equal(changes.resolve('/d/a', startedAt), '/d/b')
  assert.equal(changes.resolve('/d/a/child', startedAt), '/d/b/child')
})

test('clear forgets every mapping', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()
  changes.record('/d/a.txt', '/d/b.txt')

  changes.clear()

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/a.txt')
})

// A read whose file is deleted while it is travelling resolves to nothing, and
// that is the answer that matters: the hub has already granted the read, so the
// contents come back even though the path is gone, and installing them would
// leave a tab naming a file that no longer exists -- whose save is answered
// not_found with nothing the user can do about it.
test('a read does not resolve to a path a delete removed while it was travelling', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.recordRemoval('/d/a.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), null)
  assert.equal(changes.resolve('/d/untouched.txt', startedAt), '/d/untouched.txt')
})

test('a read ignores a removal that happened before it started', () => {
  const changes = createPathChanges()
  changes.recordRemoval('/d/a.txt')

  const startedAt = changes.generation()

  assert.equal(changes.resolve('/d/a.txt', startedAt), '/d/a.txt')
})

// A deleted directory takes its contents with it, which is the case a read of an
// open file inside it is answered by.
test('a removal covers the entries beneath it', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.recordRemoval('/d/old')

  assert.equal(changes.resolve('/d/old/f.txt', startedAt), null)
  assert.equal(changes.resolve('/d/old', startedAt), null)
  // A prefix is a path element here too: /d/older is not inside /d/old.
  assert.equal(changes.resolve('/d/older', startedAt), '/d/older')
})

// The name may be taken again while the answer is still travelling -- a create,
// or a rename onto it -- and the contents in hand describe whatever the hub
// opened, which is not necessarily what is there now. So nothing revives it: the
// user's next click reads the file that is there.
test('a name taken again after a removal does not revive the read', () => {
  const changes = createPathChanges()
  const startedAt = changes.generation()

  changes.recordRemoval('/d/a.txt')
  changes.record('/d/other.txt', '/d/a.txt')

  assert.equal(changes.resolve('/d/a.txt', startedAt), null)
})
