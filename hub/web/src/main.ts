import {createApp} from 'vue'
import App from './App.vue'
import './style.css';
import '@xterm/xterm/css/xterm.css'

// Global error surface: surface any uncaught JS error / rejection as a
// visible banner so runtime failures are diagnosable without the console.
window.addEventListener('error', (e) => {
  const el = document.getElementById('global-error')
  if (el) {
    el.style.display = 'block'
    el.textContent = `JS Error: ${e.message} (${e.filename}:${e.lineno})`
  }
})
window.addEventListener('unhandledrejection', (e) => {
  const el = document.getElementById('global-error')
  if (el) {
    el.style.display = 'block'
    el.textContent = `Unhandled rejection: ${String(e.reason)}`
  }
})

createApp(App).mount('#app')
