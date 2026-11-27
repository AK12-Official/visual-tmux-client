<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { changeHostSettings, getHostSettings, type HostSettings } from '../api'
import { notify } from '../toasts'
import { getLocale, onLocaleChange, t, type SupportedLocale } from '../i18n/index'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const panel = ref<HTMLElement | null>(null)
const proxyInput = ref<HTMLInputElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previousFocus: HTMLElement | null = null

const saved = ref<HostSettings | null>(null)
const proxyAddress = ref('')
const loading = ref(false)
const loadError = ref(false)
const saving = ref(false)
// The error under the field, kept as a key so it follows a language change.
const fieldError = ref<string | null>(null)
const generalError = ref<{ key: string } | { raw: string } | null>(null)

const currentLocale = ref<SupportedLocale>(getLocale())
let unlistenLocale: (() => void) | null = null

const fieldErrorText = computed(() => {
  void currentLocale.value
  return fieldError.value ? t(fieldError.value) : ''
})

const generalErrorText = computed(() => {
  void currentLocale.value
  if (!generalError.value) return ''
  return 'key' in generalError.value ? t(generalError.value.key) : generalError.value.raw
})

// One sentence under the field. With a saved proxy it names that proxy, so the
// dialog always says which route is in effect; otherwise it explains the field.
const hintText = computed(() => {
  void currentLocale.value
  const proxy = saved.value?.proxyAddress ?? ''
  return proxy ? t('settings.proxyHintProxy', { proxy }) : t('settings.proxyHintDirect')
})

// A quick check that spares a round trip for an obvious typo. The hub remains
// the authority and answers with the same message for anything this lets through.
const PROXY_ADDRESS = /^(?:socks5:\/\/)?(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+):(\d{1,5})$/

function looksLikeProxyAddress(value: string): boolean {
  const match = PROXY_ADDRESS.exec(value)
  if (!match) return false
  const port = Number(match[1])
  return port >= 1 && port <= 65535
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = false
  try {
    const settings = await getHostSettings()
    saved.value = settings
    proxyAddress.value = settings.proxyAddress ?? ''
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
}

async function onSave(): Promise<void> {
  if (saving.value || !saved.value) return
  const value = proxyAddress.value.trim()
  generalError.value = null
  fieldError.value = null
  if (value && !looksLikeProxyAddress(value)) {
    fieldError.value = 'settings.invalidProxyAddress'
    proxyInput.value?.focus()
    return
  }

  saving.value = true
  try {
    const updated = await changeHostSettings({ proxyAddress: value }, saved.value.revision)
    saved.value = updated
    proxyAddress.value = updated.proxyAddress ?? ''
    notify('info', t('settings.saved'))
    emit('close')
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('host_revision_conflict')) {
      // Someone else changed the settings. Take their revision and keep what
      // was typed, so the user can look at the current value and save again.
      generalError.value = { key: 'settings.revisionConflict' }
      try {
        saved.value = await getHostSettings()
      } catch {
        generalError.value = { key: 'settings.loadFailed' }
      }
      return
    }
    if (msg.includes('invalid_proxy_address')) {
      fieldError.value = 'settings.invalidProxyAddress'
      proxyInput.value?.focus()
      return
    }
    generalError.value = { key: 'settings.saveFailed' }
  } finally {
    saving.value = false
  }
}

watch(
  () => props.open,
  async (open, wasOpen) => {
    if (!open) {
      if (wasOpen) {
        previousFocus?.focus()
        previousFocus = null
      }
      return
    }
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    generalError.value = null
    fieldError.value = null
    saved.value = null
    proxyAddress.value = ''
    await nextTick()
    closeButton.value?.focus()
    await load()
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
      'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
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
  unlistenLocale = onLocaleChange((locale) => {
    currentLocale.value = locale
  })
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  unlistenLocale?.()
})

defineExpose({ saved, proxyAddress, loading, loadError, saving, fieldError, generalError })
</script>

