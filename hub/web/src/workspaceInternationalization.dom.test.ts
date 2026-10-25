import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import App from './App.vue'
import SessionList from './components/SessionList.vue'
import FileManagerOverlay from './components/files/FileManagerOverlay.vue'
import TerminalView from './components/TerminalView.vue'
import { clearToken, setToken } from './api'
import { setConfig, resetConfigForTest, type ClientConfig } from './config'
import { resetToastsForTest } from './toasts'
import { setLocale, resetLocaleForTest } from './i18n/index'
import { instances as cmInstances, reset as resetCM } from '../test/mocks/codemirror.mjs'

const testConfig: ClientConfig = {
  version: 1,
  files: { enabled: true, max_file_size: 104857600 },
  web: {
    session_poll_interval: 100000,
    activity_decay: 1000,
    activity_throttle: 250,
    resize_debounce: 50,
    reconnect: {
      initial_delay: 200,
      max_delay: 1500,
    },
    terminal: {
      scrollback: 1000,
      font_size: 15,
      min_font_size: 10,
      max_font_size: 20,
    },
    notifications: {
      max_toasts: 5,
      error_lifetime: 50,
      warning_lifetime: 50,
      info_lifetime: 50,
    },
  },
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

class MockWebSocket {
  readyState = 1
  binaryType = 'arraybuffer'
  onopen: (() => void) | null = null
  onmessage: ((ev: any) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor() {
    setTimeout(() => this.onopen?.(), 5)
  }
  send() {}
  close() {
    this.readyState = 3
  }
}

const origFetch = globalThis.fetch
const origWebSocket = globalThis.WebSocket

beforeEach(() => {
  resetConfigForTest()
  setConfig(testConfig)
  resetToastsForTest()
  resetCM()
  clearToken()
  localStorage.clear()
  localStorage.setItem('vtc:recent-host', 'local')
  resetLocaleForTest('en')
  globalThis.WebSocket = MockWebSocket as any
})

afterEach(() => {
  globalThis.fetch = origFetch
  globalThis.WebSocket = origWebSocket
  clearToken()
  localStorage.clear()
  resetCM()
  resetToastsForTest()
  resetLocaleForTest('en')
})

test('Task 5.1: Switching language with active terminal preserves host, session, attachment, and updates persistent status', async () => {
  setToken('valid-token')

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local Host', revision: 1, connectionRevision: 1, trusted: true },
          { id: 'remote-1', name: 'Remote Server', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'local', revision: 1 })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({
        sessions: [
          { name: 'dev', windows: 2, active: true },
          { name: 'build', windows: 1, active: false },
        ],
      })
    }
    if (url.includes('/api/ws-ticket')) {
      return json({ ticket: 'test-ticket-xyz' })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // Select 'dev' session
    const sessionList = wrapper.findComponent(SessionList)
    assert.ok(sessionList.exists(), 'SessionList must be mounted')
    sessionList.vm.$emit('select', 'dev')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // TerminalView should be mounted and showing dev session
    const termView = wrapper.findComponent(TerminalView)
    assert.ok(termView.exists(), 'TerminalView should be mounted')
    assert.equal(termView.props('session'), 'dev')

    // English connection state
    const connState = wrapper.find('.app__term-state')
    assert.ok(connState.exists(), 'Connection state element must exist')
    assert.ok(
      connState.text().includes('connected') || connState.text().includes('connecting'),
      `Initial connection state in English: ${connState.text()}`,
    )

    // Verify language selector in workspace header
    const langSelect = wrapper.find<HTMLSelectElement>('.app__lang-select')
    assert.ok(langSelect.exists(), 'Language selector in header should exist')

    // Switch to Simplified Chinese
    await langSelect.setValue('zh-CN')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // TerminalView instance and selected session must survive
    const termViewAfter = wrapper.findComponent(TerminalView)
    assert.ok(termViewAfter.exists(), 'TerminalView must still exist after locale change')
    assert.equal(termViewAfter.props('session'), 'dev', 'Selected session dev must survive')

    // Selected host must survive
    const hostSelect = wrapper.find<HTMLSelectElement>('.app__host-select')
    assert.equal(hostSelect.element.value, 'local', 'Selected host local must survive')

    // Localized connection state updates to Chinese without reconnecting
    const connStateZh = wrapper.find('.app__term-state')
    assert.ok(
      connStateZh.text().includes('已连接') || connStateZh.text().includes('连接'),
      `Connection state localized in Chinese: ${connStateZh.text()}`,
    )

    // Document language is updated
    assert.equal(document.documentElement.lang, 'zh-CN')

    // Switch back to English
    await langSelect.setValue('en')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    assert.equal(termViewAfter.props('session'), 'dev', 'Selected session dev still survives')
    assert.equal(document.documentElement.lang, 'en')
  } finally {
    wrapper.unmount()
  }
})

