<script setup lang="ts">
// The file manager. It owns every piece of its own state -- the loaded tree, the
// open tabs, the dirty set -- and discards all of it on close, so nothing here
// outlives the panel and no store or route is needed.
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'

import {
  FileApiError,
  createEntry,
  deleteEntry,
  downloadFile,
  fetchWorkingDirectory,
  probeFile,
  readFile,
  renameEntry,
  stampFromList,
  type Entry,
  type StartDirectory,
} from '../../files/api'
import { basename, dirname, joinPath, quoteForShell } from '../../files/pathUtils'
import { createRenames } from '../../files/renames'
import { getConfig } from '../../config'
import { choosePreview, editable, presentation, type Classification } from '../../files/preview'
import {
  anyDirty,
  beginSave,
  isDirty,
  saveOpenFile,
  settleSave,
  type OpenFile,
} from '../../files/tabs'
import {
  createTreeState,
  forgetDirectory,
  invalidateDirectory,
  isExpanded,
  loadDirectory,
} from '../../files/tree'
import CodeEditor from './CodeEditor.vue'
import EditorTabs from './EditorTabs.vue'
import FileContextMenu from './FileContextMenu.vue'
import FileTree from './FileTree.vue'
import ImagePreview from './ImagePreview.vue'
import MarkdownPreview from './MarkdownPreview.vue'

// The session is a seed, not a dependency: it names the directory to open at,
// and it is where an inserted path goes. Saving reads and writes the hub's
// filesystem through the API, which does not need the session to exist -- so a
// null one means "no seed", not "close the manager". Killing the session the
// manager was opened from must not take unsaved edits with it.
const props = defineProps<{ session: string | null }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'notice', text: string, level: 'error' | 'warning'): void
  (e: 'insert-path', text: string): void
  (e: 'dirty-change', dirty: boolean): void
}>()

const tree = reactive(createTreeState())
const current = ref('')
const loading = ref(true)
// failure takes over the panel, so it is only ever set when there is nothing
// else to show. navError is reported alongside the listing and leaves it alone.
const failure = ref('')
const navError = ref('')
const tabs = ref<OpenFile[]>([])
const activePath = ref<string | null>(null)
const markdownView = reactive(new Map<string, 'source' | 'preview'>())
const menu = ref<{ x: number; y: number; entry: Entry; path: string } | null>(null)

// navigationAt is the ticket of the most recent goTo; an earlier one that
// finishes later must not write. opening holds the paths being read right now.
let navigationAt = 0
const opening = new Set<string>()
// saveTickets is the newest save attempted per tab session, so an out-of-order
// answer from an older one cannot be folded back in.
const saveTickets = new Map<number, number>()
// renaming holds the source path of every rename this manager has asked for and
// not yet been answered about. A save answer arriving while one is outstanding
// describes a write that named the same path, and the two crossed on the wire:
// see settleSave for why that answer is not recorded.
const renaming = new Set<string>()
// nextTabId numbers the editing sessions. See OpenFile.id for why an answer is
// matched to a session rather than to a path.
let nextTabId = 1

const active = computed(() => tabs.value.find((tab) => tab.path === activePath.value) ?? null)
const dirty = computed(() => anyDirty(tabs.value))
const preview = computed(() =>
  active.value === null ? null : presentation(active.value.name, active.value.size, active.value.binary),
)
const activeIsMarkdownSource = computed(
  () => active.value !== null && (markdownView.get(active.value.path) ?? 'source') === 'source',
)
// editorTabs is every open file whose contents the editor is holding -- which is
// every open file that is editable at all, not only the one on screen. All of
// them stay mounted: unmounting the editor for a file the user switched away
// from would take that file's undo history with it, and history is the thing a
// user reaches for precisely after switching away and back.
const editorTabs = computed(() =>
  tabs.value.filter((tab) => editable(tab.name, tab.size, tab.binary)),
)
// editorVisible says whether the active file is shown in the editor right now,
// as opposed to rendered Markdown or a preview of some other kind.
const editorVisible = computed(
  () =>
    preview.value === 'editor' || (preview.value === 'markdown' && activeIsMarkdownSource.value),
)

watch(dirty, (value) => emit('dirty-change', value), { immediate: true })

/** REASONS turn the hub's wire codes into something a person can act on. The
 * code is what the client matches on, not what the user should have to read. A
 * code with no entry falls through to the code itself, so a hub that adds one
 * still says something rather than nothing. */
