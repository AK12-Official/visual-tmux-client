// TerminalSession owns one xterm.js instance attached to one tmux session over
// a WebSocket. It is the browser half of the byte-faithful streaming contract:
// it forwards xterm's keystrokes to the hub as binary frames and writes the
// hub's binary frames verbatim into xterm. It reports the measured terminal
// size (never assumed), debounces resize reporting, and reconnects with
// bounded backoff except when the server signalled a terminal end.
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { issueTicket, AuthError } from './api'
import { getConfig } from './config'

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

export function computeReconnectDelay(
  attempt: number,
  reconnectCfg = getConfig().web.reconnect,
): number {
  const safeAttempt = Math.max(0, Number.isFinite(attempt) ? Math.floor(attempt) : 0)
  return Math.min(reconnectCfg.max_delay, reconnectCfg.initial_delay * Math.pow(2, safeAttempt))
}

export function shouldThrottleActivity(
  lastActivityAt: number,
  now: number,
  throttle = getConfig().web.activity_throttle,
): boolean {
  return now - lastActivityAt < throttle
}

// Every live attachment, so a rename can reach the ones whose view is parked in
// the KeepAlive cache. A deactivated component never re-renders, so no prop can
// carry the new name to it: its terminal would keep the old one, and its next
// reconnect (a network blip is enough) would ask the hub for a session that no
// longer exists — ending the panel with "no longer exists" for a session that is
// very much alive, or, if the freed name has since been reused, attaching to an
// unrelated session.
const liveSessions = new Set<TerminalSession>()

/** renameTerminalSession retargets every live attachment currently bound to
 * `oldName`, including any whose view is cached rather than mounted. Session
 * names are unique server-side, so at most one live view is affected. */
export function renameTerminalSession(oldName: string, newName: string): void {
  for (const s of liveSessions) {
    if (s.name === oldName) s.setSession(newName)
  }
}

/** disposeTerminalSession tears down the attachment bound to `name`. It is for
 * a panel that has been retired (its session was killed, so nothing can ever
 * reach the view again): a KeepAlive-cached component is never unmounted, so
 * without this its xterm instance, scrollback and ResizeObserver would be
 * retained for the life of the page. */
export function disposeTerminalSession(name: string): void {
  for (const s of [...liveSessions]) {
    if (s.name === name) s.dispose()
  }
}

export class TerminalSession {
  private term: Terminal
  private fit: FitAddon
  private ws: WebSocket | null = null
  private generation = 0
  private attempt = 0
  private ended = false
  private disposed = false
  private resizeTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private observer: ResizeObserver
  private lastActivityAt = 0
  private container: HTMLElement
  private session: string
  private hooks: TerminalHooks

