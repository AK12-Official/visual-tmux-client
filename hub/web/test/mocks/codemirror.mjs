// Minimal CodeMirror stand-in for the node test environment. The editor only
// exists in the browser; this keeps the module graph resolvable if a component
// under test ever imports it.

export class EditorView {
  constructor(options) {
    this.options = options
    this.state = { doc: { toString: () => '' } }
  }

  dispatch() {}
  destroy() {}
  focus() {}
  get dom() {
    return undefined
  }
}

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
