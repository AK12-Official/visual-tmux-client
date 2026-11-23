import { desktopWarning, initializeDesktop } from './desktop'

// Hydrate desktop storage before importing modules that read it at import time.
void initializeDesktop().then(async (desktop) => {
  if (desktop) {
    const { installDesktopLinks } = await import('./desktop-links')
    installDesktopLinks()
  }
  return import('./application')
}).catch(async (error: unknown) => {
  desktopWarning(error instanceof Error ? error.message : String(error))
  const root = document.getElementById('app')
  if (root) {
    // Reuse localized, accessible existing initialization error/retry UI. This
    // import on failure does not mount the workspace or start protected work.
    const { renderError } = await import('./bootstrap')
    renderError(root, error instanceof Error ? error.message : String(error), () => window.location.reload())
  }
})