const REASONS: Record<string, string> = {
  path_not_allowed: 'that path is outside the directories this hub may open',
  not_found: 'it is no longer there',
  permission_denied: 'the hub is not allowed to read or write it',
  file_too_large: 'it is larger than the size limit for one file',
  conflict: 'it changed on disk since it was read',
  invalid_path: 'the hub cannot use that as a path',
  invalid_body: 'the request did not match the file it described',
  dir_not_empty: 'the directory still contains something',
  write_failed: 'the hub could not complete the write',
  mtime_unavailable: 'the hub did not say when the file last changed',
}

/** fileSizeLimit reads the hub's per-file limit, or zero when it did not say. */
function fileSizeLimit(): number {
  try {
    return getConfig().files.max_file_size
  } catch {
    // A refusal must still be reportable before the configuration has loaded.
    return 0
  }
}

/** reasonFor explains a refusal. A file over the limit names the limit, which is
 * what the specification asks the browser to report rather than the refusal. */
function reasonFor(code: string): string {
  if (code === 'file_too_large') {
    const limit = fileSizeLimit()
    if (limit > 0) return `it is larger than this hub's ${formatSize(limit)} limit for one file`
  }
  return REASONS[code] ?? code
}

function report(err: unknown, action: string) {
  const code = err instanceof FileApiError ? err.code : String(err)
  emit('notice', `${action}: ${reasonFor(code)}`, 'error')
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

/** openWithoutReading opens a tab for a file whose contents are not read: an
 * image, which the preview fetches for itself, or something the hub read as
 * binary. The size is whichever of the two observations is truthful.
 *
 * The path is corrected for a rename that landed while this was travelling, the
 * same way the read path corrects its own. Asking the hub is an await, and a
 * rename completing inside it moves the file exactly as a read's await allows --
 * so a tab built at the old name would name a file that is gone, and a click on
 * the new name would open a second tab for the one file. */
function openWithoutReading(
  path: string,
  startedAt: number,
  entry: Entry,
  size: number,
  binary: Classification,
) {
  const at = renames.resolve(path, startedAt)
  if (tabs.value.some((tab) => tab.path === at)) {
    activePath.value = at
    return
  }
  tabs.value = [
    ...tabs.value,
    {
      id: nextTabId++,
      path: at,
      name: basename(at),
      text: '',
      saved: '',
      // From the listing, which reports milliseconds only. These files are
      // never saved, so the weaker precision costs nothing.
      stamp: stampFromList(entry.mtime),
      size,
      binary,
    },
  ]
  activePath.value = at
}

/** onImageTooLarge records the size an image preview refused to render.
 *
 * The tab's size came from a directory listing, and that is the only thing that
 * ever said this file was small enough to render. Correcting it is the whole
 * fix: how a file is presented is decided from its name and its size, so once
 * the size is true the file presents as information with a download action --
 * which is what the specification asks for above the bound. */
function onImageTooLarge(path: string, size: number) {
  tabs.value = tabs.value.map((tab) => (tab.path === path ? { ...tab, size } : tab))
  emit('notice', `${basename(path)} is ${formatSize(size)}, larger than this view renders.`, 'warning')
}

/** goTo loads a directory and makes it the current one.
 *
 * A refused navigation must not throw away what the user is looking at: stepping
 * up out of a configured boundary is an ordinary thing to try, and it should
 * answer with a reason while leaving the listing in place. Only a failure with
 * nothing on screen yet takes over the panel, because then the reason is all
 * there is to show. */
async function goTo(path: string) {
  // Each navigation takes a ticket, and only the newest one may write. The header
  // buttons stay live while a load is in flight, so two navigations can overlap:
  // without this, the slower one lands last and moves the manager to a directory
  // the user has already left -- or reports an error about one.
  const attempt = ++navigationAt
  loading.value = true
  navError.value = ''
  try {
    await loadDirectory(tree, path, true)
    if (attempt !== navigationAt) return
    current.value = path
    failure.value = ''
  } catch (err) {
    if (attempt !== navigationAt) return
    const detail = err instanceof FileApiError ? err.code : String(err)
    if (current.value === '') {
      failure.value = `${path} could not be opened (${detail}).`
    } else {
      navError.value = `${path} could not be opened (${detail}).`
    }
  } finally {
    if (attempt === navigationAt) loading.value = false
  }
}

/**
 * startDirectory picks where the manager opens.
 *
 * The pane's working directory is read once, as a seed. If the hub reports that
 * it substituted one -- the pane is outside a configured boundary, and only the
 * hub can know that -- the manager opens there and says so. Otherwise the seed
 * may still be unusable for a reason that is not the boundary, such as a shell
 * whose directory was removed, so its nearest listable ancestor is used instead:
 * an ancestor that still exists beats an error.
 */
async function startDirectory(): Promise<void> {
  let seed: StartDirectory = { path: '', substituted: false }
  if (props.session === null) {
    await goTo('/')
    return
  }
  try {
    seed = await fetchWorkingDirectory(props.session)
  } catch {
    seed = { path: '', substituted: false }
  }
  if (seed.path === '') {
    await goTo('/')
    return
  }

  if (seed.substituted) {
    await goTo(seed.path)
    emit('notice', `Opened ${seed.path}: the session's directory is outside the file boundary.`, 'warning')
    return
  }

  let probe = seed.path
  for (;;) {
    try {
      await loadDirectory(tree, probe)
      current.value = probe
      loading.value = false
      if (probe !== seed.path) {
        emit('notice', `Opened ${probe}: the session's directory ${seed.path} was not accessible.`, 'warning')
      }
      return
    } catch (err) {
      const code = err instanceof FileApiError ? err.code : ''
      // A directory that is merely not permitted may still have an ancestor that
      // is, and under an unrestricted boundary there is no configured root to be
      // moved to -- so walking up is what turns a refusal into somewhere to stand.
      if (code !== 'not_found' && code !== 'path_not_allowed') {
        report(err, `Could not open ${probe}`)
        failure.value = `${probe} could not be opened (${code || 'error'}).`
        loading.value = false
        return
      }
      const parent = dirname(probe)
      if (parent === probe) break
      probe = parent
    }
  }

  // Nothing on the way up could be listed, so stay where the session is and show
  // why: a blank panel would hide the boundary that caused it.
  current.value = seed.path
  await goTo(seed.path)
}

/**
 * openFile opens a file in a tab.
 *
 * A file that will be presented as an image or as information is never read as
 * text: its bytes are fetched by the preview that needs them, or not at all. Only
 * something that ends up in the editor is decoded here.
 */
async function openFile(entry: Entry, path: string) {
  // The path as asked for, not as some earlier rename moved it: nothing has
  // happened *during* this read yet, so there is nothing to correct for.
  const existing = tabs.value.find((tab) => tab.path === path)
  if (existing) {
    activePath.value = existing.path
    return
  }
  // A second click while the first read is still in flight would otherwise open
  // the same file twice, and two tabs carrying one key are two tabs that
  // disagree about what they are showing.
  if (opening.has(path)) return
  opening.add(path)
  const startedAt = renames.generation()

  try {
    const kind = choosePreview(entry.name, entry.size)
    if (kind === 'image') {
      // Never read and never asked about: how it is shown is decided from its
      // name, which is all anyone knows.
      openWithoutReading(path, startedAt, entry, entry.size, null)
      return
    }

    // A name that calls a file binary, or an image too large to render, is a
    // guess about contents the hub is the one that can read. One bodyless
    // request gets its answer, and that answer decides: text the name called
    // binary opens in the editor rather than being refused a look. The size is
    // corrected at the same time, since a listing read earlier is the only thing
    // that ever said how big this file is.
    let size = entry.size
    if (kind === 'info') {
      const probed = await probeFile(path)
      size = probed.size
      if (probed.binary) {
        openWithoutReading(path, startedAt, entry, size, true)
        return
      }
    }

    const contents = await readFile(path)
    // A rename while this read was travelling moved the file. The tab belongs
    // where the file is now: built at the old name it would look openable and
    // then refuse to save, which is the stranding retargetTabs exists to avoid.
    //
    // Only a rename that happened *during* this read counts, which is what the
    // generation it started in is for. A read that started after a rename is about
    // whatever is at that name now -- possibly a file the user has just created
    // there -- and following an older record would open a different file under
    // the wrong name and then save over it.
    const at = renames.resolve(path, startedAt)
    // Two paths can resolve to one name after a rename, and a click on the new
    // name during the flight arrives with a different one -- so the check at the
    // top has to happen again here, or two tabs end up sharing a path.
    if (tabs.value.some((tab) => tab.path === at)) {
      activePath.value = at
      return
    }

    tabs.value = [
      ...tabs.value,
      {
        id: nextTabId++,
        path: at,
        name: basename(at),
        text: contents.text,
        saved: contents.text,
        stamp: contents.stamp,
        size,
        binary: contents.binary,
      },
    ]
    activePath.value = at
  } catch (err) {
    report(err, `Could not open ${entry.name}`)
  } finally {
    opening.delete(path)
    // The record only exists for reads that are travelling; with none left it
    // would start redirecting files created at those names later on.
    if (opening.size === 0) renames.clear()
  }
}

async function toggleDirectory(path: string) {
  if (isExpanded(tree, path)) {
    tree.expanded.delete(path)
    return
  }
  try {
    await loadDirectory(tree, path)
    tree.expanded.add(path)
  } catch (err) {
    report(err, `Could not expand ${basename(path)}`)
  }
}

/** save writes one tab, named by its session rather than by its path.
 *
 * The session, the path, and the text are captured together as one SaveRequest
 * before the write travels, and the answer is matched against that same object.
 * Everything here happens around an await: the user can rename the file or
 * switch tabs while the write is in flight, and a rename is the one that bites.
 * It moves the tab to a new path and replaces the tab object, so anything read
 * again at answer time would describe the tab as it now is -- and agreeing with
 * itself is what would mark the file at the new path saved by a write that named
 * the old one. See settleSave. */
async function save(force = false, tabId: number | null = active.value?.id ?? null) {
  const tab = tabs.value.find((candidate) => candidate.id === tabId)
  if (!tab) return

  // Only the newest save for a session may fold its result back in. Two saves in
  // flight (edit again while the first is travelling) can resolve out of order,
  // and the older answer would mark the tab clean at a text and a time that are
  // not what is on disk.
  const ticket = (saveTickets.get(tab.id) ?? 0) + 1
  saveTickets.set(tab.id, ticket)

  const request = beginSave(tab, force)
  try {
    const stamp = await saveOpenFile(request)
    if (!isSameTab(tab.id)) return
    if (saveTickets.get(tab.id) !== ticket) return
    const settled = settleSave(tabs.value, request, stamp, renaming.has(request.path))
    tabs.value = settled.files
    if (settled.outcome === 'raced') {
      emit(
        'notice',
        `${tab.name} was being renamed while it was saved, so the answer was not recorded ` +
          `against it. Save again once the rename finishes.`,
        'warning',
      )
    }
    if (settled.outcome === 'moved') {
      const moved = liveTab(tab.id)
      emit(
        'notice',
        `${tab.name} moved to ${moved?.path ?? 'another path'} while it was being saved, so the ` +
          `answer was not recorded against it. Save again to write the edit to the new name.`,
        'warning',
      )
    }
  } catch (err) {
    if (!isSameTab(tab.id)) return
    if (saveTickets.get(tab.id) !== ticket) return
    const moved = liveTab(tab.id)
    if (moved && moved.path !== request.path) {
      // The path moved, so the question a conflict prompt would ask -- overwrite
      // the file that changed? -- would be about a file this tab no longer
      // holds. Answering it would write to whatever is at the new name instead,
      // which the user was never asked about.
      report(err, `Could not save ${tab.name}, which moved to ${moved.path} while the save travelled`)
      return
    }
    if (err instanceof FileApiError && err.code === 'conflict') {
      const overwrite = window.confirm(
        `${tab.name} changed on disk since it was opened. Overwrite it with your version?`,
      )
      if (overwrite) await save(true, tab.id)
      return
    }
    report(err, `Could not save ${tab.name}`)
  }
}

// renames records where a rename moved a path, for the reads that were travelling
// when it happened. See files/renames.ts for why the record is scoped to the read
// rather than consulted wholesale.
const renames = createRenames()

/** liveTab is the tab as it is now, by session.
 *
 * Everything that happens during a round trip replaces the tab object -- a
 * rename retargets it, a save rewrites it -- so a reference captured before an
 * await is a snapshot, and anything that has to be current must be looked up
 * again rather than held. */
function liveTab(id: number): OpenFile | undefined {
  return tabs.value.find((candidate) => candidate.id === id)
}

/** isSameTab reports whether the editing session a save started from is still
 * open.
 *
 * Closing a file and reopening it gives a new session holding whatever was read
 * the second time; an answer meant for the first would be folded into the second,
 * marking it saved at a text and a time that describe a write it never made. A
 * rename keeps the session, because the tab that is now at the new path is the
 * one that asked -- which is why the path is checked separately, in settleSave. */
function isSameTab(id: number): boolean {
  return liveTab(id) !== undefined
}

function closeTab(path: string) {
  const tab = tabs.value.find((candidate) => candidate.path === path)
  if (tab && isDirty(tab) && !window.confirm(`${tab.name} has unsaved changes. Close it anyway?`)) {
    return
  }
  tabs.value = tabs.value.filter((candidate) => candidate.path !== path)
  if (activePath.value === path) {
    activePath.value = tabs.value.length > 0 ? tabs.value[tabs.value.length - 1].path : null
  }
}

/** requestClose warns before discarding unsaved edits, which are not recoverable
 * once the panel is gone. */
function requestClose() {
  if (dirty.value && !window.confirm('Files have unsaved changes. Close the file manager anyway?')) {
    return
  }
  emit('close')
}

/** refreshCurrent re-reads what is on screen.
 *
 * That is the current directory and every directory the user has opened beneath
 * it, because those are the listings being displayed and a stale one shows rows
 * for entries that are gone. Anything merely cached is left alone: it is not
 * being displayed, and the next visit reads it. */
async function refreshCurrent() {
  if (current.value === '') return
  await goTo(current.value)
  await resyncExpanded(current.value)
}

/** resyncExpanded re-reads every directory the user has opened beneath `root`.
 *
 * A directory that changed outside the manager has to show what it holds now; a
 * directory that was deleted has to stop rendering its old contents, and
 * forgetting it is what makes it render closed. */
async function resyncExpanded(root: string) {
  const prefix = root === '/' ? '/' : `${root}/`
  const open = [...tree.expanded].filter((path) => path.startsWith(prefix))
  let reported = false
  const note = (err: unknown, path: string) => {
    // Reported once: the usual cause takes out a whole subtree at a time, and a
    // notice per directory would bury the one that says what happened.
    if (reported) return
    report(err, `Could not reload ${basename(path)}`)
    reported = true
  }

  for (const path of open) {
    try {
      await loadDirectory(tree, path, true)
    } catch (err) {
      // A transport failure says nothing about the directory -- it may be there
      // and simply unreachable -- so what is cached stays, and a later refresh
      // can settle it. Only an answer from the hub justifies forgetting it, and
      // forgetting is what stops it rendering contents it no longer has.
      if (!(err instanceof FileApiError)) {
        note(err, path)
        continue
      }
      forgetDirectory(tree, path)
      note(err, path)
    }
  }
}

/** settleMutation brings the tree back in step after something on disk changed.
 *
 * The entry that changed is forgotten outright -- if it was a directory, what is
 * cached beneath it describes something that no longer exists, and the same
 * applies to whatever now stands at `destination`. The directory it was in is
 * only invalidated, and then re-read if the user is looking at it, so the result
 * appears where they made the change instead of blanking the tree. */
async function settleMutation(changed: string, destination = '') {
  // Standing inside what was just removed: move to where it was, rather than
  // leaving the tree rooted at a directory that is gone.
  if (current.value === changed || current.value.startsWith(`${changed}/`)) {
    current.value = dirname(changed)
  }

  const parent = dirname(changed)
  const parentWasOpen = isExpanded(tree, parent)
  forgetDirectory(tree, changed)
  if (destination !== '') forgetDirectory(tree, destination)
  invalidateDirectory(tree, parent)
  if (parent !== current.value && parentWasOpen) {
    try {
      await loadDirectory(tree, parent, true)
      tree.expanded.add(parent)
    } catch (err) {
      report(err, `Could not reload ${basename(parent)}`)
    }
  }
  await goTo(current.value)
}

async function createHere(isDir: boolean) {
  // Nothing is being shown yet, so there is no directory to create in. The
  // buttons are disabled in that state; this is the same rule at the point that
  // matters, because joinPath('', name) would name a path at the filesystem root.
  if (current.value === '') return
  const name = window.prompt(isDir ? 'New directory name' : 'New file name')
  if (!name) return
  const path = joinPath(current.value, name)
  try {
    await createEntry(path, isDir)
    await settleMutation(path)
    if (!isDir) await openFile({ name, is_dir: false, size: 0, mtime: 0 }, path)
  } catch (err) {
    report(err, `Could not create ${name}`)
  }
}

/** retargetTabs moves the tabs that named an entry, or anything under it, to
 * where it is now.
 *
 * A tab left on the old path would save to a file that no longer exists, which
 * strands the edit: the hub answers not_found and there is no way out through
 * the UI. Renaming a directory carries its open files with it. */
function retargetTabs(path: string, target: string, name: string) {
  const prefix = `${path}/`
  renames.record(path, target)

  // Where each tab that followed the rename ends up. A name freed outside the
  // manager -- a file removed from the terminal -- can already have a tab on it,
  // and the rename then lands on that name: the tab that follows the file is the
  // one to keep, because the other names something that is gone, and two tabs for
  // one path make the editor show whichever was opened first.
  const moving = new Map<number, string>()
  for (const tab of tabs.value) {
    if (tab.path === path) moving.set(tab.id, target)
    else if (tab.path.startsWith(prefix)) moving.set(tab.id, target + tab.path.slice(path.length))
  }
  const taken = new Set(moving.values())
  const stale = tabs.value.filter((tab) => !moving.has(tab.id) && taken.has(tab.path))
  const lost = stale.filter(isDirty)
  if (lost.length > 0) {
    const names = lost.map((tab) => tab.name).join(', ')
    emit('notice', `Unsaved changes in ${names} were dropped: ${target} replaced that name.`, 'warning')
  }
  tabs.value = tabs.value
    .filter((tab) => moving.has(tab.id) || !taken.has(tab.path))
    .map((tab) => {
      const destination = moving.get(tab.id)
      if (destination === undefined) return tab
      return { ...tab, path: destination, name: tab.path === path ? name : tab.name }
    })

  if (activePath.value === path) activePath.value = target
  else if (activePath.value?.startsWith(prefix)) {
    activePath.value = target + activePath.value.slice(path.length)
  }

  // The rendered-or-source choice is keyed by path too, so it moves with the
  // file; otherwise a renamed Markdown file silently drops back to source.
  for (const [key, view] of [...markdownView]) {
    if (key === path) {
      markdownView.delete(key)
      markdownView.set(target, view)
    } else if (key.startsWith(prefix)) {
      markdownView.delete(key)
      markdownView.set(target + key.slice(path.length), view)
    }
  }
}

function renameTarget(entry: Entry, path: string) {
  const name = window.prompt('Rename to', entry.name)
  if (!name || name === entry.name) return
  const target = joinPath(dirname(path), name)
  renaming.add(path)
  void renameEntry(path, target)
    .then(() => {
      retargetTabs(path, target, name)
      return settleMutation(path, target)
    })
    .catch((err: unknown) => report(err, `Could not rename ${entry.name}`))
    .finally(() => renaming.delete(path))
}

function deleteTarget(entry: Entry, path: string) {
  const kind = entry.is_dir ? 'directory' : 'file'
  // Tabs for the entry and for anything it contains: a deleted directory takes
  // its open files with it, and unsaved edits among them are not recoverable.
  const prefix = `${path}/`
  const doomed = tabs.value.filter((tab) => tab.path === path || tab.path.startsWith(prefix))
  const unsaved = doomed.filter(isDirty)

  let question = `Delete the ${kind} ${entry.name}? This cannot be undone.`
  if (unsaved.length > 0) {
    const names = unsaved.map((tab) => tab.name).join(', ')
    question += `\n\nOpen with unsaved changes: ${names}. Those edits will be lost.`
  }
  if (!window.confirm(question)) {
    return
  }

  void deleteEntry(path, entry.is_dir)
    .then(() => {
      // Filtered by path where the answer lands, not by the set captured before
      // the request: a tab opened while the delete travelled names a file that is
      // now gone, and would otherwise survive pointing at nothing. A tab renamed
      // out of the way during the flight no longer matches, and should survive.
      const removed = tabs.value.filter(
        (tab) => tab.path === path || tab.path.startsWith(prefix),
      )
      // The warning named what was dirty when the user answered. A tab that
      // appeared or was edited while the delete travelled was not in it -- and
      // the file is gone by now, so it is said rather than asked.
      const warned = new Set(unsaved.map((tab) => tab.id))
      const unwarned = removed.filter((tab) => isDirty(tab) && !warned.has(tab.id))
      if (unwarned.length > 0) {
        const names = unwarned.map((tab) => tab.name).join(', ')
        emit('notice', `Unsaved changes in ${names} went with ${entry.name}.`, 'warning')
      }

      tabs.value = tabs.value.filter(
        (tab) => tab.path !== path && !tab.path.startsWith(prefix),
      )
      if (!tabs.value.some((tab) => tab.path === activePath.value)) {
        activePath.value = tabs.value.length > 0 ? tabs.value[tabs.value.length - 1].path : null
      }
      return settleMutation(path)
    })
    .catch((err: unknown) => report(err, `Could not delete ${entry.name}`))
}

async function download(path: string, name: string) {
  try {
    const blob = await downloadFile(path)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = name
    link.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    report(err, `Could not download ${name}`)
  }
}

async function copyPathToClipboard(path: string) {
  const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined
  if (!clipboard) {
    emit('notice', 'Copy failed: the browser exposes no clipboard outside a secure context.', 'warning')
    return
  }
  try {
    await clipboard.writeText(path)
  } catch {
    emit('notice', 'Copy failed.', 'warning')
  }
}

/** insertPath hands the terminal a path it can accept as literal text. */
function insertPath(path: string) {
  const text = quoteForShell(path)
  if (text === null) {
    emit('notice', 'That path cannot be inserted: it contains a control character.', 'warning')
    return
  }
  emit('insert-path', text)
}

function onMenuAction(action: string) {
  const target = menu.value
  menu.value = null
  if (!target) return
  switch (action) {
    case 'new-file':
      void createHere(false)
      break
    case 'new-dir':
      void createHere(true)
      break
    case 'rename':
      renameTarget(target.entry, target.path)
      break
    case 'delete':
      deleteTarget(target.entry, target.path)
      break
    case 'download':
      void download(target.path, target.entry.name)
      break
    case 'copy-path':
      void copyPathToClipboard(target.path)
      break
    case 'insert-path':
      insertPath(target.path)
      break
  }
}

function openMenu(event: MouseEvent, entry: Entry, path: string) {
  menu.value = { x: event.clientX, y: event.clientY, entry, path }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || menu.value !== null) return
  // The editor handles Escape itself without stopping propagation -- closing its
  // search panel, for instance -- so an Escape aimed at the editor must not also
  // close the whole manager.
  const target = event.target
  if (target instanceof HTMLElement && target.closest('.code-editor') !== null) return
  requestClose()
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  void startDirectory()
})
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))

