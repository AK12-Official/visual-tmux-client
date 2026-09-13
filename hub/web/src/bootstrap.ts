import { fetchClientConfig } from './config'

let mounted = false

export function resetBootstrapForTest(): void {
  mounted = false
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
  rootEl.innerHTML = `
    <div style="display:flex;align-items:center;justify-content:center;height:100vh;background:#0a0a0a;color:#737373;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:13px;" role="status" aria-live="polite">
      Loading configuration...
    </div>
  `
}

export function renderError(rootEl: HTMLElement, message: string, onRetry: () => void): void {
  rootEl.innerHTML = `
    <div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;background:#0a0a0a;color:#f87171;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;padding:20px;box-sizing:border-box;text-align:center;" role="alert" aria-live="assertive">
      <h2 style="margin:0 0 12px 0;font-size:16px;font-weight:600;color:#f87171;">Configuration Error</h2>
      <p style="margin:0 0 18px 0;max-width:540px;color:#d4d4d4;font-size:13px;line-height:1.5;word-break:break-word;">${escapeHtml(message)}</p>
      <button id="vtc-retry-btn" type="button" style="background:#262626;color:#e5e5e5;border:1px solid #404040;border-radius:4px;padding:6px 16px;font-size:13px;cursor:pointer;font-family:inherit;">
        Retry
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
