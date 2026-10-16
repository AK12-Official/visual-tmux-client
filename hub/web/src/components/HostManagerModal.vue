<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  confirmHostTrust,
  createHost,
  deleteHost,
  listHosts,
  probeDraftHost,
  probeExistingHost,
  testHostCapabilities,
  updateHost,
  type HostCapabilities,
  type HostInput,
  type HostItem,
  type ProbeResult,
} from '../api'
import { notify } from '../toasts'

const props = defineProps<{
  open: boolean
  initialHostId?: string | null
  dirtyHosts?: string[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'hosts-changed'): void
  (e: 'host-deleted', id: string): void
  (e: 'select-host', id: string): void
}>()

const panel = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let previousFocus: HTMLElement | null = null

type ViewMode = 'list' | 'add' | 'edit'
const view = ref<ViewMode>('list')

const hosts = ref<HostItem[]>([])
const loading = ref(false)
const listError = ref('')
const activeCapabilities = ref<Record<string, HostCapabilities | 'testing' | null>>({})

// Form state
const formId = ref('')
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
const formFieldErrors = ref<Record<string, string>>({})
const formGeneralError = ref('')
const formSubmitting = ref(false)

// Probing state
const probing = ref(false)
const probedResult = ref<ProbeResult | null>(null)
const probeError = ref('')
const trustConfirmed = ref(false)

// Deletion confirmation state
const confirmingDeleteHost = ref<HostItem | null>(null)
const deleteLoading = ref(false)

