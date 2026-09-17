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

import { EditorView, instances, reset as resetEditors } from '../../../test/mocks/codemirror.mjs'
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
function hubFetch(
  overrides: Partial<{
    write: Responder
    rename: Responder
    delete: Responder
    probe: Responder
    // The read route answers both the manager's reads and the image preview's
    // fetch of the same bytes, so this one is told which path was asked for.
    read: (url: string) => Response | Promise<Response>
    // The listing is asked about different directories, so its override is given
    // the path it was asked for rather than being a fixed answer.
    list: (path: string) => Response | Promise<Response>
  }> = {},
): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    calls.push({ url, method, body: typeof init?.body === 'string' ? init.body : '' })

    if (url.includes('/working-directory')) {
      return json({ path: '/srv/work', substituted: false })
    }
    if (url.includes('/files/list')) {
      if (overrides.list) {
        return overrides.list(new URL(url, 'http://hub').searchParams.get('path') ?? '')
      }
      return json({
        path: '/srv/work',
        entries: [
          { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'b.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'notes.dat', is_dir: false, size: 5, mtime: 1000 },
          { name: 'photo.png', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      })
    }
    if (url.includes('/files/read')) {
      // The probe is the same route asked with HEAD, which is how the manager
      // asks what a file is without transferring it.
      if (method === 'HEAD' && overrides.probe) return overrides.probe()
      if (overrides.read) return overrides.read(url)
      return text('hello')
    }
    if (url.includes('/files/write')) {
      return overrides.write ? overrides.write() : json({ mtime: 2000, mtime_nanos: '2000000000' })
    }
    if (url.includes('/files/rename')) {
      return overrides.rename ? overrides.rename() : new Response(null, { status: 204 })
    }
    if (url.includes('/files/delete')) {
      return overrides.delete ? overrides.delete() : new Response(null, { status: 204 })
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

/** deleteViaMenu deletes an entry the way the user does: the menu, then the
 * confirmation, which is stubbed because a test cannot answer a dialog. */
async function deleteViaMenu(wrapper: VueWrapper, name: string) {
  const original = window.confirm
  window.confirm = () => true
  try {
    await row(wrapper, name).find('.tree__label').trigger('contextmenu')
    await flush(2)
    const action = wrapper.findAll('.menu__item').find((item) => item.text() === 'Delete')
    assert.ok(action, 'the context menu offered no delete')
    await action.trigger('click')
  } finally {
    window.confirm = original
  }
}

/** closeTab closes the open tab, answering the unsaved-changes prompt. */
async function closeTab(wrapper: VueWrapper, index = 0) {
  const original = window.confirm
  window.confirm = () => true
  try {
    await wrapper.findAll('.tabs__close')[index].trigger('click')
    await flush(2)
  } finally {
    window.confirm = original
  }
}

/** goUp navigates to the parent directory through the header button. */
async function goUp(wrapper: VueWrapper) {
  const button = wrapper.findAll('.fm__btn').find((candidate) => candidate.text() === '↑')
  assert.ok(button, 'no parent-directory button')
  await button.trigger('click')
  await flush()
}

/** type makes the file in the only open editor differ from what was read, which
 * is what makes Save both enabled and worth pressing. */
async function type(inserted = ' edited') {
  const view = instances[instances.length - 1]
  view.dispatch({ changes: { from: view.doc.length, to: view.doc.length, insert: inserted } })
  await flush()
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

// Replacing a file replaces the inode, so a target reachable under other names
// loses them: every other name keeps the contents it had. The hub refuses that
// until the caller says the user knows, and this is where the user is asked. The
// retry has to be the same write -- the same captured contents against the same
// observation -- with the agreement added, or answering the question would write
// something the user did not ask for.
test('a save refused for other names asks, and the retry saves what the tab holds', async () => {
  // The first write is held open, so the user can type while it travels -- which
  // is the state the retry has to be right about.
  const firstWrite = deferred()
  let writes = 0
  const calls = hubFetch({
    write: () => {
      writes += 1
      if (writes === 1) return firstWrite.respond()
      return json({ mtime: 2000, mtime_nanos: '2000000000' })
    },
  })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type('A')

  await (await saveButton(wrapper)).trigger('click')
  await flush(2)
  await type('B')

  const asked: string[] = []
  const original = window.confirm
  window.confirm = (message?: string) => {
    asked.push(String(message))
    return true
  }
  try {
    firstWrite.release(
      new Response(JSON.stringify({ error: 'other_names' }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    await flush(3)
  } finally {
    window.confirm = original
  }

  assert.equal(asked.length, 1, 'the user was not asked before the other names were dropped')
  assert.match(asked[0], /other names/)

  const sent = calls.filter((call) => call.url.includes('/files/write'))
  assert.equal(sent.length, 2, 'the save was not retried after the user agreed')
  const first = new URL(sent[0].url, 'http://hub')
  const retry = new URL(sent[1].url, 'http://hub')
  assert.equal(first.searchParams.has('allow_other_names'), false)
  assert.equal(retry.searchParams.get('allow_other_names'), '1')
  assert.equal(retry.searchParams.get('expected_mtime'), first.searchParams.get('expected_mtime'))
  // What the retry writes is what the editor holds when the answer arrives -- the
  // keystrokes typed while the refused write travelled are part of it -- and the
  // observation it is checked against is the one the tab still holds. It is a save
  // of the tab as it stands with the agreement added, not a replay of the request
  // that was refused.
  assert.equal(sent[1].body, 'helloAB', 'the retry did not carry what the editor holds')
  assert.equal(wrapper.find('.fm__dirty').exists(), false, 'the agreed save was not recorded')
  wrapper.unmount()
})

// A linked file can also have changed on disk since it was read, and then the two
// questions are asked one after the other. The second retry has to carry what the
// first answer was: asking again for an agreement the user has already given is
// the manager forgetting an answer, and the write it eventually sends has to be
// one all of those answers describe.
test('a linked file that also conflicted keeps both answers through the retries', async () => {
  let writes = 0
  const calls = hubFetch({
    write: () => {
      writes += 1
      if (writes === 1) {
        return new Response(JSON.stringify({ error: 'other_names' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (writes === 2) {
        return new Response(JSON.stringify({ error: 'conflict' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return json({ mtime: 2000, mtime_nanos: '2000000000' })
    },
  })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()

  const asked: string[] = []
  const original = window.confirm
  window.confirm = (message?: string) => {
    asked.push(String(message))
    return true
  }
  try {
    await (await saveButton(wrapper)).trigger('click')
    await flush(2)
  } finally {
    window.confirm = original
  }

  assert.equal(asked.length, 2, `expected both questions, got ${JSON.stringify(asked)}`)
  assert.match(asked[0], /other names/)
  assert.match(asked[1], /changed on disk/)

  const sent = calls.filter((call) => call.url.includes('/files/write'))
  assert.equal(sent.length, 3, 'expected a retry for each answer')
  const last = new URL(sent[2].url, 'http://hub')
  assert.equal(last.searchParams.get('allow_other_names'), '1', 'the second retry forgot the first answer')
  assert.equal(last.searchParams.has('expected_mtime'), false, 'the overwrite was not forced')
  assert.equal(wrapper.find('.fm__dirty').exists(), false, 'the agreed save was not recorded')
  wrapper.unmount()
})

test('a save refused for other names is not retried when the user declines', async () => {
  let writes = 0
  const calls = hubFetch({
    write: () => {
      writes += 1
      return new Response(JSON.stringify({ error: 'other_names' }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      })
    },
  })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()

  let asked = 0
  const original = window.confirm
  window.confirm = () => {
    asked += 1
    return false
  }
  try {
    await (await saveButton(wrapper)).trigger('click')
    await flush(2)
  } finally {
    window.confirm = original
  }

  assert.equal(asked, 1, 'the user was not asked')
  assert.equal(calls.filter((call) => call.url.includes('/files/write')).length, 1)
  assert.equal(writes, 1)
  assert.equal(
    wrapper.find('.fm__dirty').exists(),
    true,
    'a declined save must leave the tab modified',
  )
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
    notices.some((text) => String(text).includes('was in flight while it was saved')),
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

test('an image that turns out to be over the bound is presented as information', async () => {
  // The listing said this was a small image; the bytes say otherwise. The bound
  // is applied to what the hub reports when the file is read, and the size the
  // tab then carries is what makes it present as information with a download
  // action rather than as a frame nothing can render.
  hubFetch({
    read: () => new Response(null, { status: 200, headers: { 'X-File-Size': '9000000' } }),
  })
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush()

  assert.equal(wrapper.find('.image-preview').exists(), false, 'the image was rendered anyway')
  assert.equal(wrapper.find('.fm__info').exists(), true, 'no information panel was offered')
  // And the panel gives the reason this file is not shown, which is the bound --
  // not the fact that nobody read it, which is true of every image and would be
  // a non-answer here.
  assert.match(wrapper.find('.fm__info').text(), /binary or too large to preview/)
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('larger than this view renders')),
    `expected a notice explaining the bound, got ${JSON.stringify(notices)}`,
  )
  wrapper.unmount()
})

// The manager does not let the deletes it sends race the saves it sends. The
// hub's last check and the rename that installs a write are two adjacent system
// calls, so a write landing between them puts the file back -- while the delete's
// own answer says the file is gone, and the tree is redrawn to show it gone.
//
// Nothing can order another process's write against this delete. What the
// manager can do is refuse to order its own that way, and this is that refusal:
// the request is not sent until the write it would cross has been answered.
test('a delete is not sent while a save of the same file is travelling', async () => {
  const write = deferred()
  const calls = hubFetch({ write: write.respond })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  await deleteViaMenu(wrapper, 'a.txt')
  await flush(2)

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    false,
    'the delete overtook the write it would have crossed',
  )

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    true,
    'the delete never went out once the write had answered',
  )
  wrapper.unmount()
})

// The other direction of the same rule. A write started after the delete was
// sent cannot be waited for -- it did not exist when the delete looked -- so it
// is refused instead, which is the only ordering left that cannot put the file
// back.
test('a save is refused while the delete of that file is in flight', async () => {
  const remove = deferred()
  const calls = hubFetch({ delete: remove.respond })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()

  await deleteViaMenu(wrapper, 'a.txt')
  await flush(2)

  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  assert.equal(
    calls.some((call) => call.url.includes('/files/write')),
    false,
    'a write was sent at a file that is being deleted',
  )
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('A delete of a.txt or of a directory above it')),
    `expected a notice that a delete is in flight, got ${JSON.stringify(notices)}`,
  )

  remove.release(new Response(null, { status: 204 }))
  await flush()
  wrapper.unmount()
})

// Two renames of one path can be in flight at once, and the manager does not
// stop the user asking for the second: until the first is answered, the entry is
// still listed under the name they are renaming.
//
// Which makes the record of "a rename is outstanding for this path" a count, not
// a flag. With a flag, the *failing* second rename clears the record of the
// first -- which is still travelling -- and the write that answers next is
// recorded as saved against a path the first rename is at that moment moving out
// from under it.
test('one of two renames being answered does not clear the other', async () => {
  const write = deferred()
  const first = deferred()
  const answers: Responder[] = [
    () => first.respond(),
    () => new Response(JSON.stringify({ error: 'conflict' }), { status: 409 }),
  ]
  hubFetch({ write: write.respond, rename: () => answers.shift()!() })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  await renameViaMenu(wrapper, 'a.txt', 'b.txt')
  await flush(2)
  await renameViaMenu(wrapper, 'a.txt', 'c.txt')
  await flush(2)

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(
    wrapper.find('.fm__dirty').exists(),
    true,
    'the write was recorded as saved while a rename of that path was outstanding',
  )
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('was in flight while it was saved')),
    `expected a notice about the crossing rename, got ${JSON.stringify(notices)}`,
  )

  first.release(new Response(null, { status: 204 }))
  await flush()
  wrapper.unmount()
})

// The same bound, in the other direction, and the one that was wrong. The
// listing is stale and calls this image far too large to render, so the manager
// asks the hub what the file is before presenting it -- and every image is
// binary, so that is what comes back, together with a size that is now well
// under the bound. Treating the classification as the answer to "may this be
// previewed?" is what showed the file's details instead of the picture, for a
// file that had simply shrunk since the listing was read.
test('an image that has shrunk below the bound is previewed as an image', async () => {
  hubFetch({
    list: () =>
      json({
        path: '/srv/work',
        entries: [{ name: 'photo.png', is_dir: false, size: 9000000, mtime: 1000 }],
        truncated: false,
      }),
    probe: () =>
      new Response(null, {
        status: 200,
        headers: { 'X-File-Size': '2048', 'X-File-Binary': '1' },
      }),
  })
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush()

  assert.equal(
    wrapper.find('.fm__info').exists(),
    false,
    'a previewable image was presented as information instead',
  )
  assert.equal(wrapper.find('.image-preview').exists(), true, 'the image was never rendered')
  wrapper.unmount()
})

// The wait is by *path*, and this is what that buys. A write outlives the tab it
// came from: saving a file and then closing that tab leaves the write travelling
// with no tab to be found by, so a delete that looked for the tabs it is about to
// close would find none and go out immediately -- into the very window the wait
// exists to close. The record is keyed by the path the write named, which is why
// the path is what asks.
test('a delete waits for a write whose tab has already been closed', async () => {
  const write = deferred()
  const calls = hubFetch({ write: write.respond })
  const wrapper = await mountManager()
  await openFile(wrapper, 'a.txt')
  await type()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  // The user gives up on the file and closes the tab, answering the prompt about
  // unsaved changes. The tab is gone; the write is not.
  await closeTab(wrapper)
  assert.equal(wrapper.findAll('.tabs__tab').length, 0)

  await deleteViaMenu(wrapper, 'a.txt')
  await flush(2)

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    false,
    'the delete overtook a write with no tab left to find it by',
  )

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    true,
    'the delete never went out once the write had answered',
  )
  wrapper.unmount()
})

// The same rule across a directory: a delete names the directory, and the write
// it has to wait for names something inside it. The paths do not match, which is
// the second half of why the record covers a subtree rather than one path.
test('a delete of a directory waits for a write inside it', async () => {
  const write = deferred()
  const calls = hubFetch({
    list: (path) =>
      path === '/srv/work/dir'
        ? json({
            path,
            entries: [{ name: 'f.txt', is_dir: false, size: 5, mtime: 1000 }],
            truncated: false,
          })
        : json({
            path,
            entries: [{ name: 'dir', is_dir: true, size: 0, mtime: 1000 }],
            truncated: false,
          }),
    write: write.respond,
  })
  const wrapper = await mountManager()

  // Into the directory, open the file in it, and start a save.
  await row(wrapper, 'dir').find('.tree__label').trigger('click')
  await flush()
  await openFile(wrapper, 'f.txt')
  await type()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  // Back up to where the directory can be right-clicked, and lose the tab on the
  // way -- which is the point: with no tab naming the file, the only thing left
  // that can find the write is the directory's own path.
  await closeTab(wrapper)
  await goUp(wrapper)
  await deleteViaMenu(wrapper, 'dir')
  await flush(2)

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    false,
    'the delete of the directory overtook a write inside it',
  )

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush()

  assert.equal(
    calls.some((call) => call.url.includes('/files/delete')),
    true,
    'the delete of the directory never went out',
  )
  wrapper.unmount()
})

// A name is a guess about contents, and this is the guess being wrong: a file
// named like an image whose bytes are not one this browser can decode -- a
// truncated download, a pointer file from a large-file store, something
// encrypted. The fetch succeeds, so nothing in the preview's own error handling
// fires; only rendering fails, and without this the user is left looking at an
// empty frame with no way to get the file.
test('an image that cannot be decoded offers the file instead of an empty frame', async () => {
  const calls = hubFetch()
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush()

  // The response is not a decodable image, and jsdom raises the same event a
  // browser would when the blob cannot be read as one.
  const image = wrapper.find('.image-preview__img')
  assert.ok(image.exists(), 'the image element was never rendered, so nothing could fail')
  await image.trigger('error')
  await flush()

  assert.equal(
    wrapper.find('.image-preview__failed').exists(),
    true,
    'a decode failure left the empty frame in place',
  )
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('could not be rendered as an image')),
    `expected a notice explaining the failure, got ${JSON.stringify(notices)}`,
  )

  // And the download action is the way out, which is what the information panel
  // would have offered had the file not been presented as an image.
  const download = wrapper
    .findAll('.image-preview__download')
    .find((candidate) => candidate.text() === 'Download')
  assert.ok(download, 'no download action was offered')
  await download.trigger('click')
  await flush()
  assert.ok(
    calls.some((call) => call.url.includes('/files/download')),
    'the download action did not ask the hub for the file',
  )
  wrapper.unmount()
})

// The other half of the subtree rule, at the call site rather than in the
// predicate: with a delete of the directory outstanding, a save of a file inside
// it is refused. The two paths do not match, which is the point -- and the tab is
// still open, so this is the refusal rather than the wait.
test('a save inside a directory being deleted is refused', async () => {
  const remove = deferred()
  const calls = hubFetch({
    list: (path) =>
      path === '/srv/work/dir'
        ? json({
            path,
            entries: [{ name: 'f.txt', is_dir: false, size: 5, mtime: 1000 }],
            truncated: false,
          })
        : json({
            path,
            entries: [{ name: 'dir', is_dir: true, size: 0, mtime: 1000 }],
            truncated: false,
          }),
    delete: remove.respond,
  })
  const wrapper = await mountManager()

  await row(wrapper, 'dir').find('.tree__label').trigger('click')
  await flush()
  await openFile(wrapper, 'f.txt')
  await type()

  await goUp(wrapper)
  await deleteViaMenu(wrapper, 'dir')
  await flush(2)

  // The tab is still open on a file inside the directory whose delete is in
  // flight, so the save is refused rather than sent into that delete.
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)

  assert.equal(
    calls.some((call) => call.url.includes('/files/write')),
    false,
    'a write was sent for a file inside a directory being deleted',
  )
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('A delete of f.txt or of a directory above it')),
    `expected a notice that a delete is in flight, got ${JSON.stringify(notices)}`,
  )

  remove.release(new Response(null, { status: 204 }))
  await flush()
  wrapper.unmount()
})

// A decode event belongs to the load that raised it. Leaving one file for another
// revokes the first blob to make room, which aborts its decode -- and the browser
// then raises `error` for a URL this component has already let go of. The revoke
// queues that event ahead of the next fetch, so it usually arrives while the new
// file is still loading: read without a ticket, it marks the *next* file
// undecodable, names it in a warning, and hides the image that would have
// rendered.
test('a decode failure for a file that is gone is not read against the next one', async () => {
  const second = deferred()
  const reads: Responder[] = [() => text('hello'), () => second.respond()]
  hubFetch({
    list: (path) =>
      json({
        path,
        entries: [
          { name: 'photo.png', is_dir: false, size: 5, mtime: 1000 },
          { name: 'other.png', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      }),
    read: () => reads.shift()!(),
  })
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush()
  const stale = wrapper.find('.image-preview__img').element
  assert.ok(stale, 'the first image was never rendered, so nothing could go stale')

  // Leaving for the second file revokes the first one's URL and starts a load
  // that has not answered yet.
  await openFile(wrapper, 'other.png')
  await flush()
  assert.equal(wrapper.find('.image-preview__img').exists(), false, 'the second file resolved too early')

  stale.dispatchEvent(new Event('error'))
  await flush()

  assert.equal(
    wrapper.find('.image-preview__failed').exists(),
    false,
    'a stale decode failure was reported against the file that is loading',
  )
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    !notices.some((text) => String(text).includes('could not be rendered as an image')),
    `a stale decode failure was reported to the user: ${JSON.stringify(notices)}`,
  )

  // And the second half of the same race: the stale event arriving *after* the
  // next file has rendered. By then the ticket cannot tell the two apart -- the
  // live load is the one it names -- so what distinguishes them is which element
  // raised it, which is why the image is keyed on its own URL.
  second.release(text('hello'))
  await flush()
  assert.ok(wrapper.find('.image-preview__img').exists(), 'the second image never rendered')

  stale.dispatchEvent(new Event('error'))
  await flush()

  assert.equal(
    wrapper.find('.image-preview__failed').exists(),
    false,
    'a stale decode failure replaced the file that is on screen',
  )
  assert.ok(
    wrapper.find('.image-preview__img').exists(),
    'the image that rendered was replaced by a failure panel',
  )
  wrapper.unmount()
})

// The editor stand-in has to model the editor's change set rather than a
// plausible-looking one: a test written against a stand-in that composes
// positions the wrong way is a green test over a document no user could produce.
// A file opened as an image is never read: the preview fetches what it needs,
// and its bytes are never decoded as text. The *name* is what made that sound,
// and a rename replaces the name -- the file is the same file, and what it now
// claims to be is a guess nothing has checked. Handing the editor an empty
// document under the new name would put a Save button in front of the picture.
test('a renamed image is read, rather than offered as an empty editor', async () => {
  // The first read is the preview fetching the image for itself; the second is
  // the one the rename starts, and the test holds it open.
  const afterRename = deferred()
  let reads = 0
  hubFetch({
    read: () => {
      reads += 1
      return reads === 1 ? text('PNGDATA') : afterRename.respond()
    },
  })
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush(2)
  assert.equal(
    wrapper.find('.image-preview__img').exists(),
    true,
    'the picture should have been previewed, not read',
  )

  await renameViaMenu(wrapper, 'photo.png', 'photo.txt')
  await flush(2)

  // Until that read answers, the tab is information about the file: the editor
  // is never handed a document nobody has read, whether the read is travelling
  // or fails.
  assert.equal(
    wrapper.findAll('.fm__editor').length,
    0,
    'the editor was offered for a file nobody had read',
  )
  assert.match(wrapper.find('.fm__info').text(), /have not been read/)

  afterRename.release(text('hello'))
  await flush()

  // And once it answers, what the hub read is what the tab holds -- which is
  // what a save would write.
  const editors = wrapper.findAll('.fm__editor')
  assert.equal(editors.length, 1, 'the renamed tab was not read')
  assert.equal(instances[instances.length - 1].doc, 'hello')
  wrapper.unmount()
})

// A rename to a name the editor would hold starts a read; a rename to a name it
// would not -- a binary extension -- does not, because the editor is not what
// such a name asks for. The tab is left holding nothing, and the panel has to say
// that rather than describe the file: nobody has read it, so "binary" would be a
// claim the manager has no basis for, and its size is not the reason either.
test('a file renamed to a name nobody read is described as unread', async () => {
  hubFetch()
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush(2)
  await renameViaMenu(wrapper, 'photo.png', 'archive.zip')
  await flush(2)

  assert.equal(
    wrapper.findAll('.fm__editor').length,
    0,
    'the editor was offered for a file nobody had read',
  )
  const panel = wrapper.find('.fm__info').text()
  assert.match(panel, /have not been read/)
  assert.doesNotMatch(panel, /binary or too large/, 'the panel described a file nothing had read')
  wrapper.unmount()
})

// Two reads of one path can be in flight, because a tab can be renamed away from
// a text name and back to it: the first answer to land is what makes the tab
// editable, and a later one was asked for a tab that no longer exists in that
// state. Writing it anyway would take the user's keystrokes with it and leave the
// tab reporting itself saved against contents they never saw.
test('a second read of a renamed tab does not overwrite what the user typed', async () => {
  const reads: { respond: () => Promise<Response>; release: (res: Response) => void }[] = []
  hubFetch({
    // Both names are listed, so the test can rename the entry either way. A real
    // hub would list whichever one exists; what this stands in for is a listing
    // that is re-read after each rename the manager performs.
    list: (path) =>
      json({
        path,
        entries: [
          { name: 'photo.png', is_dir: false, size: 5, mtime: 1000 },
          { name: 'photo.txt', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      }),
    read: (url) => {
      // The image preview fetches its own bytes whenever the tab shows the
      // picture again; the reads this test is about are the ones a rename starts,
      // and those name the text path.
      if (url.includes('photo.png')) return text('PNGDATA')
      const wait = deferred()
      reads.push(wait)
      return wait.respond()
    },
  })
  const wrapper = await mountManager()

  await openFile(wrapper, 'photo.png')
  await flush(2)
  await renameViaMenu(wrapper, 'photo.png', 'photo.txt')
  await flush(2)
  // Away from the text name and back: nothing is read for an image name, and the
  // tab is still unread when the name is a text one again -- so this second rename
  // starts a second read of the *same* path.
  await renameViaMenu(wrapper, 'photo.txt', 'photo.png')
  await flush(2)
  await renameViaMenu(wrapper, 'photo.png', 'photo.txt')
  await flush(2)
  assert.equal(reads.length, 2, 'expected two reads of the renamed tab')

  // The first answer lands and makes the tab editable, and the user types.
  reads[0].release(text('hello'))
  await flush()
  await type(' typed')
  assert.equal(wrapper.find('.fm__dirty').exists(), true, 'the keystroke should have made it dirty')

  // The second answer describes the tab as it was when that read was asked for,
  // and must leave what is in it now alone.
  reads[1].release(text('other'))
  await flush()
  assert.equal(instances[instances.length - 1].doc, 'hello typed')
  assert.equal(
    wrapper.find('.fm__dirty').exists(),
    true,
    'the tab reported itself saved against contents the user never saw',
  )
  wrapper.unmount()
})

// A read the hub has already granted goes on returning the file's contents after
// the file is deleted, and it can land after the delete has swept the tabs it
// found. That sweep is what removes the tabs that were open when the delete was
// answered; this is the answer arriving behind it, which would install a tab
// naming a path that is gone -- and whose save is answered not_found, with
// nothing in the interface to get out of it with.
test('a read that lands after the delete of its file installs no tab', async () => {
  const read = deferred()
  hubFetch({ read: read.respond })
  const wrapper = await mountManager()

  // The click starts a read the test holds open.
  await row(wrapper, 'a.txt').find('.tree__label').trigger('click')
  await flush(2)

  // The user deletes the file while it is travelling, and the hub answers that
  // before the read comes back.
  await deleteViaMenu(wrapper, 'a.txt')
  await flush(2)

  read.release(text('hello'))
  await flush()

  assert.equal(
    wrapper.findAll('.tabs__tab').length,
    0,
    'a tab was installed for a file that had been deleted',
  )
  // And the click is answered rather than swallowed: the user asked for a file
  // and is told why they did not get it.
  const notices = (wrapper.emitted('notice') ?? []).flat()
  assert.ok(
    notices.some((text) => String(text).includes('deleted before it could be opened')),
    `expected a notice saying the file was deleted, got ${JSON.stringify(notices)}`,
  )
  wrapper.unmount()
})

// A menu is opened from the keyboard as well as with a pointer -- Shift+F10, or
// the menu key, on a focused row -- and it declares itself a menu, which is a
// promise about how it behaves. Taking the focus when it opens, moving it with
// the arrow keys, and giving it back to the row it came from is what makes the
// actions it holds reachable without a pointer.
test('the context menu takes the focus, walks its items, and gives it back', async () => {
  // Attached to the document, because focus is a property of a document: an
  // element outside one cannot hold it, and nothing here would be observable.
  resetEditors()
  setToken('tok')
  hubFetch({
    list: (path) =>
      json({
        path,
        entries: [
          { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'dir', is_dir: true, size: 0, mtime: 1000 },
        ],
        truncated: false,
      }),
  })
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  const focused = () => (document.activeElement as HTMLElement | null)?.textContent?.trim()
  const label = row(wrapper, 'dir').find('.tree__label')
  await label.trigger('contextmenu')
  await flush(2)

  assert.equal(wrapper.find('.menu').exists(), true, 'the menu never opened')
  assert.equal(focused(), 'New file', 'the menu did not take the focus when it opened')

  // Keys go to whatever holds the focus, which is how a user presses them.
  const press = async (key: string) => {
    const item = document.activeElement
    assert.ok(item instanceof HTMLElement, 'nothing held the focus to press a key on')
    item.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
    await flush(1)
  }

  // Down walks it in the order shown, and End reaches the last item a user can
  // actually choose: Download is refused for a directory, and a disabled item is
  // not a stop for the focus.
  await press('ArrowDown')
  assert.equal(focused(), 'New directory')
  await press('End')
  assert.equal(focused(), 'Delete', 'the focus stopped on an item that cannot be chosen')
  await press('ArrowDown')
  assert.equal(focused(), 'New file', 'ArrowDown past the last item should wrap')
  await press('ArrowUp')
  assert.equal(focused(), 'Delete', 'ArrowUp past the first item should wrap')

  await press('Escape')
  await flush(2)
  assert.equal(wrapper.find('.menu').exists(), false, 'Escape did not close the menu')
  assert.equal(document.activeElement, label.element, 'the focus did not go back to the row')
  wrapper.unmount()
})

// The menu hands the keyboard back to the row it was opened from, and a rename
// or a delete takes that row away by removing or replacing the element: the
// browser puts the focus on the document body, which is where a keyboard user has
// to start over from. Both actions put it back in the tree, the rename on the row
// the entry now has and the delete on whichever row stands where it did.
test('a rename and a delete leave the keyboard in the tree', async () => {
  resetEditors()
  setToken('tok')
  hubFetch({
    // All three names are listed so that the rows exist for the clicks and for
    // the row the rename produces; a real hub would list whichever one exists.
    list: (path) =>
      json({
        path,
        entries: [
          { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'b.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'z.txt', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      }),
  })
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  const label = () => document.activeElement as HTMLElement | null
  const focusedPath = () => label()?.dataset?.path

  await renameViaMenu(wrapper, 'a.txt', 'z.txt')
  await flush(2)
  assert.equal(
    focusedPath(),
    '/srv/work/z.txt',
    `the focus did not follow the renamed entry: ${String(document.activeElement)}`,
  )

  // The delete puts it on the entry that follows the one removed, or on the one
  // before it when that was last -- a path, not a row number, so it means the
  // same thing in a listing that has been re-read since.
  await deleteViaMenu(wrapper, 'z.txt')
  await flush(2)
  assert.equal(
    focusedPath(),
    '/srv/work/b.txt',
    `the focus did not land beside the deleted entry: ${String(document.activeElement)}`,
  )
  wrapper.unmount()
})

// The delete waits for the writes already travelling for the entry, and that wait
// is unbounded -- there is no timeout anywhere in this client. So the user can
// open another directory while it waits, and the row that stands where the
// deleted one did is a row of *that* listing: a place they have never been, which
// the next Space or Enter would act on. Where the focus goes is decided by the
// listing it was measured in.
test('a delete does not move the keyboard into a listing the user opened', async () => {
  const write = deferred()
  hubFetch({
    write: write.respond,
    list: (path) =>
      path === '/srv/work'
        ? json({
            path,
            entries: [
              { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
              { name: 'sub', is_dir: true, size: 0, mtime: 1000 },
            ],
            truncated: false,
          })
        : json({
            path,
            entries: [{ name: 'x.txt', is_dir: false, size: 5, mtime: 1000 }],
            truncated: false,
          }),
  })
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  // A save of a.txt is travelling, so the delete of it is held waiting.
  await openFile(wrapper, 'a.txt')
  await type()
  await (await saveButton(wrapper)).trigger('click')
  await flush(2)
  await deleteViaMenu(wrapper, 'a.txt')
  await flush(2)

  // While it waits, the user opens the subdirectory -- which destroys the row the
  // focus was on, and is exactly the navigation the manager must not fight.
  await row(wrapper, 'sub').find('.tree__label').trigger('click')
  await flush(2)
  assert.equal(wrapper.find('.tree__label').text(), 'x.txt', 'the listing did not change')

  write.release(json({ mtime: 2000, mtime_nanos: '2000000000' }))
  await flush(4)

  const active = document.activeElement as HTMLElement | null
  assert.equal(
    active?.classList.contains('tree__label'),
    false,
    `the focus was moved onto a row of a listing the user had opened: ${String(active?.textContent)}`,
  )
  wrapper.unmount()
})

// The strip declares itself a tablist, and that is a promise about behaviour as
// well as about names: a list of tabs is one stop in the tab order rather than
// one per tab with its close button beside it, the arrows move between them, and
// the panel says which tab it is showing. Without it the roles describe a widget
// that does not exist: a user who tabs into the list and then presses an arrow --
// which is what the pattern says to do -- is stuck on the tab the focus landed
// on, and has to walk out of the list and back in to reach another.
test('the editor tabs answer the keys a tablist is expected to answer', async () => {
  resetEditors()
  setToken('tok')
  hubFetch()
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  await openFile(wrapper, 'a.txt')
  await openFile(wrapper, 'b.txt')

  let tabs = wrapper.findAll('.tabs__label')
  assert.equal(tabs.length, 2)
  // Roving tabindex: the selected tab is the one the keyboard lands on, and the
  // other is reached from it.
  assert.equal(tabs[0].attributes('tabindex'), '-1')
  assert.equal(tabs[1].attributes('tabindex'), '0')

  // The panel and the tab name each other, using ids the two components share.
  const panel = wrapper.find('.fm__content')
  assert.equal(panel.attributes('role'), 'tabpanel')
  assert.equal(panel.attributes('aria-labelledby'), tabs[1].attributes('id'))
  assert.ok(panel.attributes('id'), 'the panel has no id for a tab to point at')
  assert.equal(tabs[0].attributes('aria-controls'), panel.attributes('id'))
  assert.equal(tabs[1].attributes('aria-controls'), panel.attributes('id'))

  // Keys go to whatever holds the focus, which is how a user presses them.
  const press = async (key: string) => {
    const held = document.activeElement
    assert.ok(held instanceof HTMLElement, 'nothing held the focus to press a key on')
    held.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
    await flush(2)
  }

  tabs[1].element.focus()
  await press('ArrowRight')
  tabs = wrapper.findAll('.tabs__label')
  assert.equal(document.activeElement, tabs[0].element, 'ArrowRight did not wrap to the first tab')
  assert.equal(tabs[0].attributes('aria-selected'), 'true', 'the focus moved without selecting')

  await press('End')
  tabs = wrapper.findAll('.tabs__label')
  assert.equal(document.activeElement, tabs[1].element)
  assert.equal(tabs[1].attributes('aria-selected'), 'true')
  assert.equal(
    wrapper.find('.fm__content').attributes('aria-labelledby'),
    tabs[1].attributes('id'),
    'the panel still names the tab that is no longer selected',
  )

  await press('Home')
  tabs = wrapper.findAll('.tabs__label')
  assert.equal(document.activeElement, tabs[0].element)

  // Nothing else in the strip is a stop: with ten files open a list of tabs is
  // still one stop, and a strip of close buttons to walk past is what the role
  // exists to avoid. Closing is Delete on the tab itself instead.
  for (const close of wrapper.findAll('.tabs__close')) {
    assert.equal(close.attributes('tabindex'), '-1', 'a close button is in the tab order')
  }
  await press('Delete')
  await flush(2)
  assert.equal(wrapper.findAll('.tabs__tab').length, 1, 'Delete did not close the focused tab')
  wrapper.unmount()
})

// What follows a directory in a flat listing is its first child, and deleting the
// directory takes that child with it. So the row that stands where the directory
// did is the next one *outside* it -- not the row at the same index, and not the
// top of the listing, which is what a search that ignored depth would produce.
test('deleting a directory puts the focus outside what it contained', async () => {
  resetEditors()
  setToken('tok')
  hubFetch({
    list: (path) =>
      path === '/srv/work'
        ? json({
            path,
            entries: [
              { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
              { name: 'd', is_dir: true, size: 0, mtime: 1000 },
              { name: 'z.txt', is_dir: false, size: 5, mtime: 1000 },
            ],
            truncated: false,
          })
        : json({
            path,
            entries: [{ name: 'inner.txt', is_dir: false, size: 5, mtime: 1000 }],
            truncated: false,
          }),
  })
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  // Opened, so its child is rendered between it and the file that follows.
  await row(wrapper, 'd').find('.tree__caret').trigger('click')
  await flush(2)
  assert.ok(row(wrapper, 'inner.txt'), 'the directory did not open')

  await deleteViaMenu(wrapper, 'd')
  await flush(2)

  const focused = document.activeElement as HTMLElement | null
  assert.equal(
    focused?.dataset?.path,
    '/srv/work/z.txt',
    `the focus did not land outside the deleted directory: ${String(focused?.textContent)}`,
  )
  wrapper.unmount()
})

// The editor is mounted per editing session, and a rename keeps the session: an
// editor keyed by path would be destroyed and rebuilt under the new name, taking
// the undo history with it -- and the history a user reaches for is the one
// belonging to the file they just worked on. The other half of that rule, that a
// file switched away from keeps its editor, has its own test; this is the half a
// rename covers.
test('a rename keeps the editor that is holding the file', async () => {
  resetEditors()
  setToken('tok')
  hubFetch({
    list: (path) =>
      json({
        path,
        entries: [
          { name: 'a.txt', is_dir: false, size: 5, mtime: 1000 },
          { name: 'z.txt', is_dir: false, size: 5, mtime: 1000 },
        ],
        truncated: false,
      }),
  })
  const wrapper = mount(FileManagerOverlay, {
    props: { session: 'work' },
    attachTo: document.body,
  })
  await flush()

  await openFile(wrapper, 'a.txt')
  await type(' typed')
  const built = instances.length

  await renameViaMenu(wrapper, 'a.txt', 'z.txt')
  await flush(2)

  assert.equal(instances.length, built, 'the rename built another editor')
  assert.equal(instances[0].destroyed, false, 'the rename destroyed the editor')
  assert.equal(instances[0].doc, 'hello typed', 'the rename lost what had been typed into it')
  assert.match(wrapper.find('.tabs__name').text(), /z\.txt/, 'the tab did not follow the rename')
  wrapper.unmount()
})

test('the editor stand-in applies a transaction the way the editor does', () => {
  const view = new EditorView({ doc: 'abcdef', extensions: [] })

  // Every position is read against the document as it was before the
  // transaction, not against the document as it stands part-way through it.
  view.dispatch({
    changes: [
      { from: 1, to: 2, insert: 'XY' },
      { from: 3, to: 4, insert: 'Z' },
    ],
  })
  assert.equal(view.doc, 'aXYcZef')
  assert.equal(view.state.doc.toString(), 'aXYcZef')

  // A replacement that happens to reproduce the same text is still a change: the
  // document is identical, but a listener runs.
  let reported = 0
  const watched = new EditorView({
    doc: 'abc',
    extensions: [() => (reported += 1)],
  })
  watched.dispatch({ changes: { from: 2, to: 3, insert: 'c' } })
  assert.equal(reported, 1, 'an identical replacement was reported as no change')
  assert.equal(watched.doc, 'abc')

  // And a transaction that really changes nothing reports nothing.
  watched.dispatch({ changes: { from: 2, to: 2, insert: '' } })
  assert.equal(reported, 1, 'a transaction with no change in it fired a listener')

  // What the stand-in cannot model it refuses rather than answering with a
  // document the editor could never hold: the editor composes overlapping
  // changes and rejects a range outside the document, and neither is modelled
  // here. A silent wrong answer is what the last two rounds kept finding.
  assert.throws(
    () => view.dispatch({ changes: { from: 4, to: 12, insert: 'Z' } }),
    RangeError,
    'a change past the end of the document was applied',
  )
  assert.throws(
    () =>
      view.dispatch({
        changes: [
          { from: 1, to: 3, insert: 'X' },
          { from: 2, to: 4, insert: 'Y' },
        ],
      }),
    /overlapping/,
    'overlapping changes were applied as if they did not overlap',
  )
})
