<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  confirmHostTrust,
  createHost,
  deleteHost,
  getHostSettings,
  listHosts,
  probeDraftHost,
  probeExistingHost,
  testHostCapabilities,
  updateHost,
  updateHostSettings,
  type CapabilityStatus,
  type ConnectionType,
  type HostCapabilities,
  type HostInput,
  type HostItem,
  type HostSettings,
  type ProbeResult,
} from '../api'
import { notify } from '../toasts'
import { getLocale, onLocaleChange, t, type SupportedLocale } from '../i18n/index'

const props = defineProps<{
  open: boolean
  initialHostId?: string | null
  dirtyHosts?: string[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'hosts-changed'): void
  (e: 'default-changed', id: string | null): void
  (e: 'host-deleted', id: string): void
  (e: 'select-host', id: string): void
}>()

const panel = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previousFocus: HTMLElement | null = null

type ViewMode = 'list' | 'add' | 'edit'
const view = ref<ViewMode>('list')

const hosts = ref<HostItem[]>([])
const hostSettings = ref<HostSettings | null>(null)
const settingDefault = ref<string | null>(null)
const loading = ref(false)
const listError = ref('')
const activeCapabilities = ref<Record<string, HostCapabilities | 'testing' | null>>({})

// Form state
const formId = ref('')
const formConnectionType = ref<ConnectionType>('ssh')
const formName = ref('')
const formAddress = ref('')
const formPort = ref(22)
const formUsername = ref('')
const formCredentialType = ref<'password' | 'private_key'>('password')
const formPassword = ref('')
const formPrivateKey = ref('')
const formPassphrase = ref('')
const formChallengeToken = ref('')
const formEditRevision = ref<number | null>(null)
type FieldErrorValue = string | { key: string; params?: Record<string, unknown> }
const formFieldErrors = ref<Record<string, FieldErrorValue>>({})
const formGeneralError = ref<{ key: string; params?: Record<string, unknown> } | { raw: string } | null>(null)
const formSubmitting = ref(false)

const currentLocale = ref<SupportedLocale>(getLocale())
let unlistenLocale: (() => void) | null = null

const localizedFieldErrors = computed<Record<string, string>>(() => {
  void currentLocale.value
  const res: Record<string, string> = {}
  for (const [k, v] of Object.entries(formFieldErrors.value)) {
    if (!v) continue
    if (typeof v === 'string') {
      if (v.startsWith('hosts.') || v.startsWith('errors.') || v.startsWith('common.') || v.startsWith('auth.')) {
        res[k] = t(v)
      } else {
        res[k] = v
      }
    } else {
      res[k] = t(v.key, v.params)
    }
  }
  return res
})

const displayGeneralError = computed(() => {
  void currentLocale.value
  if (!formGeneralError.value) return ''
  if ('key' in formGeneralError.value) {
    return t(formGeneralError.value.key, formGeneralError.value.params)
  }
  return formGeneralError.value.raw
})

// Probing state
const probing = ref(false)
const confirmingTrust = ref(false)
const probedResult = ref<ProbeResult | null>(null)
const probeError = ref<{ key: string; params?: Record<string, unknown> } | { raw: string } | null>(null)
const trustConfirmed = ref(false)

const displayProbeError = computed(() => {
  void currentLocale.value
  if (!probeError.value) return ''
  if ('key' in probeError.value) {
    return t(probeError.value.key, probeError.value.params)
  }
  return probeError.value.raw
})

// Deletion confirmation state
const confirmingDeleteHost = ref<HostItem | null>(null)
const deleteLoading = ref(false)

function isLocal(item: HostItem): boolean {
  return item.connectionType === 'local' || (!item.connectionType && item.id === 'local')
}

function isCapAvailable(cap: CapabilityStatus | boolean | undefined): boolean {
  if (typeof cap === 'boolean') return cap
  if (!cap) return false
  return cap.state === 'available' || cap.available === true
}

function isCapNotApplicable(cap: CapabilityStatus | boolean | undefined): boolean {
  if (typeof cap === 'boolean' || !cap) return false
  return cap.state === 'not_applicable'
}

function getCapPillClass(cap: CapabilityStatus | boolean | undefined): string {
  if (isCapNotApplicable(cap)) return 'host-cap-pill--na'
  return isCapAvailable(cap) ? 'host-cap-pill--ok' : 'host-cap-pill--fail'
}

function getCapLabel(cap: CapabilityStatus | boolean | undefined, kind: 'SSH' | 'tmux' | 'SFTP'): string {
  if (kind === 'SSH') {
    if (isCapNotApplicable(cap)) return t('hosts.sshNA')
    return isCapAvailable(cap) ? t('hosts.sshConnected') : t('hosts.sshOffline')
  }
  if (kind === 'tmux') {
    return isCapAvailable(cap) ? t('hosts.tmuxAvailable') : t('hosts.tmuxMissing')
  }
  if (isCapNotApplicable(cap)) return t('hosts.sftpNA')
  return isCapAvailable(cap) ? t('hosts.sftpAvailable') : t('hosts.sftpUnavailable')
}

function shouldShowAtomicOverwrite(caps: HostCapabilities): boolean {
  if (caps.sftp !== undefined) {
    if (isCapNotApplicable(caps.sftp)) return false
    return isCapAvailable(caps.sftp)
  }
  return false
}