defineExpose({ hasUnsavedChanges: () => dirty.value })
</script>

<template>
  <div class="fm" @click.self="menu = null">
    <header class="fm__bar">
      <button
        class="fm__btn"
        type="button"
        title="go to the parent directory"
        :disabled="current === '/' || current === ''"
        @click="goTo(dirname(current))"
      >↑</button>
      <span class="fm__path" :title="current">{{ current || '…' }}</span>
      <button class="fm__btn" type="button" title="new file" :disabled="current === ''" @click="createHere(false)">+ file</button>
      <button class="fm__btn" type="button" title="new directory" :disabled="current === ''" @click="createHere(true)">+ dir</button>
      <button class="fm__btn" type="button" title="reload this directory" @click="refreshCurrent">↻</button>
      <button class="fm__btn fm__btn--close" type="button" title="close" @click="requestClose">✕</button>
    </header>

    <div class="fm__body">
      <aside class="fm__tree">
        <p v-if="loading" class="fm__state">Loading…</p>
        <p v-else-if="failure" class="fm__state fm__state--error" role="alert">{{ failure }}</p>
        <template v-else>
          <p v-if="navError" class="fm__nav-error" role="alert">
            <span>{{ navError }}</span>
            <button class="fm__nav-dismiss" type="button" aria-label="Dismiss" @click="navError = ''">
              ×
            </button>
          </p>
          <FileTree
            :path="current"
            :state="tree"
            :current="current"
            @open="openFile"
            @select="goTo"
            @toggle="toggleDirectory"
            @context="openMenu"
          />
        </template>
      </aside>

      <section class="fm__viewer">
        <EditorTabs :files="tabs" :active="activePath" @select="activePath = $event" @close="closeTab" />

        <div v-if="!active" class="fm__state">Select a file to open it.</div>

        <template v-else>
          <div class="fm__actions">
            <button
              class="fm__btn"
              type="button"
              :disabled="!isDirty(active)"
              title="save (the hub refuses if the file changed since it was read)"
              @click="save(false)"
            >Save</button>
            <button
              v-if="preview === 'markdown'"
              class="fm__btn"
              type="button"
              @click="markdownView.set(active.path, activeIsMarkdownSource ? 'preview' : 'source')"
            >{{ activeIsMarkdownSource ? 'Preview' : 'Source' }}</button>
            <span class="fm__spacer"></span>
            <span v-if="isDirty(active)" class="fm__dirty" role="status">unsaved changes</span>
          </div>

          <div class="fm__content">
            <template v-if="active">
              <ImagePreview
                v-if="preview === 'image'"
                :path="active.path"
                @notice="(text, level) => emit('notice', text, level)"
                @too-large="onImageTooLarge"
              />
              <MarkdownPreview
                v-else-if="preview === 'markdown' && !activeIsMarkdownSource"
                :source="active.text"
              />
              <div v-else-if="!editorVisible" class="fm__info">
                <p class="fm__state">
                  {{ active.name }} is not shown here: it is
                  {{ preview === 'info' ? 'binary or too large to preview' : 'not previewable' }}.
                </p>
                <p class="fm__meta">{{ formatSize(active.size) }} · {{ active.path }}</p>
                <button class="fm__btn" type="button" @click="download(active.path, active.name)">
                  Download
                </button>
              </div>
            </template>

            <!--
              One editor per open file, all of them mounted, only the active one
              shown. Keyed by the editing session rather than by the path, so a
              rename keeps the instance (and its undo history) instead of
              rebuilding it under the new name.

              Keeping the others mounted is the point. The editor owns its own
              history, and history does not survive being destroyed and
              rebuilt -- so an editor that were unmounted whenever the user
              looked at another tab would lose the undo stack of every file they
              switched away from, which is exactly when they reach for it.
            -->
            <div
              v-for="tab in editorTabs"
              v-show="tab.id === active?.id && editorVisible"
              :key="tab.id"
              class="fm__editor"
            >
              <CodeEditor v-model="tab.text" :active="tab.id === active?.id && editorVisible" />
            </div>
          </div>
        </template>
      </section>
    </div>

    <FileContextMenu
      v-if="menu"
      :x="menu.x"
      :y="menu.y"
      :name="menu.entry.name"
      :is-dir="menu.entry.is_dir"
      @new-file="onMenuAction('new-file')"
      @new-dir="onMenuAction('new-dir')"
      @rename="onMenuAction('rename')"
      @delete="onMenuAction('delete')"
      @download="onMenuAction('download')"
      @copy-path="onMenuAction('copy-path')"
      @insert-path="onMenuAction('insert-path')"
      @close="menu = null"
    />
  </div>