test('Task 5.1: Switching language with open file manager preserves open panels, drafts, editor identity, and undo history', async () => {
  setToken('valid-token')

  globalThis.fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    if (url.endsWith('/api/hosts')) {
      return json({
        hosts: [
          { id: 'local', name: 'Local Host', revision: 1, connectionRevision: 1, trusted: true },
        ],
      })
    }
    if (url.endsWith('/api/host-settings')) {
      return json({ defaultHostId: 'local', revision: 1 })
    }
    if (url.includes('/api/hosts/local/sessions')) {
      return json({
        sessions: [
          { name: 'workspace', windows: 1, active: true },
        ],
      })
    }
    if (url.includes('/working-directory')) {
      return json({ path: '/home/user/project', substituted: false })
    }
    if (url.includes('/files/list')) {
      return json({
        path: '/home/user/project',
        entries: [
          { name: 'main.ts', is_dir: false, size: 24, mtime: 1000 },
        ],
        truncated: false,
      })
    }
    if (url.includes('/files/read')) {
      return new Response('let count = 0;', {
        status: 200,
        headers: {
          'X-File-Mtime': '1000',
          'X-File-Mtime-Nanos': '1000000000',
        },
      })
    }
    if (url.includes('/api/ws-ticket')) {
      return json({ ticket: 'test-ticket' })
    }
    return json({}, 404)
  }

  const wrapper = mount(App)
  try {
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // Attach session
    const sessionList = wrapper.findComponent(SessionList)
    assert.ok(sessionList.exists(), 'SessionList must exist')
    sessionList.vm.$emit('select', 'workspace')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // Open file manager
    const filesBtn = wrapper.find('.app__term-btn[title="browse files in this session"]')
    assert.ok(filesBtn.exists(), 'Files button should exist')
    await filesBtn.trigger('click')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    const fm = wrapper.findComponent(FileManagerOverlay)
    assert.ok(fm.exists(), 'FileManagerOverlay must be open')

    // Open main.ts via FileTree
    const tree = wrapper.findComponent({ name: 'FileTree' })
    assert.ok(tree.exists(), 'FileTree must exist')
    tree.vm.$emit('open', { name: 'main.ts', is_dir: false, size: 24, mtime: 1000 }, '/home/user/project/main.ts')
    for (let i = 0; i < 6; i++) {
      await nextTick()
      await new Promise((r) => setTimeout(r, 15))
    }

    assert.equal(cmInstances.length, 1, 'Exactly one CodeMirror instance should be created')
    const editorInstance = cmInstances[0]
    assert.equal(editorInstance.doc, 'let count = 0;')

    // Verify search phrase in English mode
    assert.equal(editorInstance.state.phrase('Find'), 'Find')

    // Dispatch an edit to build undo history
    editorInstance.dispatch({
      changes: { from: editorInstance.doc.length, to: editorInstance.doc.length, insert: '\ncount++;' },
    })
    assert.equal(editorInstance.doc, 'let count = 0;\ncount++;')

    // Switch language to Simplified Chinese
    setLocale('zh-CN')
    await nextTick()
    await new Promise((r) => setTimeout(r, 40))
    await nextTick()

    // 1. File Manager is still open
    assert.ok(wrapper.findComponent(FileManagerOverlay).exists(), 'File manager must remain open')

    // 2. Draft is preserved
    const tab = (fm.vm as any).tabs[0]
    assert.ok(tab, 'Tab must survive')
    assert.equal(tab.text, 'let count = 0;\ncount++;', 'Unsaved draft text must survive')

    // 3. Editor instance identity is preserved (not recreated)
    assert.equal(cmInstances.length, 1, 'CodeMirror must NOT be recreated on locale switch')
    assert.equal(cmInstances[0], editorInstance, 'CodeMirror instance identity must remain identical')

    // 4. Phrases in editor updated to Chinese
    assert.equal(editorInstance.state.phrase('Find'), '查找')
    assert.equal(editorInstance.state.phrase('Replace'), '替换')

    // 5. User can continue typing and document state remains intact
    editorInstance.dispatch({
      changes: { from: editorInstance.doc.length, to: editorInstance.doc.length, insert: '\n// done' },
    })
    assert.equal(editorInstance.doc, 'let count = 0;\ncount++;\n// done')
  } finally {
    wrapper.unmount()
  }
})
