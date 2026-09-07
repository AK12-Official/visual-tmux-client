## Why

The current product is a Wails desktop shell that mirrors tmux's pane geometry and renders one xterm.js instance per tmux pane. That rendering model is not self-consistent and produces visibly broken terminals:

- **No size negotiation exists anywhere.** There is no `refresh-client -C`, no `resize-window`, and no `pty.Setsize` call in the codebase (`engine/tmuxconn/conn.go:49` starts every control-mode subprocess at the pty default). tmux therefore lays out text for a size unrelated to the browser's CSS box, so line wrapping is wrong and full-screen TUIs (`vim`, `htop`, `less`) render mangled.
- **Scrollback loses all color and attributes.** Seeding uses `capture-pane -p -S -` without `-e` (`engine/tmuxconn/host.go:536,723`) and then joins plain lines, so a pane opens as flat grey text and only becomes colored once new output arrives.
- **The ring buffer cuts escape sequences in half.** `engine/ringbuffer/buffer.go:39-48` truncates on a raw 64 KB byte boundary with no awareness of escape-sequence framing, so seeded content can contain corrupt partial sequences.
- **The event bus silently drops events when full.** `engine/eventbus/bus.go:140-144` has a bare `default:` case, so under load pane output is permanently lost and the screen becomes unrecoverably wrong.
- **Control-mode flow control is parsed but ignored.** `%pause`/`%continue` are defined in `engine/tmuxcm/parser.go:37-38` but `host.go` has no handler, so high-volume output degrades badly.

These are not isolated bugs. They are consequences of a design that deliberately excluded terminal emulation from the engine ("Headless terminal emulation in the engine (still out of scope)", `archive/2026-09-07-interactive-tmux-shell/design.md:20`) while simultaneously rendering each tmux pane independently and never negotiating size. Those three commitments cannot hold at once, so no amount of styling work fixes the result — and styling has already been attempted against TmuxHub as a reference (`PaneGrid.vue:93`, `NavigationTree.vue:18`, `App.vue:221`) without resolving the dissatisfaction.

Separately, the desktop-only form factor rules out the actual use case: reaching long-lived tmux sessions from whatever device is at hand.

This change replaces the rendering model and the delivery model together, because switching to a browser/server architecture is what makes the correct rendering model available: let tmux render itself into one pty and stream those bytes verbatim.

## What Changes

- **BREAKING: tmux control mode (`tmux -CC`) is abandoned entirely.** Live terminal I/O becomes a pty running `tmux attach-session -t <name>`, whose raw bytes are streamed to the browser unmodified. Session metadata and mutations become one-shot `tmux` command executions with argv arrays.
- **BREAKING: the browser no longer positions tmux panes.** tmux draws its own splits, status line, and copy-mode inside a single terminal view. The engine no longer parses layout geometry, and the UI no longer owns a pane grid.
- **BREAKING: the Wails desktop shell is removed** and replaced by a Go HTTP/WebSocket server (the "hub") that serves a browser frontend. The product becomes browser-accessed.
- **BREAKING: the `tmux-engine` and `tmux-shell-ui` capabilities are retired**, with their requirements replaced by two new capabilities rather than amended — the assumptions underlying pane-geometry mirroring, per-pane output events, and ring-buffer scrollback recovery no longer exist.
- Real size negotiation is introduced: the browser reports its measured `cols`/`rows`, the hub applies them to the pty, and tmux reflows all of its own splits accordingly.
- Session management (list, create, rename, kill) is exposed over a JSON API and driven from a browser session list.
- Authentication is introduced as a hard requirement, because the product now listens on a port: a shared server token plus short-lived single-use WebSocket tickets.

Explicitly **out of scope** for this change (deferred, not rejected):

- Multi-host support and the reverse-dialing remote agent — the hub connects only to the tmux server on its own machine. The host-addressing seam is kept in the API shape so this can be added without breaking clients.
- Browser-side split workspace, drag-and-drop panes, pane zoom.
- Mobile-specific input (quick-keys toolbar, floating input, IME realtime sync), touch scrolling, PWA install.
- File manager, file transfer (ZMODEM/lrzsz/trzsz), clipboard image paste.
- Share links, custom actions/timers, scrollback export, themes beyond a single built-in dark theme, admin dashboards, OAuth, MySQL/Redis, AI-session browsing, remote browser proxy.

## Capabilities

### New Capabilities

- `session-hub`: the server-side capability — serves the browser frontend and a JSON session API, authenticates callers, executes tmux session queries and mutations, and bridges a browser WebSocket to a pty running `tmux attach-session`, including size negotiation, backpressure, and lifecycle/close semantics.
- `web-session-manager`: the browser-side capability — lists sessions and offers create/rename/kill, renders one attached session as an interactive terminal with correct sizing, and handles reconnection, terminal-level copy, and connection-state feedback.

### Modified Capabilities

- `tmux-engine`: all requirements describing control-mode connectivity, per-pane output events, window/pane topology mirroring, layout geometry, and ring-buffer scrollback recovery are **removed**. The capability is retired; its replacement behavior lives in `session-hub`.
- `tmux-shell-ui`: all requirements are **removed**. The Wails desktop shell capability is retired; its replacement behavior lives in `web-session-manager`.

## Impact

**Deleted** (~2,900 lines of non-test code plus ~1,500 lines of tests):

| Path | Lines | Reason |
|---|---|---|
| `engine/tmuxcm/parser.go` (+`parser_test.go`) | 340 (+278) | Control mode abandoned |
| `engine/tmuxcm/layout.go` (+`layout_test.go`) | 168 (+93) | Pane geometry no longer mirrored |
| `engine/tmuxconn/host.go`, `conn.go` (+`host_test.go`) | 1,045 (+970) | Replaced by pty attach + one-shot exec |
| `engine/eventbus/` | 162 (+141) | A pty byte stream is the event stream |
| `engine/domain/model.go` (+`model_test.go`) | 407 (+234) | Window/pane topology mirroring removed |
| `shell/` (Wails app, incl. `PaneGrid.vue`, `app.go`, `main.go`) | ~990 | Replaced by hub + browser frontend |

**Repurposed**: `engine/ringbuffer/` (66 lines, +63 tests) is kept but changes role — from per-pane scrollback retention to a bounded pre-`ready` output staging buffer. Its byte-boundary truncation must be replaced by whole-frame FIFO eviction.

**New**: a Go hub module (HTTP router, WebSocket, `tmux` exec wrapper, pty bridge, auth, ticket store) and a browser frontend (Vue 3 + xterm.js, reusing the existing `shell/frontend` toolchain).

**Dependencies**: adds a WebSocket library (`github.com/coder/websocket`) and keeps `github.com/creack/pty`. Removes `github.com/wailsapp/wails/v2` and the entire Wails toolchain. Frontend adds `@xterm/addon-webgl` is *not* added (see design.md); `@xterm/addon-unicode11` is added.

**Runtime posture change**: the product goes from an embedded webview with no listening socket to an HTTP server bound by default to `127.0.0.1`. Authentication and origin checking become mandatory, not optional.

**Specs**: `openspec/specs/tmux-engine/spec.md` and `openspec/specs/tmux-shell-ui/spec.md` are emptied by this change's deltas and replaced by `openspec/specs/session-hub/spec.md` and `openspec/specs/web-session-manager/spec.md`.
