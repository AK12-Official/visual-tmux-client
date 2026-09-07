## Why

The engine currently only mirrors tmux state (read-only) and the Wails shell is a throwaway throughput spike, not a usable client. To become an actual visual tmux client — the stated goal of this project — a user needs to see their panes laid out the way tmux actually renders them, type into them, and manage sessions/windows/panes, all from a desktop UI. None of that exists yet.

## What Changes

- Extend the engine's local tmux connection with write operations: create/kill/rename session, create/kill/rename window, split/kill/select pane.
- Add key/input forwarding: raw input bytes from a pane's terminal UI are forwarded to that pane via `send-keys -H` (hex byte forwarding), so arrow keys, control sequences, and pasted text all pass through unmodified without any client-side key-name translation.
- Add layout geometry parsing: the control-mode layout string is parsed into a full node tree (pane ID, position, size, split orientation), not just the flat set of pane IDs the engine currently extracts for death-detection diffing.
- Replace the throwaway Wails throughput-spike project with a real product shell: a session/window/pane tree for navigation, a pane grid laid out using the new layout geometry (one xterm.js instance per pane, wired to the existing per-pane output event stream for rendering and to the new input-forwarding path for keystrokes), and UI entry points (buttons/context menu) for the new management operations.
- Out of scope for this change (explicitly deferred): drag-to-resize panes, custom tmux prefix-key rebinding, persisted UI settings/themes, and all multi-host/SSH work.

## Capabilities

### New Capabilities
- `tmux-shell-ui`: the Wails desktop shell capability — renders the engine's session/window/pane tree, lays out one xterm.js instance per pane per the mirrored layout geometry, streams pane output into it, forwards user keystrokes back to the corresponding pane, and exposes session/window/pane management operations (create, kill, rename, split, select) through UI actions.

### Modified Capabilities
- `tmux-engine`: adding write/management operations (session/window/pane create, kill, rename, split, select), input forwarding (`SendKeys`, raw bytes to a pane), and full layout geometry extraction (position/size/orientation per pane, not just pane-ID presence) to the previously read-only engine.

## Impact

- `engine/tmuxconn`: `HostConn` gains public write methods (`CreateSession`, `KillSession`, `RenameSession`, `NewWindow`, `KillWindow`, `RenameWindow`, `SplitPane`, `KillPane`, `SelectPane`, `SendKeys`) built on the existing `issueAdminCommand` plumbing.
- `engine/tmuxcm`: new `ParseLayout` function returning a full geometry tree; existing `ParseLayoutPaneIDs` becomes a thin wrapper over it to avoid duplicating the layout grammar parser.
- `domain` model: `Window`/`Pane` gain position fields populated from the new geometry parse, so consumers (the UI) don't need to re-derive geometry from the raw layout string.
- New Wails product shell (Go host + JS/TS frontend with xterm.js), replacing the disposable spike-2 project referenced in `bootstrap-tmux-engine`'s tasks.md — this is the first real UI code in the project.
- No SSH, multi-host, drag-resize, or persisted settings in this change.
