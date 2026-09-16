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
  readFile,
  renameEntry,
  type Entry,
  type StartDirectory,
} from '../../files/api'
import { basename, dirname, joinPath, quoteForShell } from '../../files/pathUtils'
import { choosePreview } from '../../files/preview'
import { applySaved, anyDirty, isDirty, saveOpenFile, type OpenFile } from '../../files/tabs'
import { createTreeState, forgetDirectory, isExpanded, loadDirectory } from '../../files/tree'
import CodeEditor from './CodeEditor.vue'
import EditorTabs from './EditorTabs.vue'
import FileContextMenu from './FileContextMenu.vue'
import FileTree from './FileTree.vue'
import ImagePreview from './ImagePreview.vue'
import MarkdownPreview from './MarkdownPreview.vue'

const props = defineProps<{ session: string }>()
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

const active = computed(() => tabs.value.find((tab) => tab.path === activePath.value) ?? null)
const dirty = computed(() => anyDirty(tabs.value))
const preview = computed(() => (active.value ? choosePreview(active.value.name, active.value.size) : null))
const activeIsMarkdownSource = computed(
  () => active.value !== null && (markdownView.get(active.value.path) ?? 'source') === 'source',
)

watch(dirty, (value) => emit('dirty-change', value), { immediate: true })

function report(err: unknown, action: string) {
  const detail = err instanceof FileApiError ? err.code : String(err)
  emit('notice', `${action}: ${detail}`, 'error')
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

/** goTo loads a directory and makes it the current one.
 *
 * A refused navigation must not throw away what the user is looking at: stepping
 * up out of a configured boundary is an ordinary thing to try, and it should
 * answer with a reason while leaving the listing in place. Only a failure with
 * nothing on screen yet takes over the panel, because then the reason is all
 * there is to show. */
async function goTo(path: string) {
  loading.value = true
  navError.value = ''
  try {
    await loadDirectory(tree, path, true)
    current.value = path
    failure.value = ''
  } catch (err) {
    const detail = err instanceof FileApiError ? err.code : String(err)
    if (current.value === '') {
      failure.value = `${path} could not be opened (${detail}).`
    } else {
      navError.value = `${path} could not be opened (${detail}).`
    }
  } finally {
    loading.value = false
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
      if (code !== 'not_found') {
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
  const existing = tabs.value.find((tab) => tab.path === path)
  if (existing) {
    activePath.value = path
    return
  }

  const kind = choosePreview(entry.name, entry.size)
  if (kind === 'info' || kind === 'image') {
    tabs.value = [
      ...tabs.value,
      { path, name: entry.name, text: '', saved: '', mtime: entry.mtime, size: entry.size },
    ]
    activePath.value = path
    return
  }

  try {
    const contents = await readFile(path)
    tabs.value = [
      ...tabs.value,
      {
        path,
        name: entry.name,
        text: contents.text,
        saved: contents.text,
        mtime: contents.mtime,
        size: entry.size,
      },
    ]
    activePath.value = path
  } catch (err) {
    report(err, `Could not open ${entry.name}`)
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

async function save(force = false) {
  const tab = active.value
  if (!tab) return
  try {
    const mtime = await saveOpenFile(tab, force)
    tabs.value = tabs.value.map((candidate) =>
      candidate.path === tab.path ? applySaved(candidate, mtime) : candidate,
    )
  } catch (err) {
    if (err instanceof FileApiError && err.code === 'conflict') {
      const overwrite = window.confirm(
        `${tab.name} changed on disk since it was opened. Overwrite it with your version?`,
      )
      if (overwrite) await save(true)
      return
    }
    report(err, `Could not save ${tab.name}`)
  }
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

function refreshCurrent() {
  if (current.value === '') return
  forgetDirectory(tree, current.value)
  void goTo(current.value)
}

async function createHere(isDir: boolean) {
  const name = window.prompt(isDir ? 'New directory name' : 'New file name')
  if (!name) return
  const path = joinPath(current.value, name)
  try {
    await createEntry(path, isDir)
    forgetDirectory(tree, current.value)
    await goTo(current.value)
    if (!isDir) await openFile({ name, is_dir: false, size: 0, mtime: 0 }, path)
  } catch (err) {
    report(err, `Could not create ${name}`)
  }
}

function renameTarget(entry: Entry, path: string) {
  const name = window.prompt('Rename to', entry.name)
  if (!name || name === entry.name) return
  void renameEntry(path, joinPath(dirname(path), name))
    .then(() => {
      forgetDirectory(tree, dirname(path))
      return goTo(current.value)
    })
    .catch((err: unknown) => report(err, `Could not rename ${entry.name}`))
}

function deleteTarget(entry: Entry, path: string) {
  const kind = entry.is_dir ? 'directory' : 'file'
  if (!window.confirm(`Delete the ${kind} ${entry.name}? This cannot be undone.`)) {
    return
  }
  void deleteEntry(path, entry.is_dir)
    .then(() => {
      tabs.value = tabs.value.filter((tab) => tab.path !== path)
      forgetDirectory(tree, dirname(path))
      return goTo(current.value)
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
    emit('notice', 'That path cannot be inserted: it contains a line terminator.', 'warning')
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
  if (event.key === 'Escape' && menu.value === null) requestClose()
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
      <button class="fm__btn" type="button" title="new file" @click="createHere(false)">+ file</button>
      <button class="fm__btn" type="button" title="new directory" @click="createHere(true)">+ dir</button>
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
            <ImagePreview
              v-if="preview === 'image'"
              :path="active.path"
              @notice="(text, level) => emit('notice', text, level)"
            />
            <MarkdownPreview v-else-if="preview === 'markdown' && !activeIsMarkdownSource" :source="active.text" />
            <CodeEditor
              v-else-if="preview === 'editor' || preview === 'markdown'"
              v-model="active.text"
            />
            <div v-else class="fm__info">
              <p class="fm__state">
                {{ active.name }} is not shown here: it is
                {{ preview === 'info' ? 'binary or too large to preview' : 'not previewable' }}.
              </p>
              <p class="fm__meta">{{ formatSize(active.size) }} · {{ active.path }}</p>
              <button class="fm__btn" type="button" @click="download(active.path, active.name)">
                Download
              </button>
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
