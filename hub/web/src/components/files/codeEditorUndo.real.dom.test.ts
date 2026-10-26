import test, { beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import '../../../test/dom.mjs'

import { EditorState, Compartment, EditorSelection } from '@codemirror/state'
import { EditorView, basicSetup } from 'codemirror'
import { undo, redo, isolateHistory } from '@codemirror/commands'
import { setLocale, resetLocaleForTest, onLocaleChange } from '../../i18n/index'
import { cmPhrases } from '../../i18n/editorPhrases'

beforeEach(() => {
  resetLocaleForTest('en')
})

afterEach(() => {
  resetLocaleForTest('en')
})

test('Real CodeMirror regression: dynamic phrase reconfiguration preserves editor state, selection, and undo/redo history', () => {
  resetLocaleForTest('en')
  const phraseCompartment = new Compartment()
  const parent = document.createElement('div')
  document.body.appendChild(parent)

  const view = new EditorView({
    parent,
    extensions: [
      basicSetup,
      phraseCompartment.of(EditorState.phrases.of(cmPhrases['en'] ?? {})),
    ],
    doc: 'const a = 1;\n',
  })

  const unregisterLocale = onLocaleChange((locale) => {
    view.dispatch({
      effects: phraseCompartment.reconfigure(EditorState.phrases.of(cmPhrases[locale] ?? {})),
    })
  })

  try {
    // 1. Initial English state
    assert.equal(view.state.doc.toString(), 'const a = 1;\n')
    assert.equal(view.state.phrase('Find'), 'Find')
    assert.equal(view.state.phrase('Replace'), 'Replace')

    // 2. Perform editing transactions and set selection
    view.dispatch({
      changes: { from: 13, to: 13, insert: 'const b = 2;\n' },
      selection: EditorSelection.single(13, 25),
    })
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\n')
    assert.equal(view.state.selection.main.from, 13)
    assert.equal(view.state.selection.main.to, 25)

    // Perform second edit to build multi-level undo history
    view.dispatch({
      changes: { from: 26, to: 26, insert: 'console.log(a + b);' },
      selection: EditorSelection.cursor(45),
      annotations: [isolateHistory.of('before')],
    })
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\nconsole.log(a + b);')
    assert.equal(view.state.selection.main.from, 45)
    assert.equal(view.state.selection.main.to, 45)

    // 3. Switch locale live to Simplified Chinese
    setLocale('zh-CN')

    // 4. Verify phrases reconfigured dynamically
    assert.equal(view.state.phrase('Find'), '查找')
    assert.equal(view.state.phrase('Replace'), '替换')
    assert.equal(view.state.phrase('next'), '下一个')
    assert.equal(view.state.phrase('previous'), '上一个')
    assert.equal(view.state.phrase('match case'), '区分大小写')

    // 5. Verify document text and selection were NOT lost or reset by phrase reconfiguration
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\nconsole.log(a + b);')
    assert.equal(view.state.selection.main.from, 45)
    assert.equal(view.state.selection.main.to, 45)

    // 6. Execute real undo across locale reconfiguration:
    // Undo 2nd change
    assert.ok(undo(view), 'First undo must succeed')
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\n')

    // Undo 1st change
    assert.ok(undo(view), 'Second undo must succeed')
    assert.equal(view.state.doc.toString(), 'const a = 1;\n')

    // 7. Execute real redo
    assert.ok(redo(view), 'First redo must succeed')
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\n')

    assert.ok(redo(view), 'Second redo must succeed')
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\nconsole.log(a + b);')

    // 8. Switch back to English and verify continuity
    setLocale('en')
    assert.equal(view.state.phrase('Find'), 'Find')
    assert.equal(view.state.doc.toString(), 'const a = 1;\nconst b = 2;\nconsole.log(a + b);')
  } finally {
    unregisterLocale()
    view.destroy()
    parent.remove()
  }
})
