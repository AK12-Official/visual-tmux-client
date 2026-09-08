<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { marked } from 'marked'
import { notify } from '../toasts'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const html = ref<string | null>(null)
const loading = ref(false)

// The guide is static for the lifetime of the page: fetch once, cache the
// rendered HTML, and never re-fetch on subsequent opens.
let cachedHtml: string | null = null
let load: Promise<void> | null = null

async function ensureGuide(): Promise<void> {
  load ??= (async () => {
    try {
      const res = await fetch('/tmux-guide.zh-CN.md')
      if (!res.ok) throw new Error(`guide: ${res.status} ${res.statusText}`)
      const md = await res.text()
      cachedHtml = marked.parse(md, { async: false }) as string
    } catch (err) {
      load = null // a failed load may be retried on the next open
      throw err
    }
  })()
  await load
}

watch(
  () => props.open,
  async (open) => {
    if (!open || cachedHtml !== null) return
    loading.value = true
    try {
      await ensureGuide()
      html.value = cachedHtml
    } catch (err) {
      notify('error', err instanceof Error ? err.message : String(err))
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

function onKey(ev: KeyboardEvent): void {
  if (ev.key === 'Escape') emit('close')
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="open" class="help" @click.self="emit('close')">
    <div class="help__panel" role="dialog" aria-modal="true" aria-label="tmux 使用指南">
      <header class="help__header">
        <span class="help__title">tmux 使用指南</span>
        <button class="help__close" aria-label="close" @click="emit('close')">✕</button>
      </header>
      <div class="help__body">
        <p v-if="loading" class="help__loading">Loading…</p>
        <!-- The content is repo-controlled markdown fetched from the hub
             itself (no user input in the pipeline), so it is rendered as
             HTML directly. -->
        <div v-else-if="html" class="help__markdown" v-html="html"></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.help {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.6);
  z-index: 100;
}
.help__panel {
  display: flex;
  flex-direction: column;
  width: min(760px, 90vw);
  height: min(85vh, 900px);
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 10px;
  overflow: hidden;
}
.help__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 0.9rem;
  border-bottom: 1px solid var(--th-border);
}
.help__title {
  font-weight: 600;
  font-size: 0.95rem;
}
.help__close {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--th-text-mid);
  cursor: pointer;
  font-size: 0.9rem;
}
.help__close:hover {
  color: var(--th-text-hi);
}
.help__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0.5rem 1.25rem 1.5rem;
}
.help__loading {
  color: var(--th-text-lo);
}
/* v-html content is not reachable by scoped selectors; :deep() pierces it. */
.help__markdown {
  color: var(--th-text-hi);
  font-size: 0.88rem;
  line-height: 1.65;
}
.help__markdown :deep(h1) {
  font-size: 1.25rem;
  margin: 1rem 0 0.6rem;
}
.help__markdown :deep(h2) {
  font-size: 1.05rem;
  margin: 1.4rem 0 0.5rem;
  border-bottom: 1px solid var(--th-border);
  padding-bottom: 0.25rem;
}
.help__markdown :deep(h3) {
  font-size: 0.95rem;
  margin: 1rem 0 0.4rem;
}
.help__markdown :deep(p),
.help__markdown :deep(ul),
.help__markdown :deep(ol),
.help__markdown :deep(blockquote) {
  margin: 0.5rem 0;
}
.help__markdown :deep(blockquote) {
  border-left: 3px solid var(--th-border);
  margin: 0.6rem 0;
  padding: 0.1rem 0 0.1rem 0.8rem;
  color: var(--th-text-mid);
}
.help__markdown :deep(table) {
  border-collapse: collapse;
  margin: 0.7rem 0;
  max-width: 100%;
  display: block;
  overflow-x: auto;
}
.help__markdown :deep(th),
.help__markdown :deep(td) {
  border: 1px solid var(--th-border);
  padding: 0.3rem 0.6rem;
  text-align: left;
  vertical-align: top;
}
.help__markdown :deep(th) {
  background: var(--th-raised);
}
.help__markdown :deep(code) {
  font-family: var(--app-code-font);
  font-size: 0.82rem;
  background: var(--th-raised);
  border-radius: 4px;
  padding: 0.08rem 0.3rem;
}
.help__markdown :deep(pre) {
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.6rem 0.8rem;
  overflow-x: auto;
}
.help__markdown :deep(pre code) {
  background: none;
  padding: 0;
}
.help__markdown :deep(a) {
  color: var(--th-accent);
}
.help__markdown :deep(hr) {
  border: none;
  border-top: 1px solid var(--th-border);
  margin: 1.2rem 0;
}
</style>