  constructor(
    container: HTMLElement,
    session: string,
    hooks: TerminalHooks,
    fontSize?: number,
  ) {
    this.container = container
    this.session = session
    this.hooks = hooks
    liveSessions.add(this)
    const termCfg = getConfig().web.terminal
    const resolvedFontSize = fontSize ?? termCfg.font_size
    this.term = new Terminal({
      // The unicode API (unicode11 addon + term.unicode.activeVersion) is a
      // proposed API in xterm 6.x; accessing it throws unless this is set.
      allowProposedApi: true,
      scrollback: termCfg.scrollback,
      fontFamily:
        'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
      fontSize: resolvedFontSize,
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

    // Some mouse reports are not UTF-8 conformant and arrive on onBinary
    // instead of onData, where each string char is already one byte. Encoding
    // those as UTF-8 would corrupt them, so they take a latin1 path.
    this.term.onBinary((data) => {
      this.sendRawBinary(data)
    })

    // Copy: Ctrl/Cmd+C with a selection copies to the clipboard; without a
    // selection, let xterm send 0x03 (SIGINT) as normal.
    this.term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true
      const isCopy = !e.altKey && !e.shiftKey && (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c'
      if (!isCopy) return true
      const selection = this.term.getSelection()
      if (!selection) return true
      // navigator.clipboard exists only in a secure context. Served over plain
      // HTTP to anything but localhost — the documented LAN deployment — it is
      // undefined, and calling .writeText on it throws a TypeError out of
      // xterm's keydown dispatch: an uncaught error that trips main.ts's global
      // banner and aborts the rest of xterm's key handling. Swallow the key in
      // that case too, because falling through would send 0x03 (SIGINT) to the
      // shell, which is emphatically not what a copy gesture asked for.
      const clipboard =
        typeof navigator !== 'undefined' ? (navigator.clipboard as Clipboard | undefined) : undefined
      if (!clipboard) {
        this.hooks.onNotice(
          'Copy failed: the browser exposes no clipboard outside a secure context (HTTPS or localhost).',
          'warning',
        )
        return false
      }
      clipboard.writeText(selection).catch(() => {
        // A blocked clipboard is a degraded-but-recoverable state, not a
        // failure of the terminal itself: warn rather than error.
        this.hooks.onNotice('Copy failed: the browser blocked the clipboard write.', 'warning')
      })
      return false
    })

    // Container resize -> debounced fit -> term.onResize reports the new size.
    this.observer = new ResizeObserver(() => {
      if (this.resizeTimer) clearTimeout(this.resizeTimer)
      this.resizeTimer = setTimeout(() => this.refit(), getConfig().web.resize_debounce)
    })
    this.observer.observe(container)

    this.term.onResize(({ cols, rows }) => {
      this.sendResize(cols, rows)
    })

    if (typeof window !== 'undefined') {
      window.addEventListener('online', this.handleOnline)
      window.addEventListener('offline', this.handleOffline)
    }

    this.refit()
    void this.connect()
  }

  private handleOnline = (): void => {
    if (this.disposed || this.ended) return
    if (this.ws && this.ws.readyState === WebSocket.OPEN) return
    this.attempt = 0
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.hooks.onState('reconnecting', 'Network restored, reconnecting…')
    void this.connect()
  }

  private handleOffline = (): void => {
    if (this.disposed || this.ended) return
    this.hooks.onState('reconnecting', 'Network offline')
  }

  refit(): void {
    if (this.disposed) return
    if (this.container && this.container.clientWidth > 0 && this.container.clientHeight > 0) {
      try {
        this.fit.fit()
      } catch {
        // transient zero-size or detachment layout glitch
      }
    }
  }

  focus(): void {
    if (!this.disposed && this.term && typeof this.term.focus === 'function') {
      this.term.focus()
    }
  }

  // Live font-size change from the terminal header. The re-fit re-measures
  // the grid and reports the new dimensions, so tmux re-lays-out its content.
  setFontSize(px: number): void {
    this.term.options.fontSize = px
    this.refit()
  }

  // Follow a rename. The live attachment itself needs no action: the tmux
  // client is bound to the session, not to its name, so it keeps streaming the
  // renamed session. But every future reconnect issues a ticket and builds a ws
  // URL from this name, so leaving it stale would make each reconnect ask for a
  // session that no longer exists and answer session_not_found forever.
  setSession(name: string): void {
    this.session = name
  }

  /** The session this attachment is currently bound to. Callers that emit the
   * name upward must use this rather than a prop: a KeepAlive-cached view's
   * props are frozen at the moment it was deactivated. */
  get name(): string {
    return this.session
  }

  private fireActivity(): void {
    if (!this.hooks.onActivity) return
    const now = Date.now()
    if (shouldThrottleActivity(this.lastActivityAt, now)) return
    this.lastActivityAt = now
    this.hooks.onActivity()
  }

  // The size reported to the hub must be the grid the user actually sees.
  // proposeDimensions() measures the container via getComputedStyle, which
  // yields NaN while the element is detached (a KeepAlive-cached terminal
  // reconnecting in the background) — negotiating NaN/0 would resize tmux to
  // a bogus geometry for every client. Fall back to xterm's live grid.
  private proposedSize(): { cols: number; rows: number } {
    try {
      const d = this.fit.proposeDimensions()
      if (d && Number.isFinite(d.cols) && Number.isFinite(d.rows)) {
        const cols = Math.floor(d.cols)
        const rows = Math.floor(d.rows)
        if (cols >= 1 && rows >= 1) {
          return { cols, rows }
        }
      }
    } catch {
      // fit addon may throw if container is detached, hidden, or zero-sized
    }
    if (this.term && Number.isFinite(this.term.cols) && Number.isFinite(this.term.rows)) {
      const cols = Math.floor(this.term.cols)
      const rows = Math.floor(this.term.rows)
      if (cols >= 1 && rows >= 1) {
        return { cols, rows }
      }
    }
    return { cols: 80, rows: 24 }
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
      if (this.ws) {
        this.ws.onopen = null
        this.ws.onmessage = null
        this.ws.onclose = null
        this.ws.onerror = null
        try {
          this.ws.close()
        } catch {
          /* ignore */
        }
        this.ws = null
      }
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
        const { cols, rows } = this.proposedSize()
        this.sendResize(cols, rows)
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
        if (!this.ended && !this.disposed) {
          this.hooks.onState('reconnecting')
          this.scheduleReconnect()
        }
      }
      ws.onerror = () => {
        // onclose follows and drives reconnection
      }
    } catch (err) {
      if (this.disposed || gen !== this.generation) return
      const message = err instanceof Error ? err.message : String(err)
      if (err instanceof AuthError || message.includes('AUTH_FAILED')) {
        this.ended = true
        this.hooks.onState('ended', 'Authentication failed')
        this.hooks.onNotice('Session connection ended: authentication failed.')
        return
      }
      if (message.includes('session_not_found')) {
        // The session is gone; no retry can ever succeed. End instead of
        // retrying quietly forever (the spec forbids silent indefinite retry).
        this.ended = true
        this.hooks.onState('ended', `Session "${this.session}" no longer exists.`)
        this.hooks.onNotice(`Session "${this.session}" no longer exists.`)
        return
      }
      if (this.attempt === 0) {
        this.hooks.onNotice(message)
      }
      this.scheduleReconnect()
    }
  }

  private handleControl(raw: string): void {
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      return
    }
    if (!parsed || typeof parsed !== 'object') return
    const msg = parsed as { type?: unknown; message?: unknown; retryable?: unknown; code?: unknown }
    if (typeof msg.type !== 'string') return

    switch (msg.type) {
      case 'ready':
        break
      case 'exit':
        this.ended = true
        this.hooks.onState(
          'ended',
          typeof msg.code === 'number' && msg.code !== 0 ? `Process exited with code ${msg.code}` : undefined,
        )
        break
      case 'error': {
        const errorMsg = typeof msg.message === 'string' ? msg.message : 'Unknown server error'
        this.hooks.onNotice(errorMsg)
        if (msg.retryable === false) {
          this.ended = true
          // Carry the refusal reason so the ended overlay can explain itself.
          this.hooks.onState('ended', errorMsg)
        }
        break
      }
      case 'pong':
        break
    }
  }

  private scheduleReconnect(): void {
    if (this.ended || this.disposed) return
    const delay = computeReconnectDelay(this.attempt)
    this.attempt++
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      void this.connect()
    }, delay)
  }

  private sendBinary(data: string): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(new TextEncoder().encode(data))
    }
  }

  // Byte-per-char payloads (onBinary) must not be re-encoded as UTF-8.
  private sendRawBinary(data: string): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      const bytes = new Uint8Array(data.length)
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff
      this.ws.send(bytes)
    }
  }

  private sendResize(cols: number, rows: number): void {
    if (
      Number.isInteger(cols) &&
      Number.isInteger(rows) &&
      cols >= 1 &&
      rows >= 1 &&
      this.ws &&
      this.ws.readyState === WebSocket.OPEN
    ) {
      this.ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  }

  dispose(): void {
    if (this.disposed) return
    this.disposed = true
    liveSessions.delete(this)
    this.generation++
    if (this.resizeTimer) clearTimeout(this.resizeTimer)
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.observer.disconnect()
    if (typeof window !== 'undefined') {
      window.removeEventListener('online', this.handleOnline)
      window.removeEventListener('offline', this.handleOffline)
    }
    if (this.ws) {
      this.ws.onopen = null
      this.ws.onmessage = null
      this.ws.onclose = null
      this.ws.onerror = null
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
