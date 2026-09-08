// TerminalSession owns one xterm.js instance attached to one tmux session over
// a WebSocket. It is the browser half of the byte-faithful streaming contract:
// it forwards xterm's keystrokes to the hub as binary frames and writes the
// hub's binary frames verbatim into xterm. It reports the measured terminal
// size (never assumed), debounces resize reporting, and reconnects with
// bounded backoff except when the server signalled a terminal end.
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { issueTicket } from './api'

export type ConnState = 'connecting' | 'connected' | 'reconnecting' | 'ended'

/** Notice levels mirror the toast levels; omitted means 'error'. */
export type NoticeLevel = 'error' | 'warning'

export interface TerminalHooks {
  onState: (state: ConnState, detail?: string) => void
  onNotice: (message: string, level?: NoticeLevel) => void
  /** Fires when the session produces output, throttled to roughly one call
   * per ACTIVITY_THROTTLE_MS. Drives the list's activity highlights. */
  onActivity?: () => void
}

// Dark terminal palette modeled on Visual Tmux Client's default-dark theme, matching the
// chrome tokens in style.css so the terminal reads as one surface.
const THEME = {
  background: '#0a0a0a',
  foreground: '#e5e5e5',
  cursor: '#e5e5e5',
  black: '#1a1a1a',
  brightBlack: '#525252',
  red: '#f87171',
  brightRed: '#fca5a5',
  green: '#4ade80',
  brightGreen: '#86efac',
  yellow: '#facc15',
  brightYellow: '#fde047',
  blue: '#60a5fa',
  brightBlue: '#93c5fd',
  magenta: '#c084fc',
  brightMagenta: '#d8b4fe',
  cyan: '#22d3ee',
  brightCyan: '#67e8f9',
  white: '#e5e5e5',
  brightWhite: '#ffffff',
}

const RESIZE_DEBOUNCE_MS = 100
const MAX_RECONNECT_DELAY_MS = 3000
const BASE_RECONNECT_DELAY_MS = 500
// Activity events only drive a highlight, so a coarse throttle is enough and
// keeps the hook cheap even under heavy output.
const ACTIVITY_THROTTLE_MS = 500

export class TerminalSession {
  private term: Terminal
  private fit: FitAddon
  private ws: WebSocket | null = null
  private generation = 0
  private attempt = 0
  private ended = false
  private disposed = false
  private resizeTimer: ReturnType<typeof setTimeout> | null = null
  private observer: ResizeObserver
  private lastActivityAt = 0

  constructor(
    private container: HTMLElement,
    private session: string,
    private hooks: TerminalHooks,
    fontSize = 13,
  ) {
    this.term = new Terminal({
      // The unicode API (unicode11 addon + term.unicode.activeVersion) is a
      // proposed API in xterm 6.x; accessing it throws unless this is set.
      allowProposedApi: true,
      convertEol: true,
      scrollback: 5000,
      fontFamily:
        'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
      fontSize,
      theme: THEME,
    })
    this.fit = new FitAddon()
    this.term.loadAddon(this.fit)
    // unicode11 gives correct wide-character (CJK/emoji) width computation,
    // preventing cursor drift. The DOM renderer is kept (not WebGL) so CJK
    // glyphs fall back to system fonts rather than rendering as blanks.
    this.term.loadAddon(new Unicode11Addon())
    this.term.unicode.activeVersion = '11'

    this.term.open(container)

    // Input: forward every keystroke/paste verbatim as a binary frame.
    this.term.onData((data) => {
      this.sendBinary(data)
    })

    // Copy: Ctrl/Cmd+C with a selection copies to the clipboard; without a
    // selection, let xterm send 0x03 (SIGINT) as normal.
    this.term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true
      const isCopy = !e.altKey && !e.shiftKey && (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c'
      if (!isCopy) return true
      const selection = this.term.getSelection()
      if (!selection) return true
      navigator.clipboard.writeText(selection).catch(() => {
        // A blocked clipboard is a degraded-but-recoverable state, not a
        // failure of the terminal itself: warn rather than error.
        this.hooks.onNotice('Copy failed: the browser blocked the clipboard write.', 'warning')
      })
      return false
    })

    // Container resize -> debounced fit -> term.onResize reports the new size.
    this.observer = new ResizeObserver(() => {
      if (this.resizeTimer) clearTimeout(this.resizeTimer)
      this.resizeTimer = setTimeout(() => this.refit(), RESIZE_DEBOUNCE_MS)
    })
    this.observer.observe(container)

