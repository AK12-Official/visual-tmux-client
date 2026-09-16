// The file manager's own behaviour, mounted in a document.
//
// These are the cases that only exist between components: which editor is on
// screen and which merely exist, and what a save answer means when it arrives
// while a rename of the same file is in flight. The rules those decisions are
// made from live in files/tabs.ts and are tested there directly; what is checked
// here is that the component asks them with the right things, which is what a
// direct test of the rule cannot see.
//
// The editor is CodeMirror's stand-in (test/mocks/codemirror.mjs), which applies
// a change to a document and tells the listener -- enough to type into, and
// enough to see an instance survive.
import test from 'node:test'
import assert from 'node:assert/strict'

import '../../../test/dom.mjs'

import { mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'

import { instances, reset as resetEditors } from '../../../test/mocks/codemirror.mjs'
import { setToken } from '../../api'
import FileManagerOverlay from './FileManagerOverlay.vue'

type Responder = () => Response | Promise<Response>

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function text(body: string): Response {
  return new Response(body, {
    status: 200,
    headers: {
      'X-File-Size': String(body.length),
      'X-File-Mtime': '1000',
      'X-File-Mtime-Nanos': '1000000000',
    },
  })
}

interface Call {
  url: string
  method: string
  body: string
}

/**
 * hubFetch answers the calls the manager makes. The write and rename routes are
 * supplied by the test, because their *order* is what the interesting cases are
 * about, and a handler that returns a promise the test resolves is how that
 * order is chosen.
 */
function hubFetch(overrides: Partial<Record<'write' | 'rename' | 'probe', Responder>> = {}): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    calls.push({ url, method, body: typeof init?.body === 'string' ? init.body : '' })

    if (url.includes('/working-directory')) {
      return json({ path: '/srv/work', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({
        path: '/srv/work',
        entries: [
          { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'b.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'notes.dat', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      })
    }
    if (url.includes('/files/read')) {
      // The probe is the same route asked with HEAD, which is how the manager
      // asks what a file is without transferring it.
      if (method === 'HEAD' && overrides.probe) return overrides.probe()
      return text('hello')
    }
    if (url.includes('/files/write')) {
      return overrides.write ? overrides.write() : json({ mtime: 2000, mtime_nanos: '2000000000' })
    }
    if (url.includes('/files/rename')) {
      return overrides.rename ? overrides.rename() : new Response(null, { status: 204 })
    }
    throw new Error(`the manager asked for something unexpected: ${method} ${url}`)
  }) as typeof fetch
  return calls
}

/** flush lets every pending promise settle, including a render they schedule. */
async function flush(rounds = 6) {
  for (let round = 0; round < rounds; round++) {
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

/** deferred is a promise a test resolves when it wants the request to land. */
function deferred() {
  let release: (response: Response) => void = () => {}
  const promise = new Promise<Response>((resolve) => {
    release = resolve
  })
  return { respond: () => promise, release }
}

async function mountManager(): Promise<VueWrapper> {
  resetEditors()
  setToken('tok')
  const wrapper = mount(FileManagerOverlay, { props: { session: 'work' } })
  await flush()
  return wrapper
}

/** row finds the tree line for an entry. */
function row(wrapper: VueWrapper, name: string) {
  const found = wrapper.findAll('.tree__row').find((candidate) => candidate.text().includes(name))
  assert.ok(found, `no tree row for ${name}`)
  return found
}

async function openFile(wrapper: VueWrapper, name: string) {
  await row(wrapper, name).find('.tree__label').trigger('click')
  await flush()
}

/** renameViaMenu renames an entry the way the user does: the menu, then the
 * prompt, which is stubbed because a test cannot answer one. */
async function renameViaMenu(wrapper: VueWrapper, name: string, to: string) {
  const original = window.prompt
  window.prompt = () => to
  try {
    await row(wrapper, name).find('.tree__label').trigger('contextmenu')
    await flush(2)
    const action = wrapper.findAll('.menu__item').find((item) => item.text() === 'Rename')
    assert.ok(action, 'the context menu offered no rename')
    await action.trigger('click')
  } finally {
    window.prompt = original
  }
}

async function saveButton(wrapper: VueWrapper) {
  const button = wrapper.findAll('.fm__btn').find((candidate) => candidate.text() === 'Save')
  assert.ok(button, 'no Save button')
  return button
}

test('every open file keeps an editor, and only the active one is shown', async () => {
  hubFetch()
  const wrapper = await mountManager()

  await openFile(wrapper, 'a.txt')
  await openFile(wrapper, 'b.txt')

  const editors = wrapper.findAll('.fm__editor')
  assert.equal(editors.length, 2, 'a mounted editor per open file')
  assert.equal(editors[0].isVisible(), false, 'the file switched away from is hidden')
  assert.equal(editors[1].isVisible(), true, 'the active file is the one on screen')
  // The point of keeping them mounted: destroying an editor is what takes its
  // undo history with it, and the history a user reaches for belongs to the file
  // they just switched away from.
  assert.equal(instances.length, 2)
  assert.ok(
    instances.every((view) => !view.destroyed),
    'switching files destroyed an editor',
  )
  wrapper.unmount()
})

test('a save is recorded once it is the answer to what the tab holds', async () => {
  hubFetch()
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')

  instances[0].dispatch({ changes: { from: 5, to: 5, insert: ' edited' } })
  await flush()
  const save = await saveButton(wrapper)
  assert.equal(save.attributes('disabled'), undefined, 'typing must enable Save')

  await save.trigger('click')
  await flush()

  assert.equal(wrapper.find('.fm__dirty').exists(), false, 'the tab reports itself saved')
  wrapper.unmount()
})

test('an answer that crossed a rename is not recorded as a save', async () => {
  const write = deferred()
  const rename = deferred()
  hubFetch({ write: write.respond, rename: rename.respond })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')

  instances[0].dispatch({ changes: { from: 5, to: 5, insert: ' edited' } })
  await flush()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  // The user renames the file while the write is still travelling.
  await renameViaMenu(wrapper, 'a.txt', 'b.txt')
  await flush(2)

  // The write answers first, while the rename is still outstanding: the two
  // crossed, and the browser cannot tell whether the write landed before the
  // entry moved. Recording it as saved would report the edit as stored at a name
  // nothing was written to.
  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(wrapper.find('.fm__dirty').exists(), true, 'the tab must stay modified')
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('renamed while it was saved')),
    `expected a notice about the rename, got ${JSON.stringify(notices)}`,
  )

  rename.release(new Response(null, { status: 204 }))
  await flush()
  wrapper.unmount()
})

test('an answer for a file that has moved is not recorded either', async () => {
  const write = deferred()
  hubFetch({ write: write.respond })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')

  instances[0].dispatch({ changes: { from: 5, to: 5, insert: ' edited' } })
  await flush()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  // This time the rename lands first, so the tab is at the new name before the
  // write answers -- the case the path in the save request exists to catch.
  await renameViaMenu(wrapper, 'a.txt', 'b.txt')
  await flush()

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(wrapper.find('.fm__dirty').exists(), true, 'the tab must stay modified')
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('moved to /srv/work/b.txt')),
    `expected a notice naming the new path, got ${JSON.stringify(notices)}`,
  )
  wrapper.unmount()
})

test('a file the name calls binary is opened when the hub reads it as text', async () => {
  const calls = hubFetch()
  const wrapper = await mountManager()

  await openFile(wrapper, 'notes.dat')

  // The hub was asked -- that question is what makes the name a guess rather
  // than a decision -- and its answer put the file in the editor.
  assert.ok(
    calls.some((call) => call.method === 'HEAD'),
    'the hub was never asked what the file is before it was refused',
  )
  assert.equal(
    wrapper.find('.fm__editor').exists(),
    true,
    'text the name called binary did not open in the editor',
  )
  wrapper.unmount()
})

test('a rename that lands during the probe leaves one tab, at the new name', async () => {
  const probe = deferred()
  hubFetch({ probe: probe.respond })
  const wrapper = await mountManager()

  // The click starts a question the test holds open.
  await row(wrapper, 'notes.dat').find('.tree__label').trigger('click')
  await flush(2)

  // The user renames the file while it is in flight.
  await renameViaMenu(wrapper, 'notes.dat', 'renamed.dat')
  await flush(2)

  // The hub answers that it is binary, which is the branch that builds the tab
  // from the probe rather than reading the file -- and so the branch whose path
  // has to be corrected for a rename.
  probe.release(
    new Response(null, {
      status: 200,
      headers: { 'X-File-Size': '5', 'X-File-Binary': '1' },
    }),
  )
  await flush()

  // Asking the hub is an await, so a rename can complete inside it exactly as it
  // can inside a read. A tab built at the old name would name a file that is
  // gone, and a click on the new name would open a second tab for one file.
  const tabs = wrapper.findAll('.tabs__tab')
  assert.equal(tabs.length, 1, `expected one tab, got ${tabs.length}`)
  assert.ok(
    tabs[0].text().includes('renamed.dat'),
    `expected the tab at the new name, got ${tabs[0].text()}`,
  )
  wrapper.unmount()
})

test('closing a tab releases its editor, and switching between them does not', async () => {
  hubFetch()
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await openFile(wrapper, 'b.txt')

  // Switching back is not a release: this is what makes the assertion below mean
  // something.
  await wrapper.findAll('.tabs__label')[0].trigger('click')
  await flush(2)
  assert.ok(
    instances.every((view) => !view.destroyed),
    'switching between open files destroyed an editor',
  )

  // Closing the tab that is on screen is a release.
  await wrapper.findAll('.tabs__close')[1].trigger('click')
  await flush(2)
  assert.equal(
    instances.filter((view) => view.destroyed).length,
    1,
    'closing a tab did not release exactly its own editor',
  )
  assert.equal(wrapper.findAll('.fm__editor').length, 1)
  wrapper.unmount()
})
