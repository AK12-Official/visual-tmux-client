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
import { createPending } from '../../files/pending'
import { reasonFor as reasonForCode } from '../../files/reasons'
import { createPathChanges } from '../../files/pathChanges'
import { getConfig } from '../../config'
import type { Classification } from '../../files/classification'
import { choosePreview, editable, namesAnImage, presentation, wantsText } from '../../files/preview'
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
const menu = ref<{
  x: number
  y: number
  entry: Entry
  path: string
  /** opener is the row the menu was opened from, and where the focus goes back
   * to when it closes. Null when the event carried no element to return to. */
  opener: HTMLElement | null
} | null>(null)

// navigationAt is the ticket of the most recent goTo; an earlier one that
// finishes later must not write. opening holds the paths being read right now.
let navigationAt = 0
const opening = new Set<string>()
// saveTickets is the newest save attempted per tab session, so an out-of-order
// answer from an older one cannot be folded back in.
const saveTickets = new Map<number, number>()
// saving, renaming and deleting are the requests this manager has sent and not
// yet been answered about, by the path each one named. What each one is asked is
// different, which is why there are three: a rename outstanding when a save is
// answered means that answer describes a path the rename may have vacated, so it
// is not recorded (see settleSave), and a delete outstanding when a save is sent
// means the file is on its way out, so the save is not sent at all (see save).
//
// A count per path rather than a flag, because two of a kind can be in flight on
// one path at once. The two questions also look in *opposite* directions, which
// is what makes each of them the right one for its caller: isPending asks
// whether anything is outstanding for this path or for a directory above it, and
// idle waits for the writes naming this path or anything inside it. Both rules,
// with what they are for, are in files/pending.ts.
const saving = createPending()
const renaming = createPending()
const deleting = createPending()
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
// notShown is why a file presented as information is not shown. The reasons are
// different facts and the panel says which one holds. A file whose bytes the hub
// read as binary, or an image too large to render, has a reason of its own that
// has nothing to do with reading. A tab that holds nothing *because* nobody read
// the file is the one a rename leaves behind: the name no longer says image, so
// there is no preview to fetch and no contents to edit, and the panel says that
// rather than describing the file as something nobody has looked at. An image
// name is the one case left out of it -- the size may be the reason, and the
// image preview is what knows it.
const notShown = computed(() => {
  const file = active.value
  if (file === null) return ''
  if (file.binary === null && !namesAnImage(file.name)) return 'its contents have not been read'
  return 'it is binary or too large to preview'
})

watch(dirty, (value) => emit('dirty-change', value), { immediate: true })

/** REASONS and reasonFor live in files/reasons.ts, where a test can hold them
 * against the codes the hub's file routes actually answer with. A code with no
 * entry does not fail anything -- it shows the user the identifier itself -- so
 * the two sides have to be checked against each other somewhere. */
