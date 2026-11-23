import test from 'node:test'
import assert from 'node:assert/strict'
import { prepareDesktopQuit } from './desktop-lifecycle'

test('cancel preserves edits and does not flush or approve quit', async () => {
  let flushed = false
  assert.equal(await prepareDesktopQuit(() => false, async () => { flushed = true }), false)
  assert.equal(flushed, false)
})

test('approval waits for durable preferences; failed or stalled flush keeps application open', async () => {
  let release!: () => void
  let approved = false
  const result = prepareDesktopQuit(() => true, () => new Promise<void>(resolve => { release = resolve }))
    .then(value => { approved = value; return value })
  await Promise.resolve()
  assert.equal(approved, false)
  release()
  assert.equal(await result, true)
  assert.equal(await prepareDesktopQuit(() => true, async () => { throw new Error('disk full') }), false)
  assert.equal(await prepareDesktopQuit(() => true, () => new Promise(() => {}), 5), false)
})
