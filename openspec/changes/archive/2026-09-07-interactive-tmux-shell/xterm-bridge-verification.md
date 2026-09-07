# xterm.js / Go<->JS event bridge verification (task 6.2)

Verifies that the Wails binding layer added in task 6.1 correctly bridges
the engine's event bus to the JS frontend, and that xterm.js can render a
pane's live output, before building the real navigation tree (task group 7)
and pane grid (task group 8) on top of the same wiring.

## Method

Installed `@xterm/xterm` in `shell/frontend`. Added a temporary Vue
component that, on mount:

1. Registered an `EventsOn('engine:pane-output', ...)` listener first, then
2. called `Connect` on a **dedicated, isolated tmux socket**
   (`tmux-shell-smoketest`, via `tmux -L tmux-shell-smoketest ...` on the
   command line) — deliberately never the default socket, since the
   tooling driving this verification is itself commonly run from inside a
   real tmux session on the default socket, and connecting there would let
   the engine discover (and, once task 10 exists, potentially act on) that
   session,
3. called `Sessions`/`PaneScrollback` to pick the first real pane found and
   seed the terminal, then rendered further matching `engine:pane-output`
   events live via `term.write`.

Ran the app with `wails dev`, watched it via screenshot, and drove the
isolated session's pane directly from a shell (`tmux -L
tmux-shell-smoketest send-keys ...`) to generate genuinely new output while
the app was already connected.

## Findings

- `PaneScrollback` returns `nil`/empty for a pane that has been open since
  before the engine connected to it and has produced no output since —
  it only reflects streamed `%output` (plus, after a reconnect,
  `recoverPaneScrollback`'s `capture-pane` recovery). This is existing,
  documented behavior (see `HostConn.PaneScrollback`'s doc comment), not
  something task 6.2 changes — but it means task 8.2's "existing scrollback
  is shown on first display of a pane" will need `discoverWindowsAndPanes`
  (or equivalent) to seed the ring buffer via `capture-pane` at initial
  discovery too, not just on reconnect. Flagging this now for task 8.2;
  no engine code changed here.
- Fixed a real race in the smoke-test component itself (not engine code):
  tmux can emit output for a pane before the frontend's async
  `Connect`/`Sessions` chain has determined which pane ID to watch, so the
  first `engine:pane-output` events arriving before that point must be
  buffered and replayed once the watched pane is known, rather than
  dropped by an early identity check. This is a frontend-side ordering
  concern that task 7/8's real implementation will need to account for too
  (e.g. subscribe before resolving which panes exist, buffer/replay, or
  subscribe to all pane IDs and filter downstream).

## Outcome

After sending a new command directly into the isolated session's pane
while the app was running, the output appeared in the embedded xterm.js
view immediately, confirming the full path works: tmux -> control-mode
`%output` -> `HostConn` -> `eventbus.Bus` -> `runtime.EventsEmit` ->
`EventsOn` in the frontend -> `Terminal.write`.

The temporary verification component was removed after confirming this
(see this file for the record); `@xterm/xterm` remains installed in
`shell/frontend/package.json` for task group 8 to build on.
