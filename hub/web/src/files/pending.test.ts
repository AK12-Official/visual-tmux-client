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

// A path covers what is beneath it, because that is the relationship the manager
// has to reason about: a delete or a rename names an entry, and the writes that
// cross it name what is inside. Answering only for the exact path is what let a
// delete of a directory go out while a write inside it was still travelling.
test('a path covers everything beneath it', () => {
  const pending = createPending()
  pending.begin('/d/dir/f.txt')

  assert.equal(pending.isPending('/d/dir/f.txt'), true)
  assert.equal(pending.isPending('/d/dir'), true, 'the directory holding the write is not covered by it')
  assert.equal(pending.isPending('/d'), true, 'an ancestor of the directory is not covered either')
  assert.equal(pending.isPending('/d/other'), false, 'a sibling was covered')
  assert.equal(pending.isPending('/d/dir/g.txt'), false, 'another entry in the directory was covered')
})

// Which is the whole of the reason a delete can still find a write whose tab has
// been closed: the record is keyed by the path the write named, and the entry
// being deleted is found by walking up from it rather than by remembering it.
test('an idle wait on a directory is resolved only by what is under it', async () => {
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

// A prefix is a path element, not a string -- the same rule renames.ts states for
// the same reason.
test('a path does not cover a sibling whose name starts the same way', () => {
  const pending = createPending()
  pending.begin('/d/abc/f.txt')

  assert.equal(pending.isPending('/d/abc'), true)
  assert.equal(pending.isPending('/d/ab'), false, '/d/ab is not the directory holding /d/abc/f.txt')
  assert.equal(pending.isPending('/d/abcd'), false)
})

// The filesystem root is a path like any other, and the separator must not be
// appended twice when it already ends in one.
test('the filesystem root covers everything under it', () => {
  const pending = createPending()
  pending.begin('/d/a.txt')

  assert.equal(pending.isPending('/'), true)
})
