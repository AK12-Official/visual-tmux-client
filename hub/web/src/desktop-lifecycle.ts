import { desktopRequest, desktopWarning, flushDesktopPreferences, isDesktop } from './desktop'
import { syncDesktopFullscreen } from './desktop-window'

const FLUSH_TIMEOUT_MS = 5000

export async function prepareDesktopQuit(
  confirm: () => boolean,
  flush: () => Promise<void> = flushDesktopPreferences,
  timeoutMs = FLUSH_TIMEOUT_MS,
): Promise<boolean> {
  if (!confirm()) return false
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    await Promise.race([
      flush(),
      new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error('Preference save timed out')), timeoutMs) }),
    ])
    return true
  } catch (error) {
    desktopWarning(`Application remains open: ${error instanceof Error ? error.message : String(error)}`)
    return false
  } finally { clearTimeout(timer) }
}

// The external-URL WebView does not load the Wails runtime. Receive native
// requests through the same authenticated HTTP boundary as every other action.
export function installDesktopQuit(confirm: () => boolean): () => void {
  if (!isDesktop()) return () => {}
  let disposed = false
  const poll = async () => {
    while (!disposed) {
      try {
        const { id, fullscreen } = await desktopRequest<{ id: number; fullscreen?: boolean }>('quit-poll', {})
        if (disposed) return
        if (typeof fullscreen === 'boolean') syncDesktopFullscreen(fullscreen)
        if (!Number.isSafeInteger(id) || id <= 0) continue
        const approved = await prepareDesktopQuit(confirm)
        if (!disposed) await desktopRequest('quit-response', { id, approved })
      } catch (error) {
        if (disposed) return
        desktopWarning(error instanceof Error ? error.message : String(error))
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
    }
  }
  void desktopRequest('workspace-ready', {}).then(poll).catch((error: unknown) => {
    desktopWarning(error instanceof Error ? error.message : String(error))
  })
  return () => { disposed = true }
}