    this.term.onResize(({ cols, rows }) => {
      this.sendResize(cols, rows)
    })

    this.refit()
    void this.connect()
  }

  private refit(): void {
    if (this.container.clientWidth > 0 && this.container.clientHeight > 0) {
      try {
        this.fit.fit()
      } catch {
        // transient zero-size layout glitch
      }
    }
  }

  // Live font-size change from the terminal header. The re-fit re-measures
  // the grid and reports the new dimensions, so tmux re-lays-out its content.
  setFontSize(px: number): void {
    this.term.options.fontSize = px
    this.refit()
  }

  private fireActivity(): void {
    if (!this.hooks.onActivity) return
    const now = Date.now()
    if (now - this.lastActivityAt < ACTIVITY_THROTTLE_MS) return
    this.lastActivityAt = now
    this.hooks.onActivity()
  }

  private proposedSize(): { cols: number; rows: number } {
    const d = this.fit.proposeDimensions()
    return d ? { cols: d.cols, rows: d.rows } : { cols: 80, rows: 24 }
  }

  private async connect(): Promise<void> {
    if (this.disposed) return
    const gen = ++this.generation
    this.ended = false
    this.hooks.onState(this.attempt === 0 ? 'connecting' : 'reconnecting')
    try {
      const ticket = await issueTicket(this.session)
      if (this.disposed || gen !== this.generation) return
      const { cols, rows } = this.proposedSize()
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      // The long-lived token never appears here: only the single-use ticket.
      const url =
        `${proto}://${location.host}/ws/local/${encodeURIComponent(this.session)}` +
        `?ticket=${encodeURIComponent(ticket)}&cols=${cols}&rows=${rows}`
      const ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'
      this.ws = ws
      ws.onopen = () => {
        if (gen !== this.generation) {
          ws.close()
          return
        }
        this.attempt = 0
        this.hooks.onState('connected')
      }
      ws.onmessage = (ev) => {
        if (gen !== this.generation) return
        if (typeof ev.data === 'string') {
          this.handleControl(ev.data)
        } else {
          this.term.write(new Uint8Array(ev.data))
          this.fireActivity()
        }
      }
      ws.onclose = () => {
        if (gen !== this.generation) return
        if (!this.ended && !this.disposed) this.scheduleReconnect()
      }
      ws.onerror = () => {
        // onclose follows and drives reconnection
      }
    } catch (err) {
      if (this.disposed || gen !== this.generation) return
      const message = err instanceof Error ? err.message : String(err)
      if (message.includes('session_not_found')) {
        // The session is gone; no retry can ever succeed. End instead of
        // retrying quietly forever (the spec forbids silent indefinite retry).
        this.ended = true
        this.hooks.onState('ended')
        this.hooks.onNotice(`Session "${this.session}" no longer exists.`)
        return
      }
      this.hooks.onNotice(message)
      this.scheduleReconnect()
    }
  }

  private handleControl(raw: string): void {
    let msg: { type: string; message?: string; retryable?: boolean }
    try {
      msg = JSON.parse(raw)
    } catch {
      return
    }
    switch (msg.type) {
      case 'ready':
        break
      case 'exit':
        this.ended = true
        this.hooks.onState('ended')
        break
      case 'error':
        if (msg.message) this.hooks.onNotice(msg.message)
        if (!msg.retryable) {
          this.ended = true
          // Carry the refusal reason so the ended overlay can explain itself.
          this.hooks.onState('ended', msg.message)
        }
        break
      case 'pong':
        break
    }
  }

  private scheduleReconnect(): void {
    if (this.ended || this.disposed) return
    const delay = Math.min(MAX_RECONNECT_DELAY_MS, BASE_RECONNECT_DELAY_MS * Math.pow(2, this.attempt))
    this.attempt++
    setTimeout(() => void this.connect(), delay)
  }

  private sendBinary(data: string): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(new TextEncoder().encode(data))
    }
  }

  private sendResize(cols: number, rows: number): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  }

  dispose(): void {
    this.disposed = true
    this.generation++
    if (this.resizeTimer) clearTimeout(this.resizeTimer)
    this.observer.disconnect()
    if (this.ws) {
      try {
        this.ws.close()
      } catch {
        /* ignore */
      }
      this.ws = null
    }
    this.term.dispose()
  }
}
