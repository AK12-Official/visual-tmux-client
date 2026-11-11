import { desktopRequest, isDesktop } from './desktop'
import { downloadFile } from './files/api'
import { desktopFullscreen, setDesktopFullscreenTarget, syncDesktopFullscreen } from './desktop-window'

// Session/host/file operations stay on existing APIs. Only the local destination
// and OS clipboard are platform actions, authorized by the desktop HTTP bridge.
export async function saveDownload(path: string, name: string, hostId: string, connectionRevision?: number): Promise<void> {
  if (isDesktop()) {
    if (!Number.isSafeInteger(connectionRevision) || (connectionRevision ?? 0) <= 0) throw new Error('host_revision_conflict')
    await desktopRequest('download', { path, hostId, connectionRevision })
    return
  }
  const blob = await downloadFile(path, hostId)
  const url = URL.createObjectURL(blob)
  try {
    const link = document.createElement('a')
    link.href = url; link.download = name; link.click()
  } finally { URL.revokeObjectURL(url) }
}

export async function copyText(text: string): Promise<void> {
  if (isDesktop()) { await desktopRequest('clipboard', { text }); return }
  if (typeof navigator === 'undefined' || !navigator.clipboard) throw new Error('clipboard_unavailable')
  await navigator.clipboard.writeText(text)
}

export function isFullscreen(): boolean { return isDesktop() ? desktopFullscreen() : !!document.fullscreenElement }

export async function enterFullscreen(element: HTMLElement): Promise<void> {
  if (!isDesktop()) return element.requestFullscreen()
  setDesktopFullscreenTarget(element)
  await desktopRequest('fullscreen', { enabled: true })
  syncDesktopFullscreen(true)
}

export async function leaveFullscreen(): Promise<void> {
  if (!isDesktop()) return document.exitFullscreen()
  await desktopRequest('fullscreen', { enabled: false })
  syncDesktopFullscreen(false)
}