</template>

<style scoped>
.fm {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--app-bg);
  border-top: 1px solid var(--th-border);
}

.fm__bar {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 6px 8px;
  background: var(--th-surface);
  border-bottom: 1px solid var(--th-border);
}

.fm__path {
  flex: 1;
  overflow: hidden;
  color: var(--th-text-mid);
  font-family: var(--app-code-font);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fm__btn {
  padding: 3px 8px;
  color: var(--th-text-hi);
  font-size: 12px;
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  cursor: pointer;
}

.fm__btn:disabled {
  color: var(--th-text-lo);
  cursor: default;
}

.fm__btn--close:hover {
  color: var(--th-danger);
}

.fm__body {
  display: flex;
  flex: 1;
  min-height: 0;
}

.fm__tree {
  width: 280px;
  min-width: 180px;
  overflow: auto;
  border-right: 1px solid var(--th-border);
}

.fm__viewer {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.fm__actions {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 6px 8px;
  border-bottom: 1px solid var(--th-border);
}

.fm__spacer {
  flex: 1;
}

.fm__dirty {
  color: var(--th-warning);
  font-size: 11px;
}

.fm__content {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

/* A hidden editor is out of the flow, so the visible one fills the viewer. */
.fm__editor {
  height: 100%;
}

.fm__state {
  margin: 10px;
  color: var(--th-text-mid);
  font-size: 12px;
}

.fm__state--error {
  color: var(--th-danger);
}

/* A refused navigation is reported next to the listing, not in place of it. */
.fm__nav-error {
  display: flex;
  gap: 6px;
  align-items: flex-start;
  margin: 6px 8px;
  padding: 6px 8px;
  color: var(--th-warning);
  font-size: 11px;
  border: 1px solid var(--th-warning);
  border-radius: 4px;
}

.fm__nav-dismiss {
  margin-left: auto;
  color: var(--th-text-mid);
  font-size: 12px;
  line-height: 1;
  background: none;
  border: none;
  cursor: pointer;
}

.fm__info {
  padding: 8px;
}

.fm__meta {
  margin: 0 10px;
  color: var(--th-text-lo);
  font-family: var(--app-code-font);
  font-size: 11px;
}
</style>