<template>
  <div v-if="open" class="settings-modal" @click.self="emit('close')">
    <div
      ref="panel"
      class="settings-modal__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
      @keydown="trapFocus"
    >
      <header class="settings-modal__header">
        <h2 id="settings-title" class="settings-modal__title">{{ t('settings.title') }}</h2>
        <button
          ref="closeButton"
          type="button"
          class="settings-modal__close"
          :aria-label="t('settings.close')"
          @click="emit('close')"
        >
          ✕
        </button>
      </header>

      <div class="settings-modal__body">
        <p v-if="loading" class="settings-modal__state" role="status">{{ t('common.loading') }}</p>

        <div v-else-if="loadError" class="settings-alert" role="alert">
          <span>{{ t('settings.loadFailed') }}</span>
          <button type="button" class="settings-btn settings-btn--subtle" @click="load">
            {{ t('common.retry') }}
          </button>
        </div>

        <form v-else class="settings-form" novalidate @submit.prevent="onSave">
          <p v-if="generalErrorText" class="settings-alert" role="alert">{{ generalErrorText }}</p>

          <section class="settings-section">
            <h3 class="settings-section__title">{{ t('settings.connectionSection') }}</h3>
            <div class="settings-field">
              <label for="settings-proxy-address" class="settings-label">{{ t('settings.proxyLabel') }}</label>
              <input
                id="settings-proxy-address"
                ref="proxyInput"
                v-model="proxyAddress"
                type="text"
                class="settings-input"
                :class="{ 'settings-input--error': fieldErrorText }"
                :placeholder="t('settings.proxyPlaceholder')"
                :aria-invalid="fieldErrorText ? 'true' : undefined"
                :aria-describedby="fieldErrorText ? 'settings-proxy-error settings-proxy-help' : 'settings-proxy-help'"
                autocomplete="off"
                spellcheck="false"
              />
              <span v-if="fieldErrorText" id="settings-proxy-error" class="settings-field-error" role="alert">
                {{ fieldErrorText }}
              </span>
            </div>
            <p id="settings-proxy-help" class="settings-field-hint">{{ hintText }}</p>
            <p v-if="proxyAddress.trim()" id="settings-proxy-shell-hint" class="settings-field-hint">
              {{ t('settings.proxyShellOverrideHint') }}
            </p>
          </section>

          <div class="settings-form__actions">
            <button type="button" class="settings-btn settings-btn--subtle" :disabled="saving" @click="emit('close')">
              {{ t('common.cancel') }}
            </button>
            <button type="submit" class="settings-btn settings-btn--primary" :disabled="saving">
              {{ saving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-modal {
  position: fixed;
  inset: 0;
  z-index: 90;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}
.settings-modal__panel {
  width: 100%;
  max-width: 520px;
  max-height: 90vh;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.5);
  overflow: hidden;
}
.settings-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--th-border);
  background: var(--th-raised);
}
.settings-modal__title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--th-text-hi);
}
.settings-modal__close {
  background: transparent;
  border: none;
  color: var(--th-text-mid);
  font-size: 1.1rem;
  cursor: pointer;
  padding: 0.2rem 0.5rem;
  border-radius: 4px;
}
.settings-modal__close:hover {
  color: var(--th-text-hi);
  background: rgba(255, 255, 255, 0.08);
}
.settings-modal__body {
  padding: 1rem;
  overflow-y: auto;
}
.settings-modal__state {
  margin: 1.5rem 0;
  text-align: center;
  color: var(--th-text-lo);
  font-size: 0.85rem;
}
.settings-section {
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.75rem;
}
.settings-section__title {
  font-size: 0.85rem;
  margin: 0 0 0.6rem 0;
  color: var(--th-text-hi);
  font-weight: 600;
}
.settings-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.settings-label {
  font-size: 0.8rem;
  color: var(--th-text-mid);
  font-weight: 500;
}
.settings-input {
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  color: var(--th-text-hi);
  padding: 0.4rem 0.6rem;
  font-size: 0.85rem;
  font-family: var(--app-code-font);
}
/* The fixed control height leaves no room for vertical padding. */
.settings-input {
  padding-top: 0;
  padding-bottom: 0;
}
.settings-input:focus {
  border-color: var(--th-focus, var(--th-accent));
  outline: none;
}
.settings-input--error {
  border-color: var(--th-danger);
}
.settings-field-error {
  font-size: 0.75rem;
  color: var(--th-danger);
}
/* Explanations are prose: they wrap, and only what follows them moves. */
.settings-field-hint {
  display: block;
  margin: 0.4rem 0 0;
  font-size: 0.76rem;
  line-height: 1.45;
  color: var(--th-text-lo);
}
.settings-alert {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 0.75rem;
  border-radius: 4px;
  font-size: 0.8rem;
  line-height: 1.45;
  margin-bottom: 0.75rem;
  background: rgba(248, 113, 113, 0.12);
  color: var(--th-danger);
  border: 1px solid rgba(248, 113, 113, 0.3);
}
.settings-form__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  margin-top: 1rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--th-border);
}
.settings-btn {
  padding: 0.35rem 0.75rem;
  border-radius: 4px;
  font-size: 0.82rem;
  cursor: pointer;
  border: 1px solid transparent;
  font-family: var(--app-ui-font);
}
.settings-btn--primary {
  background: var(--th-accent);
  color: #000;
  font-weight: 600;
}
.settings-btn--primary:hover:not(:disabled) {
  opacity: 0.9;
}
.settings-btn--subtle {
  background: var(--th-raised);
  border-color: var(--th-border);
  color: var(--th-text-hi);
}
.settings-btn--subtle:hover:not(:disabled) {
  border-color: var(--th-text-mid);
}
.settings-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