async function fetchHosts(): Promise<void> {
  loading.value = true
  listError.value = ''
  try {
    hosts.value = await listHosts()
    emit('hosts-changed')
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

function resetForm(): void {
  formId.value = ''
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
  formGeneralError.value = ''
  probing.value = false
  probedResult.value = null
  probeError.value = ''
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

async function onProbe(): Promise<void> {
  probeError.value = ''
  probedResult.value = null
  trustConfirmed.value = false

  const addr = formAddress.value.trim()
  if (!addr) {
    formFieldErrors.value = { ...formFieldErrors.value, address: 'Address is required to probe' }
    return
  }
  const port = Number(formPort.value)
  if (!port || port < 1 || port > 65535) {
    formFieldErrors.value = { ...formFieldErrors.value, port: 'Port must be between 1 and 65535' }
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
    probeError.value = err instanceof Error ? err.message : String(err)
  } finally {
    probing.value = false
  }
}

async function onConfirmTrust(): Promise<void> {
  if (!probedResult.value) return
  trustConfirmed.value = true
  if (view.value === 'edit' && formId.value && formChallengeToken.value) {
    try {
      await confirmHostTrust(formId.value, formChallengeToken.value)
      notify('info', 'Host fingerprint trusted')
      await fetchHosts()
    } catch (err) {
      probeError.value = err instanceof Error ? err.message : String(err)
    }
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
    notify('info', `Host "${confirmingDeleteHost.value.name}" removed`)
    confirmingDeleteHost.value = null
    emit('host-deleted', deletedId)
    await fetchHosts()
  } catch (err) {
    notify('error', `Failed to delete host: ${err instanceof Error ? err.message : String(err)}`)
  } finally {
    deleteLoading.value = false
  }
}

function validateForm(): boolean {
  const errors: Record<string, string> = {}
  const trimmedId = formId.value.trim()
  if (view.value === 'add') {
    if (!trimmedId) {
      errors.id = 'Host ID is required'
    } else if (!/^[a-z0-9][a-z0-9_-]*$/.test(trimmedId)) {
      errors.id = 'Host ID must start with a lowercase letter or digit and contain only lowercase letters, digits, dashes or underscores'
    } else if (trimmedId === 'local') {
      errors.id = 'Host ID "local" is reserved for the built-in host'
    }
  }

  const trimmedName = formName.value.trim()
  if (!trimmedName) {
    errors.name = 'Host name is required'
  }

  const trimmedAddress = formAddress.value.trim()
  if (!trimmedAddress) {
    errors.address = 'Host address is required'
  }

  const port = Number(formPort.value)
  if (!port || port < 1 || port > 65535) {
    errors.port = 'Port must be between 1 and 65535'
  }

  const trimmedUsername = formUsername.value.trim()
  if (!trimmedUsername) {
    errors.username = 'Username is required'
  }

  // Authentication validation
  if (view.value === 'add') {
    if (formCredentialType.value === 'password' && !formPassword.value) {
      errors.password = 'Password is required for new host'
    }
    if (formCredentialType.value === 'private_key' && !formPrivateKey.value.trim()) {
      errors.privateKey = 'Private key is required for new host'
    }
  }

  formFieldErrors.value = errors
  return Object.keys(errors).length === 0
}

async function onSubmit(): Promise<void> {
  formGeneralError.value = ''
  if (!validateForm()) return

  formSubmitting.value = true
  try {
    const input: HostInput = {
      id: formId.value.trim(),
      name: formName.value.trim(),
      address: formAddress.value.trim(),
      port: Number(formPort.value),
      username: formUsername.value.trim(),
      credentialType: formCredentialType.value,
    }

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

    if (view.value === 'add') {
      await createHost(input)
      notify('info', `Host "${input.name}" added successfully`)
    } else if (view.value === 'edit' && formEditRevision.value !== null) {
      await updateHost(input.id!, input, formEditRevision.value)
      notify('info', `Host "${input.name}" updated successfully`)
    }

    await fetchHosts()
    view.value = 'list'
  } catch (err) {
    formGeneralError.value = err instanceof Error ? err.message : String(err)
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
      if (match && match.id !== 'local') {
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

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
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
          <span v-if="view === 'list'">Host Management</span>
          <span v-else-if="view === 'add'">Add Remote Host</span>
          <span v-else>Edit Host: {{ formName || formId }}</span>
        </h2>
        <button
          ref="closeButton"
          class="host-modal__close"
          type="button"
          aria-label="Close host manager"
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
              + Add Remote Host
            </button>
            <button class="host-btn host-btn--subtle" type="button" :disabled="loading" @click="fetchHosts">
              {{ loading ? 'Refreshing…' : 'Refresh' }}
            </button>
          </div>

          <div v-if="listError" class="host-alert host-alert--danger" role="alert">
            {{ listError }}
          </div>

          <div v-if="loading && hosts.length === 0" class="host-empty">
            Loading hosts…
          </div>

          <div v-else-if="hosts.length === 0" class="host-empty">
            No hosts configured.
          </div>

          <ul v-else class="host-cards" role="list">
            <li v-for="item in hosts" :key="item.id" class="host-card" :class="{ 'host-card--local': item.id === 'local' }">
              <div class="host-card__main">
                <div class="host-card__title-row">
                  <span class="host-card__name">{{ item.name }}</span>
                  <span class="host-badge" :class="item.id === 'local' ? 'host-badge--local' : 'host-badge--remote'">
                    {{ item.id === 'local' ? 'local' : item.id }}
                  </span>
                  <span v-if="item.id !== 'local'" class="host-badge" :class="item.trusted ? 'host-badge--green' : 'host-badge--warning'">
                    {{ item.trusted ? 'trusted' : 'untrusted' }}
                  </span>
                  <span v-if="item.hasCredential" class="host-badge host-badge--info">
                    {{ item.credentialType || 'authenticated' }}
                  </span>
                </div>
                <div class="host-card__meta">
                  <span v-if="item.id === 'local'">Local system execution (no SSH)</span>
                  <span v-else>
                    {{ item.username }}@{{ item.address }}:{{ item.port }} (rev {{ item.revision }})
                  </span>
                </div>

                <!-- Capability test feedback -->
                <div v-if="activeCapabilities[item.id]" class="host-card__caps">
                  <span v-if="activeCapabilities[item.id] === 'testing'" class="host-card__testing">
                    Testing connection…
                  </span>
                  <div v-else-if="typeof activeCapabilities[item.id] === 'object'" class="host-caps-result">
                    <span
                      class="host-cap-pill"
                      :class="(activeCapabilities[item.id] as HostCapabilities).ssh ? 'host-cap-pill--ok' : 'host-cap-pill--fail'"
                    >
                      SSH: {{ (activeCapabilities[item.id] as HostCapabilities).ssh ? 'Connected' : 'Offline' }}
                    </span>
                    <span
                      class="host-cap-pill"
                      :class="(activeCapabilities[item.id] as HostCapabilities).tmux ? 'host-cap-pill--ok' : 'host-cap-pill--fail'"
                    >
                      tmux: {{ (activeCapabilities[item.id] as HostCapabilities).tmux ? 'Available' : 'Missing' }}
                    </span>
                    <span
                      class="host-cap-pill"
                      :class="(activeCapabilities[item.id] as HostCapabilities).sftp ? 'host-cap-pill--ok' : 'host-cap-pill--fail'"
                    >
                      SFTP: {{ (activeCapabilities[item.id] as HostCapabilities).sftp ? 'Available' : 'Unavailable' }}
                    </span>
                    <span
                      v-if="(activeCapabilities[item.id] as HostCapabilities).sftp"
                      class="host-cap-pill"
                      :class="(activeCapabilities[item.id] as HostCapabilities).posixRename ? 'host-cap-pill--ok' : 'host-cap-pill--fail'"
                    >
                      Atomic Overwrite: {{ (activeCapabilities[item.id] as HostCapabilities).posixRename ? 'Supported' : 'Unsupported' }}
                    </span>
                    <span v-if="(activeCapabilities[item.id] as HostCapabilities).error" class="host-caps-error">
                      {{ (activeCapabilities[item.id] as HostCapabilities).error }}
                    </span>
                  </div>
                </div>
              </div>

              <div class="host-card__controls">
                <template v-if="item.id !== 'local'">
                  <button
                    class="host-btn host-btn--subtle"
                    type="button"
                    :aria-label="`Test connection for ${item.name}`"
                    :disabled="activeCapabilities[item.id] === 'testing'"
                    @click="onTestCapabilities(item)"
                  >
                    Test
                  </button>
                  <button
                    class="host-btn host-btn--subtle"
                    type="button"
                    :aria-label="`Edit ${item.name}`"
                    @click="startEdit(item)"
                  >
                    Edit
                  </button>
                  <button
                    class="host-btn host-btn--danger"
                    type="button"
                    :aria-label="`Remove ${item.name}`"
                    @click="promptDelete(item)"
                  >
                    Delete
                  </button>
                </template>
                <button
                  class="host-btn host-btn--primary"
                  type="button"
                  :aria-label="`Select ${item.name}`"
                  title="Select this host for current workspace"
                  @click="emit('select-host', item.id); emit('close')"
                >
                  Select
                </button>
              </div>
            </li>
          </ul>
        </div>

        <!-- 2. Add / Edit Host Form -->
        <form v-else class="host-form" novalidate @submit.prevent="onSubmit">
          <div v-if="formGeneralError" class="host-alert host-alert--danger" role="alert">
            {{ formGeneralError }}
          </div>

          <div class="host-form__grid">
            <!-- Host ID -->
            <div class="host-field">
              <label for="host-field-id" class="host-label">
                Host Identifier <span v-if="view === 'add'" class="host-req">*</span>
              </label>
              <input
                id="host-field-id"
                v-model="formId"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.id }"
                placeholder="e.g. dev-server"
                :disabled="view === 'edit'"
              />
              <span v-if="formFieldErrors.id" class="host-field-error" role="alert">{{ formFieldErrors.id }}</span>
              <span v-else class="host-field-hint">Unique lowercase alphanumeric name with hyphens or underscores.</span>
            </div>

            <!-- Host Display Name -->
            <div class="host-field">
              <label for="host-field-name" class="host-label">
                Display Name <span class="host-req">*</span>
              </label>
              <input
                id="host-field-name"
                v-model="formName"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.name }"
                placeholder="e.g. Production Cluster"
              />
              <span v-if="formFieldErrors.name" class="host-field-error" role="alert">{{ formFieldErrors.name }}</span>
            </div>

            <!-- Address -->
            <div class="host-field">
              <label for="host-field-address" class="host-label">
                SSH Address <span class="host-req">*</span>
              </label>
              <input
                id="host-field-address"
                v-model="formAddress"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.address }"
                placeholder="e.g. 192.168.1.100 or ssh.example.com"
              />
              <span v-if="formFieldErrors.address" class="host-field-error" role="alert">{{ formFieldErrors.address }}</span>
            </div>

            <!-- Port -->
            <div class="host-field host-field--small">
              <label for="host-field-port" class="host-label">
                Port <span class="host-req">*</span>
              </label>
              <input
                id="host-field-port"
                v-model.number="formPort"
                type="number"
                min="1"
                max="65535"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.port }"
              />
              <span v-if="formFieldErrors.port" class="host-field-error" role="alert">{{ formFieldErrors.port }}</span>
            </div>

            <!-- Username -->
            <div class="host-field">
              <label for="host-field-username" class="host-label">
                SSH Username <span class="host-req">*</span>
              </label>
              <input
                id="host-field-username"
                v-model="formUsername"
                type="text"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.username }"
                placeholder="e.g. root or ubuntu"
              />
              <span v-if="formFieldErrors.username" class="host-field-error" role="alert">{{ formFieldErrors.username }}</span>
            </div>
          </div>

          <!-- Fingerprint Probing & Trust Section -->
          <div class="host-section">
            <h3 class="host-section__title">Server Key & Trust</h3>
            <div class="host-section__actions">
              <button
                class="host-btn host-btn--subtle host-probe__probe-btn"
                type="button"
                :disabled="probing || !formAddress"
                @click="onProbe"
              >
                {{ probing ? 'Probing Server…' : 'Probe Server Fingerprint' }}
              </button>
            </div>

            <div v-if="probeError" class="host-alert host-alert--danger" role="alert">
              {{ probeError }}
            </div>

            <div v-if="probedResult" class="host-probe-box">
              <div class="host-probe-row">
                <span class="host-probe-label">Key Type:</span>
                <span class="host-probe-val">{{ probedResult.keyType }}</span>
              </div>
              <div class="host-probe-row">
                <span class="host-probe-label">Fingerprint:</span>
                <code class="host-probe-fingerprint">{{ probedResult.fingerprint }}</code>
              </div>
              <div class="host-probe-confirm">
                <button
                  v-if="!trustConfirmed"
                  class="host-btn host-btn--primary host-probe__trust-btn"
                  type="button"
                  @click="onConfirmTrust"
                >
                  Trust & Pin Fingerprint
                </button>
                <span v-else class="host-probe-trusted-indicator">
                  Fingerprint trusted
                </span>
              </div>
            </div>
          </div>

          <!-- Authentication Credentials -->
          <div class="host-section">
            <h3 class="host-section__title">Authentication</h3>
            <div class="host-radio-group">
              <label class="host-radio-label">
                <input
                  v-model="formCredentialType"
                  type="radio"
                  value="password"
                  name="credentialType"
                />
                Password
              </label>
              <label class="host-radio-label">
                <input
                  v-model="formCredentialType"
                  type="radio"
                  value="private_key"
                  name="credentialType"
                />
                Private Key
              </label>
            </div>

            <!-- Password Fields -->
            <div v-if="formCredentialType === 'password'" class="host-field">
              <label for="host-field-password" class="host-label">
                Password
                <span v-if="view === 'add'" class="host-req">*</span>
                <span v-else class="host-subtle">(leave blank to keep existing password)</span>
              </label>
              <input
                id="host-field-password"
                v-model="formPassword"
                type="password"
                class="host-input"
                :class="{ 'host-input--error': formFieldErrors.password }"
                :placeholder="view === 'add' ? 'SSH account password' : '•••••••• (unchanged)'"
                autocomplete="new-password"
              />
              <span v-if="formFieldErrors.password" class="host-field-error" role="alert">{{ formFieldErrors.password }}</span>
            </div>

            <!-- Private Key Fields -->
            <div v-else class="host-key-group">
              <div class="host-field">
                <label for="host-field-privkey" class="host-label">
                  Private Key (OpenSSH format)
                  <span v-if="view === 'add'" class="host-req">*</span>
                  <span v-else class="host-subtle">(leave blank to keep existing key)</span>
                </label>
                <textarea
                  id="host-field-privkey"
                  v-model="formPrivateKey"
                  class="host-textarea"
                  :class="{ 'host-input--error': formFieldErrors.privateKey }"
                  rows="4"
                  :placeholder="view === 'add' ? '-----BEGIN OPENSSH PRIVATE KEY-----\n...' : '(unchanged)'"
                  spellcheck="false"
                ></textarea>
                <span v-if="formFieldErrors.privateKey" class="host-field-error" role="alert">{{ formFieldErrors.privateKey }}</span>
              </div>

              <div class="host-field">
                <label for="host-field-passphrase" class="host-label">
                  Passphrase <span class="host-subtle">(optional, if key is encrypted)</span>
                </label>
                <input
                  id="host-field-passphrase"
                  v-model="formPassphrase"
                  type="password"
                  class="host-input"
                  placeholder="Passphrase for encrypted key"
                  autocomplete="new-password"
                />
              </div>
            </div>
          </div>

          <div class="host-form__actions">
            <button class="host-btn host-btn--subtle" type="button" :disabled="formSubmitting" @click="cancelForm">
              Cancel
            </button>
            <button class="host-btn host-btn--primary" type="submit" :disabled="formSubmitting">
              {{ formSubmitting ? 'Saving…' : (view === 'add' ? 'Create Host' : 'Save Changes') }}
            </button>
          </div>
        </form>
      </div>

      <!-- Delete Confirmation Dialog -->
      <div v-if="confirmingDeleteHost" class="host-confirm-overlay" @click.self="confirmingDeleteHost = null">
        <div class="host-confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-delete-title">
          <h3 id="confirm-delete-title" class="host-confirm-dialog__title">
            Remove Host "{{ confirmingDeleteHost.name }}"?
          </h3>
          <p v-if="hasDirtyDrafts" class="host-confirm-dialog__warning" role="alert">
            <strong>Warning:</strong> You have unsaved editor drafts on this host. Removing the host will discard these drafts.
          </p>
          <p class="host-confirm-dialog__msg">
            Removing this host stops visual-tmux-client from accessing it.
            <strong>Remote tmux sessions will NOT be killed</strong> and will remain running on the remote server.
            Any unsaved editor tabs on this host will be lost.
          </p>
          <div class="host-confirm-dialog__actions">
            <button
              class="host-btn host-btn--subtle host-confirm__cancel"
              type="button"
              :disabled="deleteLoading"
              @click="confirmingDeleteHost = null"
            >
              Cancel
            </button>
            <button
              class="host-btn host-btn--danger host-confirm__delete"
              type="button"
              :disabled="deleteLoading"
              @click="confirmDelete"
            >
              {{ deleteLoading ? 'Removing…' : 'Confirm Remove' }}
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
