import test from 'node:test'
import assert from 'node:assert/strict'

import { createPending } from './pending'

test('nothing outstanding means the path is idle', async () => {
  const pending = createPending()
  assert.equal(pending.isPending('/d/a.txt'), false)
  await pending.idle('/d/a.txt')
})

test('a path is pending between its operation starting and its answer', () => {
  const pending = createPending()
  pending.begin('/d/a.txt')
  assert.equal(pending.isPending('/d/a.txt'), true)
  assert.equal(pending.isPending('/d/b.txt'), false)

  pending.end('/d/a.txt')
  assert.equal(pending.isPending('/d/a.txt'), false)
})

// The defect the count exists for. Two operations of one kind can be in flight on
// one path, and with a flag the first to be answered clears the record of the
// second -- which is still outstanding -- so a save that crossed the second is
// recorded as saved, at a path the second may since have vacated.
test('answering one of two operations leaves the path pending', () => {
  const pending = createPending()
  pending.begin('/d/a.txt')
  pending.begin('/d/a.txt')

  pending.end('/d/a.txt')
  assert.equal(pending.isPending('/d/a.txt'), true, 'the first answer cleared the second')

  pending.end('/d/a.txt')
  assert.equal(pending.isPending('/d/a.txt'), false)
})

test('an idle wait resolves only when the last operation is answered', async () => {
  const pending = createPending()
  pending.begin('/d/a.txt')
  pending.begin('/d/a.txt')

  let settled = false
  const idle = pending.idle('/d/a.txt').then(() => {
    settled = true
  })

  pending.end('/d/a.txt')
  await Promise.resolve()
  assert.equal(settled, false, 'the wait resolved with an operation still outstanding')

  pending.end('/d/a.txt')
  await idle
  assert.equal(settled, true)
})

test('an idle wait is not resolved by another path being answered', async () => {
  const pending = createPending()
  pending.begin('/d/a.txt')
  pending.begin('/d/b.txt')

  let settled = false
  const idle = pending.idle('/d/a.txt').then(() => {
    settled = true
  })

  pending.end('/d/b.txt')
  await Promise.resolve()
  assert.equal(settled, false)

  pending.end('/d/a.txt')
  await idle
})

// A wait resolves on the answer that clears the path, so an operation begun
// while the path is still busy is one the wait still covers -- which is the case
// the manager is in: a delete is confirmed while a save of the file is
// travelling, and the tab can be saved again before that one answers.
//
// An operation begun *after* the path has been reported clear is past the wait,
// and this module does not claim otherwise. The manager covers that case from
// its own side: a delete records itself as outstanding before it waits, and a
// save declines to start against an outstanding delete.
test('a second operation begun while the path is busy is waited for', async () => {
  const pending = createPending()
  pending.begin('/d/a.txt')

  let settled = false
  const idle = pending.idle('/d/a.txt').then(() => {
    settled = true
  })

  pending.begin('/d/a.txt')
  pending.end('/d/a.txt')
  await Promise.resolve()
  assert.equal(settled, false, 'the wait resolved with an operation still outstanding')

  pending.end('/d/a.txt')
  await idle
  assert.equal(settled, true)
})

// Each kind of operation is tracked on its own, so a rename in flight says
// nothing about whether a save of the same path is.
test('two trackers do not see one another', () => {
  const renaming = createPending()
  const saving = createPending()

  renaming.begin('/d/a.txt')

  assert.equal(renaming.isPending('/d/a.txt'), true)
  assert.equal(saving.isPending('/d/a.txt'), false)
})

// isPending looks *upwards*, which is the direction its callers ask from: a save
// names a file, and what makes it unsafe to send is an operation on that file or
// on something holding it -- the directory being deleted or renamed out from
// under it.
test('an operation on a directory is reported for what it holds', () => {
  const pending = createPending()
  pending.begin('/d/dir')

  assert.equal(pending.isPending('/d/dir'), true)
  assert.equal(pending.isPending('/d/dir/f.txt'), true, 'a file inside the directory is not covered')
  assert.equal(pending.isPending('/d/dir/sub/f.txt'), true)
  assert.equal(pending.isPending('/d'), false, 'a directory was reported for its own parent')
  assert.equal(pending.isPending('/d/other.txt'), false, 'a sibling was covered')
})

// And the opposite reading of the same record is false, which is the mistake this
// predicate was written with first: an operation on a *file* says nothing about
// the directory holding it, because nothing is being done to that directory.
test('an operation on a file is reported only for that file', () => {
  const pending = createPending()
  pending.begin('/d/dir/f.txt')

  assert.equal(pending.isPending('/d/dir/f.txt'), true)
  assert.equal(pending.isPending('/d/dir'), false, 'a file was reported for the directory holding it')
  assert.equal(pending.isPending('/d/dir/other.txt'), false)
})

// idle looks *downwards*, which is the direction its caller asks from: a delete
// names an entry, and the writes it has to wait for name that entry or what is
// inside it. A delete of a file must not wait on a delete of its directory,
// which is unrelated work that could block it indefinitely.
test('an idle wait on a directory is held open by a write inside it', async () => {
  const pending = createPending()
  pending.begin('/d/dir/f.txt')

  let settled = false
  const idle = pending.idle('/d/dir').then(() => {
    settled = true
  })

  await Promise.resolve()
  assert.equal(settled, false, 'the wait resolved with a write inside the directory outstanding')

  pending.end('/d/dir/f.txt')
  await idle
  assert.equal(settled, true)
})

test('an idle wait on a file does not wait on its directory', async () => {
  const pending = createPending()
  pending.begin('/d/dir')

  // A delete of /d/dir/f.txt names a file. An operation on the directory above it
  // is not a write that delete has to wait for.
  await pending.idle('/d/dir/f.txt')
})

// A prefix is a path element, not a string -- the same rule renames.ts states for
// the same reason -- and both directions have to hold it.
test('a path is a whole element, not the start of one', async () => {
  const pending = createPending()
  pending.begin('/d/abc/f.txt')

  assert.equal(pending.isPending('/d/ab'), false, '/d/ab does not hold /d/abc/f.txt')
  assert.equal(pending.isPending('/d/abcd'), false, '/d/abcd is not /d/abc')
  assert.equal(pending.isPending('/d/abc'), false, 'a file was reported for the directory holding it')
  await pending.idle('/d/ab')
})

// The filesystem root is a path like any other, and the separator must not be
// appended twice when it already ends in one.
test('the filesystem root holds everything below it', async () => {
  const pending = createPending()
  pending.begin('/d/a.txt')

  let settled = false
  const idle = pending.idle('/').then(() => {
    settled = true
  })

  await Promise.resolve()
  assert.equal(settled, false, 'an idle wait on the root resolved with an operation outstanding')

  pending.end('/d/a.txt')
  await idle
  assert.equal(settled, true)
})
