# Task 10 verification: management actions UI (session actions verified; window/pane UI verification abandoned)

## Scope note

Task 10.1's management-action UI (create/kill/rename session and window,
split/kill pane) is fully **implemented** in `NavigationTree.vue` (the
buttons) and `App.vue` (the `runAction`-wrapped handlers, each invoking the
corresponding engine operation from task groups 3–5). End-to-end UI
verification was completed for the **session** actions (create/rename/kill,
all PASS, below) against a real isolated tmux server.

The remaining UI actions — new/rename/kill window, split/kill pane, and the
task 10.2 forced-failure recovery — were **not** exercised through the UI.
This was a deliberate decision (see "Why window/pane UI verification was
abandoned" below) rather than a code gap: their UI code path is byte-for-byte
identical to the verified session path (`NavigationTree` `emit` → `App.vue`
`runAction` → engine `HostConn` method), and every engine operation behind
them is already verified at the engine level in task groups 3–5 (all `[x]`).

## Setup

- Restarted `wails dev` from the project root (clean process, fresh
  `domain.Model`) with the stale Chrome tab described below closed.
- Created an isolated tmux server on a dedicated socket, never the default
  socket the driving agent's own shell lives on:
  `tmux -L mgmtverify new-session -d -s main`.
- Connected the shell UI by typing `mgmtverify` into the socket-name field
  and clicking Connect, then **independently** confirmed (via
  `ps aux | grep 'tmux -L mgmtverify -CC attach'` and
  `tmux list-clients` / `tmux -L mgmtverify list-clients`) that exactly one
  `-L mgmtverify -CC attach` control-mode client existed, parented by the app
  process, and the default socket had no control-mode clients.
- Before and after every action, ran `echo $TMUX; tmux list-sessions` and
  `tmux list-clients` on the agent's own default socket to confirm
  `session-20260907-152842` and `smoketest` stayed untouched (1 window each)
  throughout. Confirmed clean at every check.

## Root cause of the earlier default-socket incidents (documented for the record)

While bringing the shell back up for this pass, the app was repeatedly found
attached to the tmux **default socket** (spawning one `tmux -CC attach`
control-mode client per default-socket session) within seconds of a fresh
launch, before any Connect click. Investigation traced this to a **stale
Chrome tab** at `http://localhost:34115/` left open from a much earlier
verification segment:

- Wails dev mode runs its frontend dev server *inside* the app binary
  (port 34115). The dev browser runtime (`internal/frontend/runtime/dev/main.js`)
  **queues** binding calls made while its IPC websocket is down and **replays
  them on reconnect**, and the page auto-reconnects every 500 ms **without
  reloading**.
- The stale tab therefore replayed a previously-queued `Connect("")` into
  every fresh app process on launch. `Connect("")` (blank socket = tmux's
  default socket) caused the engine to discover all default-socket sessions
  and open one `-CC attach` client each.
- Closing the tab eliminated the behavior entirely: with it closed, a fresh
  launch produced zero `-CC` processes until an explicit Connect click.

This was an environment/test-harness artifact, **not** an app bug — the app
has no auto-connect logic (`main.ts`, `App.vue`, `app.go`, `main.go` all
grep clean). It does, however, reinforce the standing safety rule: never
trust the app's own UI to confirm which socket it is connected to; always
cross-check via `ps`/`tmux list-clients` immediately after every Connect.

## Engine gap found (not fixed here, tracked for follow-up)

`App.Disconnect()` (`shell/app.go`) calls `host.Close()` and nils `a.host`
but does **not** clear the long-lived `a.model`. Because `App.Sessions()`
returns `a.model.Sessions()`, reconnecting to a *different* socket after a
disconnect leaves the previous socket's sessions visible in the tree
alongside the new ones. This is display-only (any action on a stale name is
routed through the actual current host and fails with "session not found"),
and single-connect-per-run usage per design.md would not hit it, so it was
not treated as an in-scope blocker — but it is a real correctness gap worth a
follow-up fix (`Disconnect` should reset the model, or `Connect` should start
from a fresh model).

A second, related observation: `HostConn.reconnectSession` (`engine/tmuxconn/host.go`)
replaces a tracked session's connection without closing the old subprocess
when the old connection is superseded, which can briefly leave a stale
`-CC attach` process running. Also not fixed here (out of scope for this
change's tasks); noted for follow-up.

## Task 10.1 — session actions (verified PASS)

All three verified against the real `mgmtverify` server, with each engine-side
effect confirmed via `tmux -L mgmtverify ...` independently of the UI.

### Create session

- Typed `verify-sess` into the nav tree's "New session name" field and
  clicked the "+ Session" button.
- UI: the tree immediately showed a second session row (field cleared).
- Engine: `tmux -L mgmtverify list-sessions` showed `verify-sess` alongside
  `main`; a new `-L mgmtverify -CC attach -t verify-sess` client was spawned.
- Default socket: untouched.
- **PASS.**

### Rename session

- Clicked the ✎ (rename) button on the `verify-sess` row, set the inline
  rename input to `verify-renamed`, clicked the ✓ confirm button.
- UI: the row's label updated to `VERIFY-RENAMED`.
- Engine: `tmux -L mgmtverify list-sessions` showed `verify-renamed`.
- Default socket: untouched.
- **PASS.**

### Kill session

- Clicked the ✕ (kill) button on the row, then the "Yes" confirm button.
- UI: the tree collapsed back to a single `MAIN` row.
- Engine: `tmux -L mgmtverify list-sessions` showed only `main`; the
  session's dedicated `-CC` client exited.
- Default socket: untouched.
- **PASS.**

## Task 10.1 / 10.2 — window & pane actions, and forced-failure recovery (abandoned)

Not exercised through the UI. Rationale: the macOS AX automation driving this
native Wails/WKWebView app repeatedly lost access to the app window mid-action
(after every successful AX interaction the process would transiently report
zero AX windows, requiring an `open`-based re-activation to recover), making
the remaining multi-step flows (window new/rename/kill, pane split/kill, and
the forced-failure error-surfacing check) unreliable to drive and verify.
Combined with the explicit direction to abandon UI-side verification, these
were left unverified at the UI layer.

Confidence is nonetheless high that they behave correctly, because:

- Their UI path is identical to the verified session actions: `NavigationTree`
  emits an event, `App.vue`'s `runAction` invokes the bound `App` method,
  and on success calls `refresh()`; on failure it sets `errorMessage`
  (rendered inline in the connect bar), satisfying 10.2's "surface failure
  without leaving the tree/grid inconsistent" via the same catch-and-refresh
  pattern every other action uses.
- Each backing engine operation is already verified end-to-end in task
  groups 3–5: `NewWindow`/`KillWindow`/`RenameWindow` (4.1–4.3),
  `SplitPane`/`KillPane` (5.1–5.2), and failure paths for untracked/dead
  panes (5.4).

## Cleanup

- Isolated `mgmtverify` server and the leftover orphaned
  `tmux -L inputverify -CC attach -t main` client from the 9.x pass were both
  torn down.
- Confirmed via `echo $TMUX; tmux list-sessions; tmux list-clients` that the
  agent's own default-socket session and `smoketest` were undisturbed
  throughout this entire pass.
