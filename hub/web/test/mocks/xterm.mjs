export class Terminal {
  constructor(options) {
    this.options = options
    this.unicode = { activeVersion: '11' }
    this.cols = 80
    this.rows = 24
  }

  loadAddon() {}
  open() {}
  onData() {}
  onBinary() {}
  attachCustomKeyEventHandler() {}
  onResize() {}
  write() {}
  getSelection() {
    return null
  }
  focus() {}
  dispose() {}
}

export class FitAddon {
  constructor() {
    this.fitCount = 0
  }
  fit() {
    this.fitCount++
  }
  proposeDimensions() {
    return { cols: 80, rows: 24 }
  }
}

export class Unicode11Addon {}

export default {
  Terminal,
  FitAddon,
  Unicode11Addon,
}
