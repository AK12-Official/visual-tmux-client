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
    read: Responder
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
      if (overrides.read) return overrides.read()
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
    notices.some((text) => String(text).includes('A delete of a.txt is in flight')),
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
    notices.some((text) => String(text).includes('A delete of f.txt is in flight')),
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

  // And the second file's own arrival is unaffected.
  second.release(text('hello'))
  await flush()
  assert.ok(wrapper.find('.image-preview__img').exists(), 'the second image never rendered')
  wrapper.unmount()
})

// The editor stand-in has to model the editor's change set rather than a
// plausible-looking one: a test written against a stand-in that composes
// positions the wrong way is a green test over a document no user could produce.
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
