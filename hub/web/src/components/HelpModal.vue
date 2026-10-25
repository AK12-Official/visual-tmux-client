<script lang="ts">
// Per-language guide caches and pending loads.
const cachedHtml: Record<string, string> = {}
const loads: Record<string, Promise<string>> = {}
let currentRequestId = 0

export function resetGuideCacheForTest(): void {
  for (const k of Object.keys(cachedHtml)) {
    delete cachedHtml[k]
  }
  for (const k of Object.keys(loads)) {
    delete loads[k]
  }
  currentRequestId = 0
}

export function getCachedGuideForTest(locale: string): string | undefined {
  return cachedHtml[locale]
}
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { notify } from '../toasts'
import { t, getLocale, onLocaleChange, type SupportedLocale } from '../i18n/index'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const currentLocale = ref<SupportedLocale>(getLocale())

const guideTitle = computed(() => {
  void currentLocale.value
  return t('help.guideTitle')
})
const closeLabel = computed(() => {
  void currentLocale.value
  return t('help.close')
})
const loadingText = computed(() => {
  void currentLocale.value
  return t('help.loading')
})

const html = ref<string | null>(null)
const loading = ref(false)
const panel = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previousFocus: HTMLElement | null = null
let unbindLocale: (() => void) | null = null

async function fetchAndRenderGuide(targetLocale: SupportedLocale): Promise<string> {
  const url = targetLocale === 'zh-CN' ? '/tmux-guide.zh-CN.md' : '/tmux-guide.en.md'
  const [{ marked }, res] = await Promise.all([
    import('marked'),
    fetch(url),
  ])
  if (!res.ok) throw new Error(`guide: ${res.status} ${res.statusText}`)
  const md = await res.text()
  return marked.parse(md, { async: false }) as string
}

async function loadGuideForLocale(targetLocale: SupportedLocale): Promise<void> {
  if (cachedHtml[targetLocale]) {
    html.value = cachedHtml[targetLocale]
    loading.value = false
    return
  }

  const reqId = ++currentRequestId
  loading.value = true
  html.value = null

  try {
    if (!loads[targetLocale]) {
      loads[targetLocale] = fetchAndRenderGuide(targetLocale).then(
        (rendered) => {
          cachedHtml[targetLocale] = rendered
          delete loads[targetLocale]
          return rendered
        },
        (err) => {
          delete loads[targetLocale]
          throw err
        },
      )
    }
    const rendered = await loads[targetLocale]
    if (reqId === currentRequestId && props.open && currentLocale.value === targetLocale) {
      html.value = rendered
    }
  } catch (err) {
    if (reqId === currentRequestId && props.open && currentLocale.value === targetLocale) {
      notify('error', err instanceof Error ? err.message : String(err))
    }
  } finally {
    if (reqId === currentRequestId && props.open && currentLocale.value === targetLocale) {
      loading.value = false
    }
  }
}

watch(
  [() => props.open, currentLocale],
  async ([open, loc], [prevOpen]) => {
    if (!open) {
      if (prevOpen) {
        previousFocus?.focus()
        previousFocus = null
      }
      return
    }
    if (!prevOpen) {
      previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      await nextTick()
      closeButton.value?.focus()
    }
    await loadGuideForLocale(loc)
  },
  { immediate: true },
)

function onKey(ev: KeyboardEvent): void {
  if (props.open && ev.key === 'Escape') emit('close')
}

function trapFocus(ev: KeyboardEvent): void {
  if (ev.key !== 'Tab' || !panel.value) return
  const focusable = Array.from(
    panel.value.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  )
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (ev.shiftKey && document.activeElement === first) {
    ev.preventDefault()
    last.focus()
  } else if (!ev.shiftKey && document.activeElement === last) {
    ev.preventDefault()
    first.focus()
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  unbindLocale = onLocaleChange((loc) => {
    currentLocale.value = loc
  })
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  unbindLocale?.()
})

defineExpose({
  html,
  loading,
  currentLocale,
  resetGuideCacheForTest,
})
</script>

<template>
  <div v-if="open" class="help" @click.self="emit('close')">
    <div
      ref="panel"
      class="help__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="help-title"
      @keydown="trapFocus"
    >
      <header class="help__header">
        <h2 id="help-title" class="help__title">{{ guideTitle }}</h2>
        <button ref="closeButton" type="button" class="help__close" :aria-label="closeLabel" @click="emit('close')">
          <span aria-hidden="true">✕</span>
        </button>
      </header>
      <div class="help__body" :aria-busy="loading">
        <p v-if="loading" class="help__loading" role="status">{{ loadingText }}</p>
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
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
  overflow: hidden;
}
.help__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--th-border);
}
.help__title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--th-text);
}
.help__close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  color: var(--th-muted);
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
}
.help__close:hover {
  color: var(--th-text);
  border-color: var(--th-border);
}
.help__body {
  flex: 1;
  overflow-y: auto;
  padding: 16px 24px;
  color: var(--th-text);
  font-size: 13px;
  line-height: 1.6;
}
.help__loading {
  margin: 24px 0;
  text-align: center;
  color: var(--th-muted);
}
.help__markdown :deep(h1) {
  margin: 0 0 16px;
  font-size: 20px;
  font-weight: 600;
  border-bottom: 1px solid var(--th-border);
  padding-bottom: 8px;
}
.help__markdown :deep(h2) {
  margin: 24px 0 12px;
  font-size: 15px;
  font-weight: 600;
  color: var(--th-accent, #68d391);
}
.help__markdown :deep(h3) {
  margin: 16px 0 8px;
  font-size: 13px;
  font-weight: 600;
}
.help__markdown :deep(p) {
  margin: 8px 0;
}
.help__markdown :deep(blockquote) {
  margin: 8px 0;
  padding: 6px 12px;
  border-left: 3px solid var(--th-accent, #68d391);
  background: var(--th-panel);
  color: var(--th-muted);
}
.help__markdown :deep(blockquote p) {
  margin: 2px 0;
}
.help__markdown :deep(table) {
  width: 100%;
  border-collapse: collapse;
  margin: 12px 0;
  font-size: 12px;
}
.help__markdown :deep(th),
.help__markdown :deep(td) {
  padding: 6px 10px;
  text-align: left;
  border: 1px solid var(--th-border);
}
.help__markdown :deep(th) {
  background: var(--th-panel);
  font-weight: 600;
}
.help__markdown :deep(tr:nth-child(even) td) {
  background: rgba(255, 255, 255, 0.02);
}
.help__markdown :deep(code) {
  padding: 1px 4px;
  background: var(--th-panel);
  border: 1px solid var(--th-border);
  border-radius: 3px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
}
.help__markdown :deep(pre) {
  margin: 8px 0;
  padding: 10px 12px;
  background: var(--th-panel);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  overflow-x: auto;
}
.help__markdown :deep(pre code) {
  padding: 0;
  background: transparent;
  border: none;
}
.help__markdown :deep(ul),
.help__markdown :deep(ol) {
  margin: 8px 0;
  padding-left: 20px;
}
.help__markdown :deep(li) {
  margin: 3px 0;
}
.help__markdown :deep(hr) {
  border: none;
  border-top: 1px solid var(--th-border);
  margin: 20px 0;
}
.help__markdown :deep(a) {
  color: var(--th-accent, #68d391);
  text-decoration: none;
}
.help__markdown :deep(a:hover) {
  text-decoration: underline;
}
</style>
