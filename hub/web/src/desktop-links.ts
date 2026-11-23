import { desktopRequest, desktopWarning } from './desktop'

export function desktopLinkTarget(href: string, origin: string): 'internal' | 'external' | 'blocked' {
  try {
    const url = new URL(href, origin)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'blocked'
    if (url.username || url.password) return 'blocked'
    return url.origin === origin ? 'internal' : 'external'
  } catch { return 'blocked' }
}

export function installDesktopLinks(): void {
  const handle = (event: MouseEvent) => {
    const target = event.target instanceof Element ? event.target.closest('a[href]') : null
    if (!(target instanceof HTMLAnchorElement)) return
    const kind = desktopLinkTarget(target.href, window.location.origin)
    if (kind === 'internal') return
    // Capture before preview/application handlers and prevent WebView navigation
    // even for unsupported URLs and programmatic synthetic anchor clicks.
    event.preventDefault()
    event.stopImmediatePropagation()
    if (kind !== 'external' || !event.isTrusted) return
    void desktopRequest('open-external', { url: target.href }).catch((error: unknown) => {
      desktopWarning(error instanceof Error ? error.message : String(error))
    })
  }
  document.addEventListener('click', handle, true)
  document.addEventListener('auxclick', handle, true)
}