async function fetchHosts(): Promise<void> {
  loading.value = true
  listError.value = ''
  try {
    const [fetchedHosts, fetchedSettings] = await Promise.all([
      listHosts(),
      getHostSettings().catch(() => null),
    ])
    hosts.value = fetchedHosts
    hostSettings.value = fetchedSettings ?? { defaultHostId: null, revision: 1 }
    emit('hosts-changed')
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function onSetDefault(item: HostItem): Promise<void> {
  if (!hostSettings.value) return
  settingDefault.value = item.id
  try {
    const updated = await updateHostSettings(item.id, hostSettings.value.revision)
    hostSettings.value = updated
    notify('info', t('hosts.hostDefaultSuccess', { name: item.name }))
    emit('default-changed', item.id)
  } catch (err) {
    notify('error', t('hosts.hostDefaultFailed', { error: err instanceof Error ? err.message : String(err) }))
  } finally {
    settingDefault.value = null
  }
}

function resetForm(): void {
  formId.value = ''
  formConnectionType.value = 'ssh'
  formName.value = ''
  formAddress.value = ''
  formPort.value = 22
  formUsername.value = ''
  formCredentialType.value = 'password'
  formPassword.value = ''
  formPrivateKey.value = ''
  formPassphrase.value = ''
  formChallengeToken.value = ''
  formEditRevision.value = null
  formFieldErrors.value = {}
  formGeneralError.value = null
  probing.value = false
  probedResult.value = null
  probeError.value = null
  trustConfirmed.value = false
  formSubmitting.value = false
}

function startAdd(): void {
  resetForm()
  view.value = 'add'
}

function startEdit(item: HostItem): void {
  resetForm()
  formId.value = item.id
  formConnectionType.value = item.connectionType ?? (item.id === 'local' ? 'local' : 'ssh')
  formName.value = item.name
  formAddress.value = item.address ?? ''
  formPort.value = item.port ?? 22
  formUsername.value = item.username ?? ''
  formCredentialType.value = item.credentialType === 'private_key' ? 'private_key' : 'password'
  // Passwords and private keys are intentionally left blank so existing secrets are retained unless overwritten
  formPassword.value = ''
  formPrivateKey.value = ''
  formPassphrase.value = ''
  formEditRevision.value = item.revision
  view.value = 'edit'
}

function cancelForm(): void {
  resetForm()
  view.value = 'list'
}

function formatProbeError(msg: string): { key: string; params?: Record<string, unknown> } | { raw: string } {
  if (msg.includes('probing_disabled')) return { key: 'hosts.probingDisabled' }
  if (msg.includes('challenge_expired')) return { key: 'hosts.challengeExpired', params: { error: msg } }
  if (msg.includes('challenge_mismatch')) return { key: 'hosts.challengeMismatch', params: { error: msg } }
  if (msg.includes('cannot probe local host')) return { key: 'hosts.cannotProbeLocal' }
  if (msg.includes('probe_failed')) return { key: 'hosts.probeFailed', params: { error: msg } }
  return { raw: msg }
}

function formatHostSubmitError(msg: string): { key: string; params?: Record<string, unknown> } | { raw: string } {
  if (msg.includes('duplicate_local_host')) {
    return { key: 'hosts.duplicateLocalHost' }
  }
  if (msg.includes('host_revision_conflict')) {
    return { key: 'hosts.revisionConflict' }
  }
  if (msg.includes('host_not_found')) {
    return { key: 'terminal.hostNotFoundNotice' }
  }
  if (msg.includes('challenge_expired')) {
    return { key: 'hosts.challengeExpired', params: { error: msg } }
  }
  if (msg.includes('challenge_mismatch')) {
    return { key: 'hosts.challengeMismatch', params: { error: msg } }
  }
  if (msg.includes('host_management_disabled')) {
    return { key: 'hosts.hostManagementDisabled' }
  }
  if (msg.includes('host_unavailable')) {
    return { key: 'errors.host_unavailable' }
  }
  if (msg.includes('host_unsupported')) {
    return { key: 'errors.host_unsupported' }
  }
  if (msg.includes('invalid_request')) {
    return { key: 'errors.invalid_request' }
  }
  return { key: 'hosts.submitFailedWithDiagnostic', params: { diagnostic: msg } }
}

async function onProbe(): Promise<void> {
  if (confirmingTrust.value || formSubmitting.value) return
  probeError.value = null
  probedResult.value = null
  trustConfirmed.value = false

  const addr = formAddress.value.trim()
  if (!addr) {
    formFieldErrors.value = { ...formFieldErrors.value, address: 'hosts.addressRequired' }
    return
  }
  const port = Number(formPort.value)
  if (!port || port < 1 || port > 65535) {
    formFieldErrors.value = { ...formFieldErrors.value, port: 'hosts.portRange' }
    return
  }

  probing.value = true
  try {
    if (view.value === 'edit' && formId.value) {
      const res = await probeExistingHost(formId.value)
      probedResult.value = res
      formChallengeToken.value = res.challengeToken
    } else {
      const res = await probeDraftHost(addr, port)
      probedResult.value = res
      formChallengeToken.value = res.challengeToken
    }
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    probeError.value = formatProbeError(msg)
  } finally {
    probing.value = false
  }
}

async function onConfirmTrust(): Promise<void> {
  if (!probedResult.value || confirmingTrust.value || formSubmitting.value) return
  probeError.value = null
  if (view.value === 'edit' && formId.value && formChallengeToken.value) {
    const hostId = formId.value
    const challengeToken = formChallengeToken.value
    confirmingTrust.value = true
    try {
      const updated = await confirmHostTrust(hostId, challengeToken)
      // Trust is a persisted edit: subsequent saves must use its new revision.
      // Do not replace the user's unsaved fields with the returned host record.
      if (formId.value !== hostId || formChallengeToken.value !== challengeToken) return
      formEditRevision.value = updated.revision
      formChallengeToken.value = '' // The server has consumed this challenge.
      trustConfirmed.value = true
      notify('info', t('hosts.fingerprintTrustedNotice'))
      await fetchHosts()
    } catch (err) {
      if (formId.value === hostId && formChallengeToken.value === challengeToken) {
        const msg = err instanceof Error ? err.message : String(err)
        probeError.value = formatProbeError(msg)
      }
    } finally {
      confirmingTrust.value = false
    }
  } else {
    trustConfirmed.value = true
  }
}

async function onTestCapabilities(item: HostItem): Promise<void> {
  activeCapabilities.value = { ...activeCapabilities.value, [item.id]: 'testing' }
  try {
    const caps = await testHostCapabilities(item.id)
    activeCapabilities.value = { ...activeCapabilities.value, [item.id]: caps }
  } catch (err) {
    activeCapabilities.value = {
      ...activeCapabilities.value,
      [item.id]: {
        ssh: false,
        tmux: false,
        sftp: false,
        posixRename: false,
        error: err instanceof Error ? err.message : String(err),
      },
    }
  }
}

const hasDirtyDrafts = computed(() => {
  if (!confirmingDeleteHost.value) return false
  return props.dirtyHosts?.includes(confirmingDeleteHost.value.id) ?? false
})

function promptDelete(item: HostItem): void {
  confirmingDeleteHost.value = item
}

async function confirmDelete(): Promise<void> {
  if (!confirmingDeleteHost.value) return
  const deletedId = confirmingDeleteHost.value.id
  deleteLoading.value = true
  try {
    await deleteHost(deletedId, confirmingDeleteHost.value.revision)
    notify('info', t('hosts.hostRemovedSuccess', { name: confirmingDeleteHost.value.name }))
    confirmingDeleteHost.value = null
    emit('host-deleted', deletedId)
    await fetchHosts()
  } catch (err) {
    notify('error', t('hosts.hostDeleteFailed', { error: err instanceof Error ? err.message : String(err) }))
  } finally {
    deleteLoading.value = false
  }
}

function validateForm(): boolean {
  const errors: Record<string, FieldErrorValue> = {}
  const trimmedId = formId.value.trim()
  if (view.value === 'add') {
    if (!trimmedId) {
      errors.id = 'hosts.idRequired'
    } else if (!/^[a-z0-9][a-z0-9_-]*$/.test(trimmedId)) {
      errors.id = 'hosts.invalidId'
    } else if (formConnectionType.value === 'ssh' && trimmedId === 'local') {
      errors.id = 'hosts.reservedIdLocal'
    }
  }

  const trimmedName = formName.value.trim()
  if (!trimmedName) {
    errors.name = 'hosts.nameRequired'
  }

  if (formConnectionType.value === 'ssh') {
    const trimmedAddress = formAddress.value.trim()
    if (!trimmedAddress) {
      errors.address = 'hosts.addressRequired'
    }

    const port = Number(formPort.value)
    if (!port || port < 1 || port > 65535) {
      errors.port = 'hosts.portRange'
    }

    const trimmedUsername = formUsername.value.trim()
    if (!trimmedUsername) {
      errors.username = 'hosts.usernameRequired'
    }

    // Authentication validation
    if (view.value === 'add') {
      if (formCredentialType.value === 'password' && !formPassword.value) {
        errors.password = 'hosts.passwordRequired'
      }
      if (formCredentialType.value === 'private_key' && !formPrivateKey.value.trim()) {
        errors.privateKey = 'hosts.privateKeyRequired'
      }
    }
  }

  formFieldErrors.value = errors
  return Object.keys(errors).length === 0
}

async function onSubmit(): Promise<void> {
  if (confirmingTrust.value || formSubmitting.value) return
  formGeneralError.value = null
  if (!validateForm()) return

  formSubmitting.value = true
  try {
    const isConnLocal = formConnectionType.value === 'local'
    const input: HostInput = {
      id: formId.value.trim(),
      name: formName.value.trim(),
      connectionType: formConnectionType.value,
    }

    if (!isConnLocal) {
      input.address = formAddress.value.trim()
      input.port = Number(formPort.value)
      input.username = formUsername.value.trim()
      input.credentialType = formCredentialType.value

      if (formChallengeToken.value && trustConfirmed.value) {
        input.challengeToken = formChallengeToken.value
      }

      // Secret retention semantics:
      // Only send credentials when user has entered a new secret.
      // If left blank during edit, omit credentials to retain saved secret.
      const hasNewPassword = formCredentialType.value === 'password' && formPassword.value !== ''
      const hasNewKey =
        formCredentialType.value === 'private_key' && formPrivateKey.value.trim() !== ''
      if (hasNewPassword) {
        input.credentials = { password: formPassword.value }
      } else if (hasNewKey) {
        input.credentials = {
          privateKey: formPrivateKey.value.trim(),
          passphrase: formPassphrase.value ? formPassphrase.value : undefined,
        }
      }
    }

    if (view.value === 'add') {
      await createHost(input)
      notify('info', t('hosts.hostAddedSuccess', { name: input.name }))
    } else if (view.value === 'edit' && formEditRevision.value !== null) {
      await updateHost(input.id!, input, formEditRevision.value)
      notify('info', t('hosts.hostUpdatedSuccess', { name: input.name }))
    }

    await fetchHosts()
    view.value = 'list'
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    formGeneralError.value = formatHostSubmitError(msg)
  } finally {
    formSubmitting.value = false
  }
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      previousFocus?.focus()
      previousFocus = null
      return
    }
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    view.value = 'list'
    resetForm()
    await nextTick()
    closeButton.value?.focus()
    await fetchHosts()
    if (props.initialHostId) {
      const match = hosts.value.find((h) => h.id === props.initialHostId)
      if (match) {
        startEdit(match)
      }
    }
  },
  { immediate: true },
)

