// Minimal CodeMirror stand-in for the node test environment. The editor only
// exists in the browser, so this keeps the module graph resolvable -- and it
// carries just enough behaviour for the component tests to type into an editor:
// a document that a change is applied to, the update listener the editor
// forwards its changes to, and a record of which instances were created and
// destroyed, which is how a test sees an editor survive being switched away from.

/** instances are every EditorView constructed since the last reset, in order. */
export const instances = []

/** reset forgets every instance, so one test cannot see another's editor. */
export function reset() {
  instances.length = 0
}

export class EditorView {
  constructor(options) {
    this.options = options
    this.doc = String(options.doc ?? '')
    this.destroyed = false
    // The extensions the editor is built with, minus the ones a real CodeMirror
    // would treat differently: this stand-in only understands a bare listener,
    // which is what updateListener.of returns.
    this.listeners = (options.extensions ?? []).filter(
      (extension) => typeof extension === 'function',
    )
    this.state = { doc: { toString: () => this.doc } }
    instances.push(this)
  }

  /**
   * dispatch applies a replacement to the document and tells the listeners,
   * which is what the real editor does for an external value written back in and
   * what a test calls to type.
   */
  dispatch(update) {
    const change = update?.changes
    if (change) {
      const insert = String(change.insert ?? '')
      this.doc = this.doc.slice(0, change.from) + insert + this.doc.slice(change.to)
    }
    const applied = { docChanged: true, state: { doc: { toString: () => this.doc } } }
    for (const listener of this.listeners) listener(applied)
  }

  requestMeasure() {}

  focus() {}

  destroy() {
    this.destroyed = true
  }

  get dom() {
    return undefined
  }
}

EditorView.updateListener = { of: (listener) => listener }

export class EditorState {
  static create(config) {
    return { config }
  }

  static readonly() {
    return {}
  }
}

export const basicSetup = []

export const keymap = {
  of: (bindings) => bindings,
}
