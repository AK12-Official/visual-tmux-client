import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../../../test/dom.mjs'

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import CodeEditor from './CodeEditor.vue'
import { instances, reset as resetCM } from '../../../test/mocks/codemirror.mjs'
import { setLocale, resetLocaleForTest } from '../../i18n/index'

beforeEach(() => {
  resetCM()
  resetLocaleForTest('en')
})

afterEach(() => {
  resetCM()
  resetLocaleForTest('en')
})

test('CodeMirror built-in phrases update live on locale changes without recreating the editor', async () => {
  resetLocaleForTest('en')
  const wrapper = mount(CodeEditor, {
    props: {
      modelValue: 'const a = 1;',
      active: true,
    },
  })
  await nextTick()

  assert.equal(instances.length, 1, 'Exactly one editor instance must be created')
  const editorInstance = instances[0]

  // In English mode, phrases fall back to keys
  assert.equal(editorInstance.state.phrase('Find'), 'Find')
  assert.equal(editorInstance.state.phrase('Replace'), 'Replace')
  assert.equal(editorInstance.state.phrase('next'), 'next')

  // Type something into editor
  editorInstance.dispatch({ changes: { from: 12, to: 12, insert: '\nconst b = 2;' } })
  assert.equal(editorInstance.doc, 'const a = 1;\nconst b = 2;')

  // Switch locale to Simplified Chinese
  setLocale('zh-CN')
  await nextTick()

  // Verify editor instance was NOT recreated
  assert.equal(instances.length, 1, 'Editor must not be recreated on locale change')
  assert.equal(instances[0], editorInstance, 'Editor instance identity must be preserved')

  // Verify phrases are updated to Chinese
  assert.equal(editorInstance.state.phrase('Find'), '查找')
  assert.equal(editorInstance.state.phrase('Replace'), '替换')
  assert.equal(editorInstance.state.phrase('next'), '下一个')
  assert.equal(editorInstance.state.phrase('previous'), '上一个')
  assert.equal(editorInstance.state.phrase('match case'), '区分大小写')

  // Verify draft content is preserved
  assert.equal(editorInstance.doc, 'const a = 1;\nconst b = 2;')

  // Switch back to English
  setLocale('en')
  await nextTick()

  assert.equal(instances.length, 1, 'Editor must remain the same instance')
  assert.equal(editorInstance.state.phrase('Find'), 'Find')
  assert.equal(editorInstance.doc, 'const a = 1;\nconst b = 2;')

  wrapper.unmount()
})