function onKey(ev: KeyboardEvent): void {
  if (!props.open) return
  if (ev.key === 'Escape') {
    if (confirmingDeleteHost.value) {
      confirmingDeleteHost.value = null
      return
    }
    if (view.value !== 'list') {
      view.value = 'list'
      return
    }
    emit('close')
  }
}

function trapFocus(ev: KeyboardEvent): void {
  if (ev.key !== 'Tab' || !panel.value) return
  const focusable = Array.from(
    panel.value.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
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
  unlistenLocale?.()
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div v-if="open" class="host-modal" @click.self="emit('close')">
    <div
      ref="panel"
      class="host-modal__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="host-modal-title"
      @keydown="trapFocus"
    >
      <header class="host-modal__header">
        <h2 id="host-modal-title" class="host-modal__title">
          <span v-if="view === 'list'">{{ t('hosts.title') }}</span>
          <span v-else-if="view === 'add'">{{ t('hosts.createHost') }}</span>
          <span v-else>{{ t('hosts.editHost') }}: {{ formName || formId }}</span>
        </h2>
        <button
          ref="closeButton"
          class="host-modal__close"
          type="button"
          :aria-label="t('hosts.closeManager')"
          @click="emit('close')"
        >
          ✕
        </button>
      </header>

      <div class="host-modal__body">
        <!-- 1. Hosts List View -->
        <div v-if="view === 'list'" class="host-list-view">
          <div class="host-list-view__actions">
            <button class="host-btn host-btn--primary host-modal__add-btn" type="button" @click="startAdd">
              {{ t('hosts.addHost') }}
            </button>
            <button class="host-btn host-btn--subtle" type="button" :disabled="loading" @click="fetchHosts">
              {{ loading ? t('common.loading') : t('common.refresh') }}
            </button>
          </div>

          <div v-if="listError" class="host-alert host-alert--danger" role="alert">
            {{ listError }}
          </div>

          <div v-if="loading && hosts.length === 0" class="host-empty">
            {{ t('common.loading') }}
          </div>

          <div v-else-if="hosts.length === 0" class="host-empty">
            {{ t('hosts.noHosts') }}
          </div>

          <ul v-else class="host-cards" role="list">
            <li v-for="item in hosts" :key="item.id" class="host-card" :class="{ 'host-card--local': isLocal(item) }">
              <div class="host-card__main">
                <div class="host-card__title-row">
                  <span class="host-card__name">{{ item.name }}</span>
                  <span class="host-badge" :class="isLocal(item) ? 'host-badge--local' : 'host-badge--remote'">
                    {{ isLocal(item) ? 'local' : item.id }}
                  </span>
                  <span v-if="hostSettings?.defaultHostId === item.id" class="host-badge host-badge--default">
                    default
                  </span>
                  <span v-if="!isLocal(item)" class="host-badge" :class="item.trusted ? 'host-badge--green' : 'host-badge--warning'">
                    {{ item.trusted ? 'trusted' : 'untrusted' }}
                  </span>
                  <span v-if="!isLocal(item) && item.hasCredential" class="host-badge host-badge--info">
                    {{ item.credentialType || 'authenticated' }}
                  </span>
                </div>
                <div class="host-card__meta">
                  <span v-if="isLocal(item)">{{ t('hosts.localHostDescription') }}</span>
                  <span v-else>
                    {{ item.username }}@{{ item.address }}:{{ item.port }} (rev {{ item.revision }})
                  </span>
                </div>

                <!-- Capability test feedback -->
                <div v-if="activeCapabilities[item.id]" class="host-card__caps">
                  <span v-if="activeCapabilities[item.id] === 'testing'" class="host-card__testing">
                    {{ t('hosts.testing') }}
                  </span>
                  <div v-else-if="typeof activeCapabilities[item.id] === 'object'" class="host-caps-result">
                    <span
                      class="host-cap-pill"
                      :class="getCapPillClass((activeCapabilities[item.id] as HostCapabilities).ssh)"
                    >
                      {{ getCapLabel((activeCapabilities[item.id] as HostCapabilities).ssh, 'SSH') }}
                    </span>
                    <span
                      class="host-cap-pill"
                      :class="getCapPillClass((activeCapabilities[item.id] as HostCapabilities).tmux)"
                    >
                      {{ getCapLabel((activeCapabilities[item.id] as HostCapabilities).tmux, 'tmux') }}
                    </span>
                    <span
                      class="host-cap-pill"
                      :class="getCapPillClass((activeCapabilities[item.id] as HostCapabilities).sftp)"
                    >
                      {{ getCapLabel((activeCapabilities[item.id] as HostCapabilities).sftp, 'SFTP') }}
                    </span>
                    <span
                      v-if="shouldShowAtomicOverwrite(activeCapabilities[item.id] as HostCapabilities)"
                      class="host-cap-pill"
                      :class="(activeCapabilities[item.id] as HostCapabilities).posixRename ? 'host-cap-pill--ok' : 'host-cap-pill--fail'"
                    >
                      {{ (activeCapabilities[item.id] as HostCapabilities).posixRename ? t('hosts.atomicOverwriteSupported') : t('hosts.atomicOverwriteUnsupported') }}
                    </span>
                    <span v-if="(activeCapabilities[item.id] as HostCapabilities).error" class="host-caps-error">
                      {{ (activeCapabilities[item.id] as HostCapabilities).error }}
                    </span>
                  </div>
                </div>
              </div>

              <div class="host-card__controls">
                <button
                  class="host-btn host-btn--subtle"
                  type="button"
                  :aria-label="t('hosts.testConnectionAria', { name: item.name })"
                  :disabled="activeCapabilities[item.id] === 'testing'"
                  @click="onTestCapabilities(item)"
                >
                  {{ t('hosts.testConnection') }}
                </button>
                <button
                  class="host-btn host-btn--subtle"
                  type="button"
                  :aria-label="t('hosts.editHostAria', { name: item.name })"
                  @click="startEdit(item)"
                >
                  {{ t('common.edit') }}
                </button>
                <button
                  v-if="hostSettings && hostSettings.defaultHostId !== item.id"
                  class="host-btn host-btn--subtle"
                  type="button"
                  :aria-label="t('hosts.setDefaultHostAria', { name: item.name })"
                  :disabled="settingDefault === item.id"
                  @click="onSetDefault(item)"
                >
                  {{ t('hosts.setDefault') }}
                </button>
                <button
                  class="host-btn host-btn--danger"
                  type="button"
                  :aria-label="t('hosts.removeHostAria', { name: item.name })"
                  @click="promptDelete(item)"
                >
                  {{ t('hosts.deleteHost') }}
                </button>
                <button
                  class="host-btn host-btn--primary"
                  type="button"
                  :aria-label="t('hosts.selectHostAria', { name: item.name })"
                  :title="t('hosts.chooseHost')"
                  @click="emit('select-host', item.id); emit('close')"
                >
                  {{ t('common.select') }}
                </button>
              </div>
            </li>
          </ul>
        </div>

        <!-- 2. Add / Edit Host Form -->
        <form v-else class="host-form" novalidate @submit.prevent="onSubmit">
          <div v-if="displayGeneralError" class="host-alert host-alert--danger" role="alert">
            {{ displayGeneralError }}
          </div>

          <div class="host-form__grid">
            <!-- Connection Type -->
            <div class="host-field host-field--full">
              <label class="host-label">{{ t('hosts.connectionType') }}</label>
              <div v-if="view === 'add'" class="host-radio-group">
                <label class="host-radio-label">
                  <input
                    v-model="formConnectionType"
                    type="radio"
                    value="ssh"
                    name="connectionType"
                  />
                  {{ t('hosts.sshRemoteHost') }}
                </label>
                <label class="host-radio-label">
                  <input
                    v-model="formConnectionType"
                    type="radio"
                    value="local"
                    name="connectionType"
                  />
                  {{ t('hosts.localExecution') }}
                </label>
              </div>
              <div v-else class="host-readonly-text">
                <span class="host-badge" :class="formConnectionType === 'local' ? 'host-badge--local' : 'host-badge--remote'">
                  {{ formConnectionType === 'local' ? t('hosts.localExecution') : t('hosts.sshRemoteHost') }}
                </span>
              </div>
            </div>

            <!-- Host ID -->
            <div class="host-field">
              <label for="host-field-id" class="host-label">
                {{ t('hosts.id') }} <span v-if="view === 'add'" class="host-req">*</span>
              </label>
              <input
                id="host-field-id"
                v-model="formId"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': localizedFieldErrors.id }"
                :placeholder="t('hosts.idPlaceholder')"
                :disabled="view === 'edit'"
              />
              <span v-if="localizedFieldErrors.id" class="host-field-error" role="alert">{{ localizedFieldErrors.id }}</span>
              <span v-else class="host-field-hint">{{ t('hosts.idHint') }}</span>
            </div>

            <!-- Host Display Name -->
            <div class="host-field">
              <label for="host-field-name" class="host-label">
                {{ t('hosts.name') }} <span class="host-req">*</span>
              </label>
              <input
                id="host-field-name"
                v-model="formName"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': localizedFieldErrors.name }"
                :placeholder="t('hosts.namePlaceholder')"
              />
              <span v-if="localizedFieldErrors.name" class="host-field-error" role="alert">{{ localizedFieldErrors.name }}</span>
            </div>

            <!-- SSH Specific fields -->
            <template v-if="formConnectionType === 'ssh'">
              <!-- Address -->
              <div class="host-field">
                <label for="host-field-address" class="host-label">
                  {{ t('hosts.sshHost') }} <span class="host-req">*</span>
                </label>
                <input
                  id="host-field-address"
                  v-model="formAddress"
                  type="text"
                  class="host-input"
                  :class="{ 'host-input--error': localizedFieldErrors.address }"
                  :placeholder="t('hosts.sshHostPlaceholder')"
                />
                <span v-if="localizedFieldErrors.address" class="host-field-error" role="alert">{{ localizedFieldErrors.address }}</span>
              </div>

              <!-- Port -->
              <div class="host-field host-field--small">
                <label for="host-field-port" class="host-label">
                  {{ t('hosts.sshPort') }} <span class="host-req">*</span>
                </label>
                <input
                  id="host-field-port"
                  v-model.number="formPort"
                  type="number"
                  min="1"
                  max="65535"
                  class="host-input"
                  :class="{ 'host-input--error': localizedFieldErrors.port }"
                />
                <span v-if="localizedFieldErrors.port" class="host-field-error" role="alert">{{ localizedFieldErrors.port }}</span>
              </div>

              <!-- Username -->
              <div class="host-field">
                <label for="host-field-username" class="host-label">
                  {{ t('hosts.sshUser') }} <span class="host-req">*</span>
                </label>
                <input
                  id="host-field-username"
                  v-model="formUsername"
                  type="text"
                  class="host-input"
                  :class="{ 'host-input--error': localizedFieldErrors.username }"
                  :placeholder="t('hosts.sshUserPlaceholder')"
                />
                <span v-if="localizedFieldErrors.username" class="host-field-error" role="alert">{{ localizedFieldErrors.username }}</span>
              </div>
            </template>
          </div>

          <!-- SSH Key & Authentication sections ONLY if SSH -->
          <template v-if="formConnectionType === 'ssh'">
            <!-- Fingerprint Probing & Trust Section -->
            <div class="host-section">
              <h3 class="host-section__title">{{ t('hosts.serverKeyAndTrust') }}</h3>
              <div class="host-section__actions">
                <button
                  class="host-btn host-btn--subtle host-probe__probe-btn"
                  type="button"
                  :disabled="probing || confirmingTrust || formSubmitting || !formAddress"
                  @click="onProbe"
                >
                  {{ probing ? t('hosts.probingServer') : t('hosts.probeServer') }}
                </button>
              </div>

              <div v-if="displayProbeError" class="host-alert host-alert--danger" role="alert">
                {{ displayProbeError }}
              </div>

              <div v-if="probedResult" class="host-probe-box">
                <div class="host-probe-row">
                  <span class="host-probe-label">{{ t('hosts.keyType') }}:</span>
                  <span class="host-probe-val">{{ probedResult.keyType }}</span>
                </div>
                <div class="host-probe-row">
                  <span class="host-probe-label">{{ t('hosts.fingerprint') }}:</span>
                  <code class="host-probe-fingerprint">{{ probedResult.fingerprint }}</code>
                </div>
                <div class="host-probe-confirm">
                  <button
                    v-if="!trustConfirmed"
                    class="host-btn host-btn--primary host-probe__trust-btn"
                    type="button"
                    :disabled="confirmingTrust || formSubmitting"
                    @click="onConfirmTrust"
                  >
                    {{ confirmingTrust ? t('hosts.confirmingFingerprint') : t('hosts.trustConfirm') }}
                  </button>
                  <span v-else class="host-probe-trusted-indicator">
                    {{ t('hosts.fingerprintTrusted') }}
                  </span>
                </div>
              </div>
            </div>

            <!-- Authentication Credentials -->
            <div class="host-section">
              <h3 class="host-section__title">{{ t('hosts.authentication') }}</h3>
              <div class="host-radio-group">
                <label class="host-radio-label">
                  <input
                    v-model="formCredentialType"
                    type="radio"
                    value="password"
                    name="credentialType"
                  />
                  {{ t('hosts.password') }}
                </label>
                <label class="host-radio-label">
                  <input
                    v-model="formCredentialType"
                    type="radio"
                    value="private_key"
                    name="credentialType"
                  />
                  {{ t('hosts.sshKey') }}
                </label>
              </div>

              <!-- Password Fields -->
              <div v-if="formCredentialType === 'password'" class="host-field">
                <label for="host-field-password" class="host-label">
                  {{ t('hosts.password') }}
                  <span v-if="view === 'add'" class="host-req">*</span>
                  <span v-else class="host-subtle">{{ t('hosts.passwordKeep') }}</span>
                </label>
                <input
                  id="host-field-password"
                  v-model="formPassword"
                  type="password"
                  class="host-input"
                  :class="{ 'host-input--error': localizedFieldErrors.password }"
                  :placeholder="view === 'add' ? t('hosts.passwordPlaceholderAdd') : t('hosts.passwordPlaceholderEdit')"
                  autocomplete="new-password"
                />
                <span v-if="localizedFieldErrors.password" class="host-field-error" role="alert">{{ localizedFieldErrors.password }}</span>
              </div>

              <!-- Private Key Fields -->
              <div v-else class="host-key-group">
                <div class="host-field">
                  <label for="host-field-privkey" class="host-label">
                    {{ t('hosts.sshKey') }}
                    <span v-if="view === 'add'" class="host-req">*</span>
                    <span v-else class="host-subtle">{{ t('hosts.sshKeyKeep') }}</span>
                  </label>
                  <textarea
                    id="host-field-privkey"
                    v-model="formPrivateKey"
                    class="host-textarea"
                    :class="{ 'host-input--error': localizedFieldErrors.privateKey }"
                    rows="4"
                    :placeholder="view === 'add' ? t('hosts.privateKeyPlaceholderAdd') : t('hosts.privateKeyPlaceholderEdit')"
                    spellcheck="false"
                  ></textarea>
                  <span v-if="localizedFieldErrors.privateKey" class="host-field-error" role="alert">{{ localizedFieldErrors.privateKey }}</span>
                </div>

                <div class="host-field">
                  <label for="host-field-passphrase" class="host-label">
                    {{ t('hosts.passphrase') }} <span class="host-subtle">{{ t('hosts.passphraseHint') }}</span>
                  </label>
                  <input
                    id="host-field-passphrase"
                    v-model="formPassphrase"
                    type="password"
                    class="host-input"
                    :placeholder="t('hosts.passphrasePlaceholder')"
                    autocomplete="new-password"
                  />
                </div>
              </div>
            </div>
          </template>

          <div class="host-form__actions">
            <button class="host-btn host-btn--subtle" type="button" :disabled="formSubmitting || confirmingTrust" @click="cancelForm">
              {{ t('common.cancel') }}
            </button>
            <button class="host-btn host-btn--primary" type="submit" :disabled="formSubmitting || confirmingTrust">
              {{ formSubmitting ? t('common.saving') : (view === 'add' ? t('hosts.createHost') : t('hosts.saveChanges')) }}
            </button>
          </div>
        </form>
      </div>

      <!-- Delete Confirmation Dialog -->
      <div v-if="confirmingDeleteHost" class="host-confirm-overlay" @click.self="confirmingDeleteHost = null">
        <div class="host-confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-delete-title">
          <h3 id="confirm-delete-title" class="host-confirm-dialog__title">
            {{ t('hosts.deleteConfirm', { name: confirmingDeleteHost.name }) }}
          </h3>
          <p v-if="hasDirtyDrafts" class="host-confirm-dialog__warning" role="alert">
            <strong>Warning:</strong> {{ t('hosts.deleteConfirmDirty') }}. Removing the host will discard these drafts.
          </p>
          <p class="host-confirm-dialog__msg">
            Removing this host stops visual-tmux-client from accessing it.
            <strong>{{ t('hosts.deleteWarningRemote') }}</strong> and will remain running on the remote server.
            Any unsaved editor tabs on this host will be lost.
          </p>
          <div class="host-confirm-dialog__actions">
            <button
              class="host-btn host-btn--subtle host-confirm__cancel"
              type="button"
              :disabled="deleteLoading"
              @click="confirmingDeleteHost = null"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              class="host-btn host-btn--danger host-confirm__delete"
              type="button"
              :disabled="deleteLoading"
              @click="confirmDelete"
            >
              {{ deleteLoading ? t('common.deleting') : t('hosts.deleteHost') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.host-modal {
  position: fixed;
  inset: 0;
  z-index: 50;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}

.host-modal__panel {
  width: 100%;
  max-width: 720px;
  max-height: 90vh;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.5);
  overflow: hidden;
}

.host-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--th-border);
  background: var(--th-raised);
}

.host-modal__title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--th-text-hi);
}

