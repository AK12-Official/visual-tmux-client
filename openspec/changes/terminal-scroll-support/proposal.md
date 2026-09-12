## Why

When a tmux session produces output exceeding the visible terminal viewport (such as command output, log dumps, or compiler messages), users are currently unable to scroll up and down to inspect earlier context using mouse wheel or trackpad scroll gestures. This happens because tmux defaults to `mouse off`, disabling terminal mouse reporting and running in the alternate screen buffer without passing scroll events, leaving the browser terminal unable to navigate pane history. Enabling mouse support by default ensures users can naturally scroll through history and context out of the box.

## What Changes

- Enable tmux global mouse support (`set-option -g mouse on`) on the tmux server so tmux enables mouse reporting in the client terminal (xterm.js), enters copy-mode automatically on wheel up to navigate pane history, and exits copy-mode when scrolled back to the bottom.
- Ensure global options (`window-size latest` and `mouse on`) are reliably configured without relying on a one-off `sync.Once` that fails to reapply if the tmux server exits and restarts, and without re-running `set-option` when already active to avoid client repaints and false activity triggers.
- Ensure global options are applied when creating sessions or attaching to existing sessions.
- Update in-app documentation / guide to reflect that mouse support and scrolling are enabled by default.

## Capabilities

### New Capabilities

*(None)*

### Modified Capabilities

- `session-hub`: The hub SHALL configure the tmux server with mouse support enabled (`mouse on`), ensuring terminal clients can use mouse wheel and trackpad scroll gestures to navigate session scrollback history.
- `web-session-manager`: The terminal view SHALL support scrolling up and down through session history and context via mouse wheel and trackpad gestures without breaking text copy or terminal keyboard interaction.

## Impact

- `hub/server.go`, `hub/attach.go`, `hub/api.go`: Ensure tmux global options include `mouse on` and are kept in sync safely.
- `hub/web/public/tmux-guide.zh-CN.md`: Update documentation regarding default mouse support.
- Existing tests and new tests: Add unit tests verifying `mouse on` option configuration and scrolling behavior.