function reasonFor(code: string): string {
  return reasonForCode(code, fileSizeLimit(), formatSize)
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

function report(err: unknown, action: string) {
  const code = err instanceof FileApiError ? err.code : String(err)
  emit('notice', `${action}: ${reasonFor(code)}`, 'error')
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

/** reportRemoved says that a file the hub had already been asked for was deleted
 * before the answer landed.
 *
 * A read this manager has sent is granted before the answer arrives, so a delete
 * answered in between does not stop it: the contents come back for a path that
 * is gone. The delete sweeps the tabs it finds afterwards, and this is the other
 * half of that sweep -- the answer that lands after it would install a tab
 * naming nothing, whose save is answered not_found with no way out through the
 * interface. What records that is the delete itself, for the reads that were
 * travelling when it was answered; see files/pathChanges.ts.
 *
 * A directory being deleted takes its open files with it, which resolves the
 * same way, so there is one thing to say about both. Naming the file rather than
 * the path because the name is what the user clicked. */
function reportRemoved(name: string) {
  emit('notice', `${name} was deleted before it could be opened.`, 'warning')
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
  const at = changes.resolve(path, startedAt)
  if (at === null) {
    reportRemoved(entry.name)
    return
  }
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
  const startedAt = changes.generation()

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
      // The listing's size stands in when the hub did not report one: it is only
      // ever displayed for a file that is not read as text.
      size = probed.size ?? entry.size
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
    const at = changes.resolve(path, startedAt)
    // A delete of this path, or of a directory holding it, was answered while
    // this read was travelling: the answer describes a file that is gone, and
    // installing it would leave a tab naming nothing.
    if (at === null) {
      reportRemoved(entry.name)
      return
    }
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
    if (opening.size === 0) changes.clear()
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

  // A file on its way out is not one to write to. The delete below waits for the
  // saves it found travelling, and this is the other direction: a save started
  // while a delete is in flight would reach the hub in an order neither request
  // can see, and could put the file back after the user was told it was gone.
  //
  // The query runs *upwards* -- it is true when this path, or a directory above
  // it, has a delete in flight -- so a file inside a directory being deleted is
  // refused as well as the entry itself.
  if (deleting.isPending(tab.path)) {
    // What is known, and no more: a delete of this file, or of a directory above
    // it, has been sent. Whether it will succeed is not known here, and saying
    // the file "is being deleted" and then failing to delete it would be the
    // refusal explaining itself with something that did not happen.
    emit(
      'notice',
      `A delete of ${tab.name} or of a directory above it is in flight, so it was not saved.`,
      'warning',
    )
    return
  }

  // Only the newest save for a session may fold its result back in. Two saves in
  // flight (edit again while the first is travelling) can resolve out of order,
  // and the older answer would mark the tab clean at a text and a time that are
  // not what is on disk.
  const ticket = (saveTickets.get(tab.id) ?? 0) + 1
  saveTickets.set(tab.id, ticket)

  const request = beginSave(tab, force)
  saving.begin(request.path)
  try {
    const stamp = await saveOpenFile(request)
    if (!isSameTab(tab.id)) return
    if (saveTickets.get(tab.id) !== ticket) return
    const settled = settleSave(tabs.value, request, stamp, renaming.isPending(request.path))
    tabs.value = settled.files
    if (settled.outcome === 'raced') {
      emit(
        'notice',
        // Naming the directory as well as the file, because a rename of either is
        // what this branch covers: the check is by path and a path has ancestors.
        `A rename of ${tab.name} or of a directory above it was in flight while it was saved, ` +
          `so the answer was not recorded. Save again once the rename finishes.`,
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
  } finally {
    // The path the write named, not the tab's: a rename during the flight moved
    // the tab, and it is the write that was outstanding.
    saving.end(request.path)
  }
}

// changes records what happened to a path -- a rename that moved it, a delete
// that removed it -- for the reads that were travelling when it happened. See
// files/pathChanges.ts for why each record is scoped to the read rather than
// consulted wholesale.
const changes = createPathChanges()

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
  // A create is deliberately not ordered against an outstanding delete, unlike a
  // save: one that lands before the unlink has its empty file removed, and one
  // that lands after leaves exactly what the user asked for. Nothing of theirs is
  // lost either way -- though which of the two the screen is left showing is not
  // guaranteed, since the two listings race -- so there is nothing here for the
  // ordering to protect.
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
  changes.record(path, target)

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
  // A tab nobody has read was opened without reading, and its *name* is the
  // reason: an image goes to the preview, which fetches what it needs, and its
  // bytes are never decoded as text. A rename replaces the name and with it the
  // reason -- the file is the same file, and what it now claims to be is a guess
  // nothing has checked. So a tab that follows a rename to a name the editor
  // would hold is read at that name, and becomes whatever the hub says it is:
  // text to edit, or a file to download. Until the answer lands, `presentation`
  // keeps the unread tab out of the editor, which is what stops an empty
  // document from standing in front of bytes nobody read.
  //
  // Collected before the tabs are moved, because the first party that changes
  // the tab's name is the assignment below.
  const unread = tabs.value.flatMap((tab) => {
    const destination = moving.get(tab.id)
    if (destination === undefined) return []
    const renamed = tab.path === path ? name : tab.name
    if (tab.binary !== null || !wantsText(renamed, tab.size)) return []
    return [{ id: tab.id, path: destination }]
  })
  tabs.value = tabs.value
    .filter((tab) => moving.has(tab.id) || !taken.has(tab.path))
    .map((tab) => {
      const destination = moving.get(tab.id)
      if (destination === undefined) return tab
      return { ...tab, path: destination, name: tab.path === path ? name : tab.name }
    })
  for (const tab of unread) void readRenamedTab(tab.id, tab.path)

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

/** readRenamedTab reads the file a tab was opened on without reading, after a
 * rename gave it a name that asks to be decoded as text.
 *
 * The tab's session rather than its path identifies it, because what is being
 * filled in is the state of an editing session that followed the file. That
 * state is also what decides whether the answer is still wanted, and it is
 * checked before anything is written: only a tab that is still *unread* is
 * filled in. Two reads of one path can be in flight -- renamed to a text name,
 * renamed back to an image name, renamed to the text name again -- and the first
 * of them to land is what makes the tab editable. Letting a later answer replace
 * that would take the user's keystrokes with it and leave the tab reporting
 * itself saved against contents they never saw. The path is checked as well,
 * since a tab that has moved on names another file.
 *
 * What comes back replaces the contents, the observation, and the hub's
 * classification together, so the tab describes the file the hub read rather
 * than the name it was given. A hub that answers not_found leaves the tab as it
 * is: it says the file is not there, which the tab now shows as information,
 * and reopening it reports the same thing in the same words. */
async function readRenamedTab(id: number, path: string) {
  try {
    const contents = await readFile(path)
    const live = liveTab(id)
    if (!live || live.path !== path || live.binary !== null) return
    tabs.value = tabs.value.map((tab) =>
      tab.id === id
        ? {
            ...tab,
            text: contents.text,
            saved: contents.text,
            stamp: contents.stamp,
            binary: contents.binary,
          }
        : tab,
    )
  } catch (err) {
    report(err, `Could not open ${basename(path)}`)
  }
}

function renameTarget(entry: Entry, path: string) {
  const name = window.prompt('Rename to', entry.name)
  if (!name || name === entry.name) return
  const target = joinPath(dirname(path), name)
  renaming.begin(path)
  void renameEntry(path, target)
    .then(() => {
      retargetTabs(path, target, name)
      return settleMutation(path, target)
    })
    .catch((err: unknown) => report(err, `Could not rename ${entry.name}`))
    .finally(() => renaming.end(path))
}

async function deleteTarget(entry: Entry, path: string) {
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

  // The manager does not let the deletes it sends race the saves it sends, and
  // this is where that is decided. The hub's last check and the rename that
  // installs a write are two adjacent system calls -- no filesystem primitive
  // replaces a name only if it still holds what was there -- so a write landing
  // between them puts the file back, while the delete's own answer says the file
  // is gone. Nothing can order another process's write against this delete; the
  // manager can at least refuse to order its own that way.
  //
  // Waiting by *path* rather than by the tabs this is about to close is the
  // whole of it. The writes that can cross this delete are the ones naming the
  // entry or anything under it, and a tab is not a reliable way to find them: a
  // write outlives the tab it came from, so a file saved and then closed before
  // the delete was confirmed has no tab left to be found by, and the write is
  // still travelling. The record is keyed by the path the write named, so the
  // path is what asks.
  //
  // The marker goes up before the wait rather than after it, so a save started
  // while the wait is on is refused by save() instead of joining the queue: the
  // wait is for what was already travelling, and a later write is a separate
  // question the marker answers directly.
  //
  // The wait is bounded by the writes it is waiting for and by nothing else --
  // there is no timeout on these requests anywhere in this client. A write that
  // never answers therefore leaves the delete unsent, with the entry still
  // listed, which is the truth; the alternative is a delete sent into a race it
  // cannot see. It is worth being plain about the cost rather than calling that
  // transient: while the wait is outstanding the marker stays up, so saves of
  // that path are refused from then on and each retry of the delete adds another
  // waiter. A client where a write never answers is one where that write's tab
  // never stops being unsaved either; the way out of both is a reload.
  deleting.begin(path)
  try {
    await saving.idle(path)

    await deleteEntry(path, entry.is_dir)

    // The hub has answered that the entry is gone, and that is what the reads
    // still travelling need to know: one of them was granted before this delete
    // was sent, so it comes back with contents for a path that has been removed,
    // and it can land after the sweep below. The record is what makes that sweep
    // a rule rather than a moment. It is kept before the sweep rather than after,
    // because there is no await between the answer and this line: an answer that
    // arrives from here on finds it.
    changes.recordRemoval(path)

    // Filtered by path where the answer lands, not by the set captured before
    // the request: a tab opened while the delete travelled names a file that is
    // now gone, and would otherwise survive pointing at nothing. A tab renamed
    // out of the way during the flight no longer matches, and should survive.
    const removed = tabs.value.filter((tab) => tab.path === path || tab.path.startsWith(prefix))
    // The warning named what was dirty when the user answered. A tab that
    // appeared or was edited while the delete travelled was not in it -- and
    // the file is gone by now, so it is said rather than asked.
    const warned = new Set(unsaved.map((tab) => tab.id))
    const unwarned = removed.filter((tab) => isDirty(tab) && !warned.has(tab.id))
    if (unwarned.length > 0) {
      const names = unwarned.map((tab) => tab.name).join(', ')
      emit('notice', `Unsaved changes in ${names} went with ${entry.name}.`, 'warning')
    }

    tabs.value = tabs.value.filter((tab) => tab.path !== path && !tab.path.startsWith(prefix))
    if (!tabs.value.some((tab) => tab.path === activePath.value)) {
      activePath.value = tabs.value.length > 0 ? tabs.value[tabs.value.length - 1].path : null
    }
    await settleMutation(path)
  } catch (err) {
    report(err, `Could not delete ${entry.name}`)
  } finally {
    deleting.end(path)
  }
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
    emit(
      'notice',
      'Copy failed: this browser exposes no clipboard here. It needs a secure context, and not every browser provides one.',
      'warning',
    )
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
  closeMenu()
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
      void deleteTarget(target.entry, target.path)
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

function openMenu(event: MouseEvent, entry: Entry, path: string, opener: HTMLElement | null) {
  menu.value = { ...anchorFor(event, opener), entry, path, opener }
}

/** anchorFor is where a menu for this event should open.
 *
 * A press carries the pointer's own position, and the menu belongs there. A
 * contextmenu the browser raised for the keyboard -- Shift+F10, or the menu key
 * on a focused row -- carries no pointer at all: what it reports is the origin,
 * or wherever the mouse was last left, and a menu opened at either is a menu the
 * user has to go looking for. The row that raised it is the anchor in that case,
 * and the test is whether the event's position is inside the row: a press on the
 * row is inside by construction, so anything else is a position that does not
 * describe this press. */
function anchorFor(event: MouseEvent, opener: HTMLElement | null): { x: number; y: number } {
  if (opener === null) return { x: event.clientX, y: event.clientY }
  const box = opener.getBoundingClientRect()
  const inside =
    event.clientX >= box.left &&
    event.clientX <= box.right &&
    event.clientY >= box.top &&
    event.clientY <= box.bottom
  if (inside) return { x: event.clientX, y: event.clientY }
  // Under the row rather than over it, which is where a press on it would put a
  // menu and where the entry stays visible while the menu is up.
  return { x: box.left + 8, y: box.bottom }
}

/** closeMenu dismisses the menu, and hands the keyboard back to the row it was
 * opened from.
 *
 * The menu takes the focus when it opens, so it has to give it back: closing it
 * without that leaves the focus nowhere, and a keyboard user who opened it from
 * a row has to walk back to that row through every control between. The row may
 * be gone by now -- the action just taken may have deleted or renamed it -- in
 * which case there is nothing to focus and the browser's own default stands.
 *
 * preventScroll because a dismissal is not a navigation: the row is where the
 * user left it, and focusing it must not drag the tree back to it. */
function closeMenu() {
  const opener = menu.value?.opener ?? null
  menu.value = null
  if (opener !== null && opener.isConnected) opener.focus({ preventScroll: true })
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
  <div class="fm" @click.self="closeMenu()">
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
                @download="download(active.path, active.name)"
              />
              <MarkdownPreview
                v-else-if="preview === 'markdown' && !activeIsMarkdownSource"
                :source="active.text"
              />
              <div v-else-if="!editorVisible" class="fm__info">
                <p class="fm__state">{{ active.name }} is not shown here: {{ notShown }}.</p>
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
      @close="closeMenu"
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
