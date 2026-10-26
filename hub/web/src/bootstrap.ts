import { fetchClientConfig } from './config'
import { t, onLocaleChange } from './i18n/index'

type BootstrapState =
  | { type: 'loading'; rootEl: HTMLElement }
  | { type: 'error'; rootEl: HTMLElement; message: string; onRetry: () => void }
  | null

let mounted = false
let currentState: BootstrapState = null
let unbindLocaleChange: (() => void) | null = null

function ensureBootstrapSubscription(): void {
  if (unbindLocaleChange) return
  unbindLocaleChange = onLocaleChange(() => {
    if (mounted || !currentState) return
    if (currentState.type === 'loading') {
      renderLoading(currentState.rootEl)
    } else if (currentState.type === 'error') {
      renderError(currentState.rootEl, currentState.message, currentState.onRetry)
    }
  })
}

export function cleanupBootstrap(): void {
  if (unbindLocaleChange) {
    unbindLocaleChange()
    unbindLocaleChange = null
  }
}

export function resetBootstrapForTest(): void {
  cleanupBootstrap()
  mounted = false
  currentState = null
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

export function renderLoading(rootEl: HTMLElement): void {
  currentState = { type: 'loading', rootEl }
  ensureBootstrapSubscription()

  rootEl.innerHTML = `
    <div style="display:flex;align-items:center;justify-content:center;height:100vh;background:#0a0a0a;color:#737373;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:13px;" role="status" aria-live="polite">
      ${escapeHtml(t('common.loadingConfig'))}
    </div>
  `
}

export function renderError(rootEl: HTMLElement, message: string, onRetry: () => void): void {
  currentState = { type: 'error', rootEl, message, onRetry }
  ensureBootstrapSubscription()

  rootEl.innerHTML = `
    <div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;background:#0a0a0a;color:#f87171;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;padding:20px;box-sizing:border-box;text-align:center;" role="alert" aria-live="assertive">
      <h2 style="margin:0 0 12px 0;font-size:16px;font-weight:600;color:#f87171;">${escapeHtml(t('common.configError'))}</h2>
      <p style="margin:0 0 18px 0;max-width:540px;color:#d4d4d4;font-size:13px;line-height:1.5;word-break:break-word;">${escapeHtml(message)}</p>
      <button id="vtc-retry-btn" type="button" style="background:#262626;color:#e5e5e5;border:1px solid #404040;border-radius:4px;padding:6px 16px;font-size:13px;cursor:pointer;font-family:inherit;">
        ${escapeHtml(t('common.retry'))}
      </button>
    </div>
  `
  const btn = rootEl.querySelector('#vtc-retry-btn') as HTMLButtonElement | null
  if (btn) {
    btn.onclick = () => {
      btn.disabled = true
      onRetry()
    }
  }
}

export async function bootstrapApp(
  rootEl: HTMLElement,
  fetchFn?: typeof fetch,
  mountFn?: (el: HTMLElement) => void,
): Promise<boolean> {
  if (mounted) return true

  renderLoading(rootEl)

  try {
    await fetchClientConfig(fetchFn)
    if (mounted) return true
    mounted = true
    cleanupBootstrap()
    currentState = null
    rootEl.innerHTML = ''
    if (mountFn) {
      mountFn(rootEl)
    }
    return true
  } catch (err) {
    if (mounted) return true
    const errMsg = err instanceof Error ? err.message : String(err)
    renderError(rootEl, errMsg, () => {
      void bootstrapApp(rootEl, fetchFn, mountFn)
    })
    return false
  }
}