.host-modal__close {
  background: transparent;
  border: none;
  color: var(--th-text-mid);
  font-size: 1.1rem;
  cursor: pointer;
  padding: 0.2rem 0.5rem;
  border-radius: 4px;
}
.host-modal__close:hover {
  color: var(--th-text-hi);
  background: rgba(255, 255, 255, 0.08);
}

.host-modal__body {
  padding: 1rem;
  overflow-y: auto;
  flex: 1;
}

.host-list-view__actions {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.host-cards {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.host-card {
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.75rem 1rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
.host-card--local {
  border-left: 3px solid var(--th-accent);
}

.host-card__main {
  flex: 1;
  min-width: 0;
}

.host-card__title-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.host-card__name {
  font-weight: 600;
  color: var(--th-text-hi);
}

.host-card__meta {
  font-size: 0.8rem;
  color: var(--th-text-mid);
  margin-top: 0.2rem;
  font-family: var(--app-code-font);
}

.host-card__controls {
  display: flex;
  gap: 0.4rem;
  flex-shrink: 0;
}

.host-badge {
  font-size: 0.7rem;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
  text-transform: uppercase;
  font-weight: 600;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  color: var(--th-text-mid);
}
.host-badge--local {
  color: var(--th-accent);
  border-color: var(--th-accent);
}
.host-badge--remote {
  color: var(--th-text-hi);
}
.host-badge--green {
  color: var(--th-green);
  border-color: var(--th-green);
}
.host-badge--warning {
  color: var(--th-warning);
  border-color: var(--th-warning);
}
.host-badge--default {
  color: #a855f7;
  border-color: #a855f7;
}
.host-badge--info {
  color: var(--th-accent);
}

.host-card__caps {
  margin-top: 0.5rem;
  font-size: 0.75rem;
}
.host-card__testing {
  color: var(--th-accent);
  font-style: italic;
}
.host-caps-result {
  display: flex;
  gap: 0.4rem;
  flex-wrap: wrap;
  align-items: center;
}
.host-cap-pill {
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.72rem;
  font-family: var(--app-code-font);
}
.host-cap-pill--ok {
  background: rgba(74, 222, 128, 0.12);
  color: var(--th-green);
  border: 1px solid rgba(74, 222, 128, 0.3);
}
.host-cap-pill--fail {
  background: rgba(248, 113, 113, 0.12);
  color: var(--th-danger);
  border: 1px solid rgba(248, 113, 113, 0.3);
}
.host-cap-pill--na {
  background: rgba(148, 163, 184, 0.12);
  color: #94a3b8;
  border: 1px solid rgba(148, 163, 184, 0.3);
}
.host-cap-pill--unknown {
  background: rgba(251, 191, 36, 0.12);
  color: var(--th-warning);
  border: 1px solid rgba(251, 191, 36, 0.3);
}
.host-readonly-text {
  display: flex;
  align-items: center;
  min-height: 2.2rem;
}
.host-field--full {
  grid-column: 1 / -1;
}
.host-caps-error {
  color: var(--th-danger);
  font-size: 0.75rem;
  width: 100%;
  margin-top: 0.2rem;
}

.host-btn {
  padding: 0.35rem 0.75rem;
  border-radius: 4px;
  font-size: 0.82rem;
  cursor: pointer;
  border: 1px solid transparent;
  font-family: var(--app-ui-font);
}
.host-btn--primary {
  background: var(--th-accent);
  color: #000;
  font-weight: 600;
}
.host-btn--primary:hover:not(:disabled) {
  opacity: 0.9;
}
.host-btn--subtle {
  background: var(--th-raised);
  border-color: var(--th-border);
  color: var(--th-text-hi);
}
.host-btn--subtle:hover:not(:disabled) {
  border-color: var(--th-text-mid);
}
.host-btn--danger {
  background: transparent;
  border-color: var(--th-danger);
  color: var(--th-danger);
}
.host-btn--danger:hover:not(:disabled) {
  background: var(--th-danger);
  color: #000;
}
.host-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.host-form__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.75rem;
}

.host-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  margin-bottom: 0.6rem;
}
.host-field--small {
  max-width: 120px;
}

.host-label {
  font-size: 0.8rem;
  color: var(--th-text-mid);
  font-weight: 500;
}
.host-req {
  color: var(--th-danger);
}
.host-subtle {
  font-size: 0.75rem;
  color: var(--th-text-lo);
}

.host-input,
.host-textarea {
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  color: var(--th-text-hi);
  padding: 0.4rem 0.6rem;
  font-size: 0.85rem;
  font-family: inherit;
}
.host-textarea {
  font-family: var(--app-code-font);
  font-size: 0.8rem;
}
.host-input--error {
  border-color: var(--th-danger);
}
.host-field-error {
  font-size: 0.75rem;
  color: var(--th-danger);
}
.host-field-hint {
  font-size: 0.72rem;
  color: var(--th-text-lo);
}

.host-section {
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 6px;
  padding: 0.75rem;
  margin: 0.75rem 0;
}
.host-section__title {
  font-size: 0.85rem;
  margin: 0 0 0.5rem 0;
  color: var(--th-text-hi);
  font-weight: 600;
}

.host-radio-group {
  display: flex;
  gap: 1rem;
  margin-bottom: 0.5rem;
}
.host-radio-label,
.host-checkbox-label {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.82rem;
  color: var(--th-text-hi);
  cursor: pointer;
}

.host-probe-box {
  margin-top: 0.5rem;
  padding: 0.5rem;
  background: var(--th-surface);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  font-size: 0.8rem;
}
.host-probe-row {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.3rem;
}
.host-probe-label {
  color: var(--th-text-mid);
  width: 90px;
}
.host-probe-fingerprint {
  font-family: var(--app-code-font);
  color: var(--th-accent);
  word-break: break-all;
}
.host-probe-confirm {
  margin-top: 0.5rem;
  padding-top: 0.5rem;
  border-top: 1px solid var(--th-border);
}

.host-form__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  margin-top: 1rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--th-border);
}

