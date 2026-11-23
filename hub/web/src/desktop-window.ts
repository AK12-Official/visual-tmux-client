// Native fullscreen does not populate document.fullscreenElement. Keep the
// terminal region and OS window in sync, including OS-initiated exits.
let target: HTMLElement | undefined
let fullscreen = false

export function desktopFullscreen(): boolean { return fullscreen }

export function setDesktopFullscreenTarget(element: HTMLElement): void { target = element }

export function syncDesktopFullscreen(enabled: boolean): void {
  if (fullscreen === enabled) return
  fullscreen = enabled
  target?.classList.toggle('app__main--desktop-fullscreen', enabled)
  window.dispatchEvent(new Event('resize'))
}
