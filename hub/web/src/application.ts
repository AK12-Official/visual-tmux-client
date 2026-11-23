import { createApp } from 'vue'
import App from './App.vue'
import './style.css'
import '@xterm/xterm/css/xterm.css'
import { bootstrapApp } from './bootstrap'
import { i18n, setupLanguageChangeListener, t } from './i18n/index'

// Global error surface: surface any uncaught JS error / rejection as a
// visible banner so runtime failures are diagnosable without the console.
window.addEventListener('error', (e) => {
  const el = document.getElementById('global-error')
  if (el) {
    el.style.display = 'block'
    el.textContent = t('errors.jsError', {
      message: e.message,
      filename: e.filename,
      lineno: e.lineno,
    })
  }
})
window.addEventListener('unhandledrejection', (e) => {
  const el = document.getElementById('global-error')
  if (el) {
    el.style.display = 'block'
    el.textContent = t('errors.unhandledRejection', {
      reason: String(e.reason),
    })
  }
})

// In browser runtime, auto-bootstrap against #app element.
if (typeof document !== 'undefined') {
  setupLanguageChangeListener()
  const root = document.getElementById('app')
  if (root) {
    void bootstrapApp(root, undefined, (el) => {
      createApp(App).use(i18n).mount(el)
    })
  }
}
