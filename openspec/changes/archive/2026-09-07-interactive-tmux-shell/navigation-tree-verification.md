# Navigation tree verification (task 7.1)

Verifies the "Tree reflects current state" scenario in
`specs/tmux-shell-ui/spec.md`: the shell's navigation tree shows every
tracked session with its windows and panes, matching the engine's domain
model, for a real connected host.

## Method

Added `src/components/NavigationTree.vue` (presentational: renders
`domain.Session[]` as a session > window > pane tree, sorted by name/ID)
and wired it into `App.vue`, which owns a connect bar (tmux `-L` socket
name field, blank = default socket, per the standing safety rule below)
and calls `Sessions()` once after `Connect` resolves to populate the tree.

Built and ran the real product shell with `wails dev`. Per the standing
safety rule (the tooling driving this verification may itself be running
inside a default-socket tmux session that must not be disturbed), created
an isolated server on a dedicated socket rather than the default one:

```
tmux -L nav-tree-verify new-session -d -s main -x 100 -y 30
tmux -L nav-tree-verify new-window -t main -n editor
tmux -L nav-tree-verify new-window -t main -n logs
tmux -L nav-tree-verify split-window -t main:editor -h
tmux -L nav-tree-verify split-window -t main:logs -v
```

This produced session `main` with three windows: `zsh` (window 0, its
default name, 1 pane `%0`), `editor` (window 1, 2 panes `%1`/`%3`), `logs`
(window 2, 2 panes `%2`/`%4`, active). Confirmed via `tmux -L
nav-tree-verify list-windows`/`list-panes -a`.

Typed `nav-tree-verify` into the shell's socket field and clicked Connect
(driven via screen automation; confirmed with `echo $TMUX` before and
after that the agent's own default-socket session was never touched).

## Outcome

The rendered tree exactly matched the isolated server's real topology:
session `main` containing windows `editor` (panes `%1`, `%3`), `logs`
(panes `%2`, `%4`), and `zsh` (pane `%0`) — sorted alphabetically by
window name, which happens to interleave window-creation order here since
window 0's default name is `zsh`. Each pane row showed its ID and running
command (`zsh`), matching `list-panes`' output.

Disconnected via the shell's Disconnect button, then tore down the
isolated server (`tmux -L nav-tree-verify kill-server`) and confirmed via
`echo $TMUX` and `tmux -L nav-tree-verify list-sessions` that the agent's
own default-socket session was unaffected and the verification server is
fully gone.

Live-update behavior (creating/renaming/closing sessions or windows while
the shell is open) is task 7.2's scope, not covered here — this task only
verifies the tree's initial, one-shot rendering of existing state.

## Task 7.2: live updates

Wired `App.vue` to subscribe to `engine:lifecycle` (via `EventsOn`) right
after `Connect` resolves, and to re-call `Sessions()` (a cheap in-memory
model read, no tmux round-trip) whenever the event's `type` is one of the
topology-relevant `eventbus.LifecycleEventType` string values:
`session.discovered`, `session.closed`, `session.renamed`,
`window.layout-changed`, `window.renamed`, `pane.died`. The subscription
is torn down (via the unsubscribe function `EventsOn` returns) on
`Disconnect`.

### Bug found and fixed during verification

First verification pass (reusing the isolated `nav-tree-verify` server
from 7.1, connecting, then externally creating a second tmux session)
showed the tree rendering the *same* window/pane set under both sessions
after the second session appeared — clearly wrong; confirmed independently
that the engine's domain model itself was correct by writing a throwaway
Go program (`domain.NewModel` + `tmuxconn.Connect` against the same
socket, dumping `model.Sessions()` as JSON) that showed the two sessions
with their correct, distinct window sets.

Root cause: `NavigationTree.vue`'s outer `v-for` used `session.ID` as the
Vue `:key`. `domain.Session.ID` (tmux's `session_id`, e.g. `$0`) is never
populated by `tmuxconn` (`discoverSessions`/`addSession` construct
`&domain.Session{Key: key, Windows: ...}` without setting `ID` — a
pre-existing gap from `bootstrap-tmux-engine`, out of this change's
scope). Since `ID` is always `""`, every session shared the same Vue key,
so once a second session existed, Vue's keyed-list diffing reused the
first session's rendered subtree for the second one instead of creating
a new one. Fixed by keying on `session.Key.Name` instead, which is
guaranteed unique (it's the domain model's own map key for sessions).

### Method (post-fix) and outcome

Rebuilt (`npm run build`), restarted the app fresh, reconnected to the
isolated `nav-tree-verify` socket, then — without touching the shell's
Refresh button — ran, one at a time, against the isolated server:
`new-window`, `new-session`, `rename-session`, `rename-window`,
`kill-window`, `kill-session`. After every single one, the tree updated
to the correct new state on its own (screenshots taken after each step
confirmed exact correspondence with `tmux list-sessions`/`list-windows`
output at that point), with no stale, duplicated, or missing entries.

Disconnected, then tore down the isolated server and confirmed (as in
7.1) via `echo $TMUX` / `tmux -L nav-tree-verify list-sessions` that the
agent's own default-socket session was unaffected throughout.
