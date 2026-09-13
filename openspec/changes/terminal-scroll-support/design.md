## Context

When visual-tmux-client connects to a tmux session, it opens an xterm.js instance in the browser that communicates with `tmux attach-session` running under a pty via a WebSocket bridge. By default, tmux runs with `mouse off`, which instructs xterm to disable mouse reporting (`\e[?1000l \e[?1002l \e[?1006l`). Because tmux operates in the alternate screen buffer, xterm has no local scrollback lines, and because mouse reporting is disabled, wheel and trackpad scroll events are discarded.

In `hub/server.go`, the server currently executes `pinWindowSizePolicy` using `sync.Once` to set `window-size latest`. This does not enable `mouse on`, and fails to reapply settings if the tmux server exits and restarts.

## Goals / Non-Goals

**Goals:**
- Enable `mouse on` globally on the tmux server so wheel and trackpad scroll gestures navigate session scrollback history seamlessly.
- Automatically enter copy-mode on scroll up and exit copy-mode when scrolled back to the bottom.
- Ensure global options (`window-size latest` and `mouse on`) are checked and applied idempotently on session attach and creation without redundant `set-option` invocations that cause client redraws.
- Reapply options correctly even if the tmux server process terminates and a new one starts.
- Maintain compatibility with Shift-drag text selection and keyboard shortcuts.

**Non-Goals:**
- Implementing a custom DOM scrollbar overlay inside xterm.js (tmux natively handles viewport rendering and position indicators in copy-mode).
- Overriding user custom keybindings inside tmux.

## Decisions

### 1. Safe Idempotent Option Verification (`ensureGlobalOptions`)

Instead of a one-time `sync.Once` that never fires again after a tmux server restart, the hub will use `ensureGlobalOptions`:
- Early exit if tmux resolution failed (`s.tmux.err != nil`).
- Inspect `window-size` and `mouse` independently via `show-options -gv`:
  - If `window-size` is not `"latest"`, set `set-option -g window-size latest`.
  - If `mouse` is not `"on"`, set `set-option -g mouse on`.
- If an option is already at its desired value, no `set-option` command is executed for it, preventing spurious tmux client repaints and unnecessary process forks.
- A mutex (`optionsMu`) guards the check-and-set sequence to prevent race conditions across concurrent attachments.

*Alternatives considered:*
- Statically running `set-option -g` on every attach: causes tmux to repaint all attached clients, lighting up activity indicators in background panels.
- Keeping `sync.Once`: if all sessions are closed and the tmux daemon exits, subsequent sessions on a new daemon revert to `mouse off`.

### 2. Check on Attach and Session Creation

Call `ensureGlobalOptions` on:
- WebSocket terminal attachment in `hub/attach.go`
- Session creation in `hub/api.go` (`createSession`)

This guarantees that whether a session is created via the web UI or attached from an existing tmux instance, the server options are guaranteed to have mouse support active.

### 3. Documentation Update

Update `hub/web/public/tmux-guide.zh-CN.md` to inform users that mouse support and wheel/trackpad scrolling are enabled out-of-the-box in Visual Tmux Client.

## Risks / Trade-offs

- **[Risk] Mouse dragging selects inside tmux instead of browser DOM:**
  → *Mitigation:* When mouse reporting is active, holding `Shift` while dragging directly selects text in the browser terminal as supported by xterm.js and the project's specification. Furthermore, tmux selection copies into the tmux paste buffer, and `Ctrl/Cmd+C` clipboard copying is preserved.