.host-alert {
  padding: 0.5rem 0.75rem;
  border-radius: 4px;
  font-size: 0.8rem;
  margin-bottom: 0.75rem;
}
.host-alert--danger {
  background: rgba(248, 113, 113, 0.12);
  color: var(--th-danger);
  border: 1px solid rgba(248, 113, 113, 0.3);
}

.host-empty {
  text-align: center;
  padding: 2rem 1rem;
  color: var(--th-text-lo);
  font-size: 0.85rem;
}

.host-confirm-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.75);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  z-index: 60;
}
.host-confirm-dialog {
  background: var(--th-surface);
  border: 1px solid var(--th-danger);
  border-radius: 6px;
  padding: 1.25rem;
  max-width: 440px;
  box-shadow: 0 10px 20px rgba(0, 0, 0, 0.6);
}
.host-confirm-dialog__title {
  margin: 0 0 0.5rem 0;
  font-size: 1rem;
  color: var(--th-danger);
}
.host-confirm-dialog__msg {
  font-size: 0.82rem;
  color: var(--th-text-mid);
  line-height: 1.4;
  margin: 0 0 1rem 0;
}
.host-confirm-dialog__warning {
  margin: 0 0 0.75rem 0;
  padding: 0.5rem 0.75rem;
  background: rgba(230, 162, 60, 0.15);
  border: 1px solid var(--th-warning, #e6a23c);
  border-radius: 4px;
  color: var(--th-warning, #e6a23c);
  font-size: 0.8rem;
  line-height: 1.4;
}
.host-confirm-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
