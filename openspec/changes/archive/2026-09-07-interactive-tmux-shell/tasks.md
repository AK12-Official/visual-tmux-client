## 1. Engine: layout geometry parsing

- [x] 1.1 Add `tmuxcm.LayoutNode` (X, Y, Width, Height, PaneID for leaves, Orientation + Children for splits) and implement `ParseLayout(layout string) *LayoutNode` covering the grammar already handled by `parseLayoutNode` in layout.go, verified with unit tests against layout strings recorded in the archived `bootstrap-tmux-engine` change (splits, nested splits, single-pane windows)
- [x] 1.2 Reimplement `ParseLayoutPaneIDs` as a post-order walk over `ParseLayout`'s result and verify existing `layout_test.go` cases still pass unchanged
- [x] 1.3 Add unit tests asserting `ParseLayout`'s geometry (X/Y/Width/Height per pane) matches expected values for a multi-way split layout string, including a nested horizontal-inside-vertical split

## 2. Engine: domain model geometry and new lifecycle events

- [x] 2.1 Add `X, Y int` fields to `domain.Pane` (Width/Height already exist) and verify with a unit test that `UpsertPane` stores and `Pane`/`Window` snapshot accessors return them
- [x] 2.2 Add `SessionRenamed` and `PaneFocusChanged` to `eventbus.LifecycleEventType` and verify with a unit test that both can be published/subscribed like existing lifecycle event types
- [x] 2.3 Wire `discoverWindowsAndPanes`/`refreshWindowPanes` in tmuxconn/host.go to populate `Pane.X/Y` from `tmuxcm.ParseLayout` (using the window's current layout string) at the same points Width/Height are already populated, verified with a unit test using a fake/recorded control-mode session with a split window

## 3. Engine: session management operations

- [x] 3.1 Implement `HostConn.CreateSession(name string) error` (`new-session -d -s <name>`) and verify end-to-end against a real local tmux server that the new session appears in the domain model via existing discovery
- [x] 3.2 Implement `HostConn.KillSession(name string) error` (`kill-session -t <name>`) and verify end-to-end that the session is removed from the domain model via existing discovery
- [x] 3.3 Implement `HostConn.RenameSession(name, newName string) error` (`rename-session -t <name> <newName>`); add a `%session-renamed` case to `handleNotification` that updates the session's key in the domain model (add a `Model.RenameSession` or equivalent key-remapping method) and publishes `SessionRenamed`; verify end-to-end both for an engine-initiated rename and for a rename triggered by a separate `tmux rename-session` call on the same server

## 4. Engine: window management operations

- [x] 4.1 Implement `HostConn.NewWindow(sessionName string) error` (`new-window -t <sessionName>`) and verify end-to-end that the new window and its initial pane appear in the domain model
- [x] 4.2 Implement `HostConn.KillWindow(windowID string) error` (`kill-window -t <windowID>`) and verify end-to-end that the window and its panes are removed from the domain model
- [x] 4.3 Implement `HostConn.RenameWindow(windowID, newName string) error` (`rename-window -t <windowID> <newName>`) and verify end-to-end that the domain model reflects the new name (reuses the existing `%window-renamed` path)

## 5. Engine: pane management operations and input forwarding

- [x] 5.1 Implement `HostConn.SplitPane(paneID string, vertical bool) error` (`split-window -t <paneID>` with `-h`/`-v`) and verify end-to-end that both resulting panes appear in the domain model with updated layout geometry (reuses the existing `%layout-change` path from task 2.3)
- [x] 5.2 Implement `HostConn.KillPane(paneID string) error` (`kill-pane -t <paneID>`) and verify end-to-end that a `pane.died` event is emitted and the pane is removed from the domain model
- [x] 5.3 Add a `%window-pane-changed` case to `handleNotification` that updates the active pane in the domain model (add a `Model.SetActivePane` or equivalent method clearing the previous active pane in that window) and publishes `PaneFocusChanged`; implement `HostConn.SelectPane(paneID string) error` (`select-pane -t <paneID>`); verify end-to-end that calling `SelectPane` updates the domain model's active pane and emits the event
- [x] 5.4 Implement `HostConn.SendKeys(paneID string, data []byte) error`, formatting `data` as `send-keys -H <hex-byte-pairs> -t <paneID>`, returning an error for an untracked or dead pane without issuing the command; verify with a unit test covering hex encoding of representative byte sequences (printable ASCII, a control byte, a multi-byte UTF-8 sequence) and end-to-end against a real pane that sent bytes are received by the pane's running program
- [x] 5.5 Manually verify (per design.md's input-forwarding decision) that arrow keys, Ctrl-C, and a multi-line paste sent through `SendKeys` behave identically in a real pane to typing/pasting at a directly attached terminal; record the verification steps and outcome in a short note in this change directory

## 6. Shell: Wails project scaffold

- [x] 6.1 Scaffold a new Wails (Go) project as the product shell (distinct from the deleted spike-2 project), with a Go-side binding layer exposing the engine's `HostConn` operations (connect, session/window/pane management, `SendKeys`) and event bus subscriptions to the JS frontend
- [x] 6.2 Embed xterm.js in the frontend build and verify a single hardcoded pane's output (from a manually connected `HostConn`) renders correctly, confirming the Go<->JS event bridge wiring works end-to-end before building the full UI

## 7. Shell: navigation tree

- [x] 7.1 Implement the session/window/pane navigation tree component, populated from the engine's domain model via the Go binding, and verify it displays an existing local tmux server's sessions/windows/panes correctly
- [x] 7.2 Wire the tree to lifecycle events (`session.discovered`, `session.closed`, `SessionRenamed`, `window.layout-changed`, `WindowRenamed`, `pane.died`) so it updates live, and verify by creating/renaming/closing a session or window on the underlying tmux server while the shell is open and observing the tree update without manual refresh

## 8. Shell: geometry-accurate pane grid

- [x] 8.1 Implement the pane grid component that lays out one xterm.js instance per pane of the selected window, positioned/sized from the domain model's per-pane geometry (task 2.1/2.3), and verify against a real multi-pane split window that the rendered grid visually matches tmux's own layout
- [x] 8.2 Wire each xterm.js instance to its pane's `pane.output` event subscription plus an initial scrollback seed from `PaneScrollback`, and verify output appears live and existing scrollback is shown on first display of a pane
- [x] 8.3 Wire pane-grid re-layout to `window.layout-changed` events so a split/resize updates the grid without a full window reload, and verify by splitting a pane on the underlying tmux server and observing the grid update

## 9. Shell: keyboard input and focus

- [x] 9.1 Wire each xterm.js instance's `onData` callback to call `SendKeys` for its pane via the Go binding, and verify typing in a rendered pane is received by the pane's running program
- [x] 9.2 Implement click-to-focus: clicking a pane's terminal view calls `SelectPane` and updates the shell's focus indicator; verify clicking between panes changes which one receives subsequent keyboard input, and that focus set via `PaneFocusChanged` from an external source (e.g. `tmux select-pane` run directly) is also reflected

## 10. Shell: management actions UI

- [x] 10.1 Add UI actions (buttons/context menu) for create/kill/rename session and window, and split/kill pane, each invoking the corresponding engine operation from task groups 3-5, and verify each action against a real local tmux server — UI actions fully implemented; create/rename/kill **session** verified end-to-end against a real isolated tmux server (PASS). Window and pane UI actions not exercised through the UI (abandoned per direction); their UI code path is identical to the verified session path and their backing engine operations are already verified in task groups 4–5. See `management-actions-verification.md`.
- [x] 10.2 Surface action failures (e.g. a kill on an already-dead pane) to the user via an inline error indicator, without leaving the tree or pane grid in an inconsistent state, and verify by forcing a failure (e.g. renaming a session to a name that already exists) and observing the shell recovers cleanly — failure-surfacing implemented (`runAction` catch → `errorMessage`, rendered inline in the connect bar, with `refresh()` on success). Forced-failure UI verification abandoned per direction. See `management-actions-verification.md`.

## 11. Validation

- [x] 11.1 Run `openspec validate --change interactive-tmux-shell --strict` and confirm it passes — passed (`Change 'interactive-tmux-shell' is valid`).
- [x] 11.2 Confirm every scenario in both `specs/tmux-engine/spec.md` and `specs/tmux-shell-ui/spec.md` deltas has a corresponding verification step completed above — all `tmux-engine` scenarios map to completed tasks 1–5; all `tmux-shell-ui` scenarios map to completed tasks 7–9 except the three management-action scenarios (create-session-from-UI, kill-pane-from-UI, management-action-fails). Of those, create-session-from-UI is verified (10.1); kill-pane-from-UI and management-action-fails are covered at the engine level (5.2, 5.4) and by code inspection but not UI-click-verified (abandoned). See `management-actions-verification.md`.
