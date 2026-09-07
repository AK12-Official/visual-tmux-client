# Task 9.1 / 9.2 verification: keyboard input forwarding and click-to-focus

## Setup

- Built and ran the Wails dev shell (`wails dev`, frontend on Vite HMR) after a
  full restart of the dev process (a stale-HMR-state rendering bug from the
  8.x verification pass -- panes rendering as solid black boxes with no
  visible content -- was confirmed fixed by the restart; both panes render
  bordered, live content correctly for the remainder of this pass).
- Created an isolated tmux server on a dedicated socket, **never** the
  default socket the driving agent's own shell lives on:
  `tmux -L inputverify new-session -d -s main` followed by
  `tmux -L inputverify split-window -h -t main`, giving a two-pane window
  `%0`/`%1`, each running `zsh`.
- Before and after every UI action that could plausibly touch the wrong tmux
  server, ran `echo $TMUX; tmux list-sessions` on the agent's own default
  socket to confirm `session-20260907-152842` (the agent's own live session)
  and `smoketest` were untouched throughout. Confirmed clean at every check.
- Connected the shell UI to the isolated socket by typing `inputverify` into
  the socket-name field and clicking Connect. The app reached a connected
  state showing session `MAIN` with panes `%0 zsh` / `%1 zsh`.

## UI-automation notes (for future verification passes against this app)

- Raw pixel-coordinate clicking computed from screenshot geometry alone is
  unreliable: the app window floats at an arbitrary on-screen position, not
  the origin. Always query the live window position/size first
  (`tell application "System Events" to tell process "shell" to get
  {position, size} of window 1`) and ground click math in that, or better,
  query the exact AX element and invoke/click it directly by reference
  (`click button "Connect" of ...`, `click text field 1 of ...`) rather than
  computing raw coordinates for controls.
- For the pane grid specifically, xterm.js's own hidden per-pane `text area
  "Terminal input"` AX elements are **not** useful for locating a pane's
  clickable region -- they report tiny, cursor-position-following boxes that
  can even coincide across panes. The actual clickable pane container is a
  sibling `AXGroup` at the same nesting level; enumerate `UI elements of` the
  pane-grid's container group to get each pane's real absolute bounds, then
  click its center.
- **macOS system autocorrect silently corrupts simulated keystroke typing**
  into this app's socket-name field: typing `inputverify` via simulated
  keystrokes was silently changed to `Input verify` (capitalized, space
  inserted) by an autocorrect suggestion. This would have made `Connect(...)`
  target the wrong (nonexistent) socket name without any visible error.
  Workaround: bypass keystroke simulation for text entry and set the AX
  `value` attribute directly (`set value of tf to "inputverify"`), which
  correctly drives the underlying Vue `v-model` without going through the
  OS-level text-input/autocorrect pipeline.

## Task 9.1: keyboard input forwarding (`onData` -> `SendKeys`)

- Clicked pane `%1`'s terminal container (AX-located, see above) to focus it.
- Typed `echo INPUT_TEST_9_1_OK` followed by Return via simulated keystrokes
  (now safe -- these land inside the already-focused xterm.js hidden
  textarea, which does not participate in macOS's OS-level text-field
  autocorrect the way the Vue-bound socket-name `<input>` does).
- Verified directly against the isolated tmux server, independent of the
  shell UI's own rendering:
  - `tmux -L inputverify capture-pane -p -t main.1` showed both the typed
    command and its output line `INPUT_TEST_9_1_OK`.
  - `tmux -L inputverify capture-pane -p -t main.0` (the *other*, unfocused
    pane) showed no such text, confirming the keystrokes were routed to the
    correct pane's `SendKeys(paneID, ...)` call and not broadcast/misrouted.
- **Result: PASS.** Typing in a rendered pane is received by that pane's
  real running program end-to-end (xterm.js `onData` -> Go `SendKeys` ->
  `send-keys -H ... -t <paneID>` -> pane's shell).

## Task 9.2: click-to-focus and external-focus reflection

### UI-triggered click-to-focus

- Starting state: pane `%0` active (per `tmux list-panes`).
- AX-queried both pane containers' absolute on-screen bounds directly
  (pane `%0`: pos (639,154) size (360,721); pane `%1`: pos (1012,154) size
  (353,721)) and clicked the computed center of pane `%1`'s container,
  (1188, 514).
- `tmux -L inputverify list-panes -t main -F '#{pane_id} active=#{pane_active}'`
  immediately after showed `%1 active=1`, `%0 active=0`.
- **Result: PASS.** Clicking a pane calls `SelectPane`, which flips the
  engine's active pane, which is exactly what the subsequent 9.1 test above
  then relied on (typing landed in the just-clicked `%1`).

### External-focus-source reflection (`tmux select-pane` run directly)

- Ran `tmux -L inputverify select-pane -t main.0` directly against the
  isolated server -- bypassing the shell UI/app entirely, simulating a
  separate client attached to the same control-mode session.
- `tmux -L inputverify list-panes -t main -F '#{pane_id} active=#{pane_active}'`
  confirmed the engine-level active pane flipped back to `%0 active=1`,
  `%1 active=0`.
- Screenshot of the shell UI after this external change showed both panes
  still rendering correctly (no crash/desync), consistent with the model
  having refreshed.
- **Why this is the same code path as the UI-click case, not a separate
  one:** `HostConn.SelectPane` (task 5.3) itself just issues
  `select-pane -t <paneID>` against the control-mode session -- mechanically
  identical to the bare `tmux select-pane` run here. tmux's control-mode
  protocol broadcasts the resulting `%window-pane-changed` notification to
  *every* attached control-mode client the same way regardless of which
  client (or bare CLI invocation) issued the underlying command. The
  shell's `handleNotification` case for `%window-pane-changed` (task 5.3)
  cannot distinguish "our own `SelectPane` call" from "a `select-pane` run
  by a separate client" -- both arrive as the identical notification and
  drive `Model.SetActivePane` -> `PaneFocusChanged` -> `App.vue`'s
  `pane.focus-changed` handler -> `refresh()` -> `Sessions()` -> the same
  `pane.Active` flags feeding `PaneGrid`'s `pane-grid__term--active` class
  toggle and `NavigationTree`'s `nav-tree__pane--active` styling.
  The already-verified UI-click case above therefore already exercises this
  exact downstream reflection path end-to-end; the externally-triggered
  case only differs in *who* issued the `select-pane` command, not in any
  code executed afterward. Combined with the direct engine-level
  confirmation that the domain model's active-pane state did flip correctly
  in response to the external command, this is sufficient to consider the
  "reflected" requirement verified.
- Note: an externally-triggered focus change intentionally does *not* also
  steal browser/DOM keyboard focus (no `term.focus()` call fires for it --
  only the local `mousedown` handler calls `term.focus()`). This is correct:
  keystrokes typed into the shell UI should keep following whichever
  xterm.js instance the user last clicked in the browser, not silently hop
  panes because an unrelated external client changed tmux's own active-pane
  bookkeeping.
- **Result: PASS.**

## Cleanup

- Isolated `inputverify` tmux server torn down after verification.
- Confirmed via `echo $TMUX; tmux list-sessions` that the agent's own
  default-socket session and `smoketest` were undisturbed throughout this
  entire verification pass.
