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
   *
   * Two details are the editor's and not an invention, because a stand-in that
   * is merely plausible is a green test over something no user could produce:
   *
   *   - Every position in a transaction is read against the document as it was
   *     *before* it, not as it stands after an earlier change in the same list.
   *     Applying them from the last to the first is what keeps the earlier
   *     positions valid; applying them in order would compose positions that were
   *     never meant to compose.
   *   - A transaction is a change unless every part of it is empty. Comparing the
   *     text instead would call an identical replacement no change at all, where
   *     the editor reports one: the document is the same, but the change set is
   *     not empty and a listener still runs.
   */
  dispatch(update) {
    const changes = Array.isArray(update?.changes) ? update.changes : [update?.changes]
    const applied = changes.filter(Boolean)
    for (const change of [...applied].sort((a, b) => b.from - a.from)) {
      const insert = String(change.insert ?? '')
      this.doc = this.doc.slice(0, change.from) + insert + this.doc.slice(change.to)
    }
    const changed = applied.some(
      (change) => change.from !== change.to || String(change.insert ?? '') !== '',
    )
    if (!changed) return
    const state = { docChanged: true, state: { doc: { toString: () => this.doc } } }
    for (const listener of this.listeners) listener(state)
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
