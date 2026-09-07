# Spike Notes: bootstrap-tmux-engine

## Spike 1: tmux control-mode protocol

**Setup**: local tmux server on a dedicated socket (`-L spike`), sessions `alpha` (windows `main` with 2 panes, `second` with 1 pane) and `beta` (window `main`, 1 pane). Attached via `tmux -CC attach -t alpha` under a real pty (required — see note below). Raw control-mode stream captured to file.

### 1.2 — `%output` scoping: per-session, not per-server

Confirmed empirically: while attached to `alpha` via `-CC`, generating output in `alpha:main` (both panes, including the pane not currently active) and in `alpha:second` (a different, non-active window in the same session) all produced `%output` events. Generating output in `beta:main` — a **different session**, not attached — produced **no** `%output` event for its pane (`%3`), even though `beta` existed on the same server the whole time.

**Conclusion**: a single control-mode client attached to a session receives output for *all* panes across *all* windows of that session (not just the active window/pane), but **not** for other sessions on the same server. This directly resolves the open question in design.md's "Decisions" section:

> **The engine needs one control-mode client per session, not one per server.**

This changes the engine's connection granularity: for local (and later remote) hosts with multiple sessions, the engine must spawn one `tmux -CC attach -t <session>` subprocess per tracked session, not one per host. `host.connected` in the event model should be understood as "the host's SSH/local transport is up"; each session tracked on that host has its own underlying control-mode subprocess and connection lifecycle. This should be reflected in design.md's connection-handling section before task 4.2's implementation.

Secondary finding: `%sessions-changed` (a session was created or destroyed *anywhere on the server*) and `%unlinked-window-add`/`%unlinked-window-close` (windows not linked to the attached session) **do** fire globally, even though `%output` is session-scoped. So structural awareness of the whole server leaks through even from a single session-scoped attach — a per-server "session discovery" client (see below) could rely on these rather than requiring a full attach per session before a session is even known.

**Practical implication for engine design**: a lightweight approach is one always-on per-host control-mode client (attached to any session, or a dedicated bootstrap session) purely to observe `%sessions-changed`/`%unlinked-window-*` for discovery, and one additional per-session control-mode client for each session the engine actively tracks (to get its `%output`/`%layout-change`/etc). This avoids polling `list-sessions` for discovery while keeping output streams properly scoped.

### 1.3 — `-C` vs `-CC`

Both accept the same command/notification protocol on the wire. The observed differences:

1. **Echo**: `-C` does not suppress local terminal echo of commands sent on stdin — when a command like `list-sessions\n` is written to the client's stdin, it appears verbatim in the output stream (this is pty-level echo, not a protocol difference, but `-CC` disables it by putting the pty in the right mode / wrapping output).
2. **Framing**: `-CC` wraps the entire protocol stream in a DCS (Device Control String) escape sequence: `\x1bP1000p ... ` at the start. `-C` does not — it's a bare protocol stream. This DCS wrapper is what lets a terminal emulator (like iTerm2) distinguish "this is a nested control-mode session" from literal terminal output if it were somehow displayed directly; it's designed for exactly the embedding use case this project has.

**Conclusion**: **use `-CC`**, as design.md already assumed. It is the "extended" mode meant for programmatic/embedded consumption (no echo noise, explicit framing), while plain `-C` behaves more like a raw interactive control-mode terminal. No change to design.md needed here — this confirms the existing assumption rather than overturning it.

### 1.4 — reconnect recovery via `capture-pane -p -S -`

Confirmed: `capture-pane -p -S - -t <pane-id>` returns full scrollback + current screen for a pane, and this works as a plain command issued over a **fresh** control-mode attach (simulating a reconnect after the prior control-mode client was killed) — pane content lives on the tmux server itself, not in the control-mode client's connection state. Killing every control-mode client attached to a session does not affect the session, its panes, or their scrollback in any way; a new `-CC attach` to the same session immediately has full access to history via `capture-pane`.

- `capture-pane -p` alone returns only the currently visible screen (padded to pane height with blank lines).
- `capture-pane -p -S -` returns the entire available scrollback (bounded by the pane's `history-limit`, default 10000 lines) followed by the current screen.

**Conclusion**: task 4.7's reconnect design is validated as-is — on reconnect, issue `capture-pane -p -S -` per tracked pane to seed the ring buffer / xterm.js state, no special-case handling needed for "was the connection actually severed vs just idle." No revision to design.md needed here.

### Operational note (not a design finding, but necessary for implementation)

`tmux -CC attach` requires a real pty on its stdin/stdout to work at all — piping through a plain OS pipe fails with `tcgetattr failed: Operation not supported`. The engine's subprocess handling (both for local sockets and later for `ssh <host> tmux -CC attach`) must allocate a pty for the subprocess, not a plain pipe. This is a real implementation constraint for task 4.2 (Go's `os/exec` needs a pty package, e.g. `github.com/creack/pty`, or equivalent).

### Addendum (task 4.1): control-mode has no direct "pane died" notification

Recorded while implementing and testing the control-mode parser against a real server (splitting a pane, then running `exit` inside it): tmux's control-mode protocol has **no notification that directly announces a pane's death** (tmux(1)'s hooks list has `pane-died`/`pane-exited` *hooks*, but those are server-side hook scripts, not control-mode `%`-notifications — they are not sent over the control-mode wire). What is observed instead, for a pane closing:

```
%output %3 exit\033[?2004l\015\015\012\r\n
%layout-change @0 b25d,80x24,0,0,0 b25d,80x24,0,0,0 -\r\n   <- pane %3 no longer appears in the new layout
%window-pane-changed @0 %0\r\n
```

**Implication for task 4.2/4.6**: the engine cannot map `pane.died` to a single notification type. It must diff the set of pane IDs implied by a window's layout string before and after a `%layout-change` (or after a `%window-close`/`%unlinked-window-close` for a whole window closing) and emit its own `pane.died` for any pane ID present before but absent after. This is an implementation strategy, not a spec or design change — `specs/tmux-engine/spec.md`'s "Pane process exits" scenario describes the engine's own emitted event, which this diffing approach satisfies; it does not require a tmux-level 1:1 notification to exist.

### Addendum (task 4.1): exact `%output` escaping rules (confirmed against live server)

- Any byte tmux considers non-printable, and the literal backslash byte itself, is escaped as a backslash followed by exactly 3 octal digits (e.g. `\010` for backspace, `\134` for a literal `\`).
- Bytes that are part of a valid printable UTF-8 sequence (e.g. `é` = `0xc3 0xa9`) are passed through **unescaped**, raw, in the multi-byte form — confirmed with `printf '...é\n'` producing raw `\xc3\xa9` bytes in the `%output` value, not an octal escape.
- The parser (`engine/tmuxcm`) decodes only backslash+3-octal-digits sequences and passes everything else through byte-for-byte; this matches the above and is covered by `parser_test.go`.

## Spike 2: Wails event throughput

**Setup**: throwaway Wails v2 app at `/tmp/wails-spike/throughput-spike/` (not part of the repo — see task 2.5). Go side (`App.StartSpike(eventsPerSecond, payloadBytes)`) emits synthetic `pane.output` events via `runtime.EventsEmit` on a `time.Ticker`, tracking emitted count/bytes with atomics. Frontend subscribes via `EventsOn('pane.output', ...)`, writes each payload into an xterm.js terminal, and tracks received count/bytes. A stats line polls both sides every 500ms and reports `received`, `emitted (go)`, and `backlog (go - received)`.

**Runs**:
- 500 events/sec × 80 bytes, sustained ~30s: `received: 14740 / emitted: 14740`, backlog constantly 0, measured rate ~499.0 evt/s (matches requested rate). No visible lag in xterm rendering.
- 20000 events/sec × 80 bytes (40x the first run, well above any real tmux pane's realistic output rate — even `yes` piped through a terminal doesn't sustain this), sustained ~52s: `received: 1,042,788 / emitted: 1,042,788`, backlog constantly 0 throughout, measured rate ~19922-19927 evt/s (tracks requested rate within noise). xterm.js kept scrolling smoothly with no dropped frames or input lag observed in the app window.

**Conclusion**: the Go→JS event bridge (`runtime.EventsEmit` / `EventsOn`) and xterm.js rendering have no observed throughput ceiling anywhere near realistic tmux output rates. Backlog stayed at exactly 0 in both the realistic-load run and the 40x-stress run — no coalescing, batching, or backpressure logic is needed for the `pane.output` path in this change's scope. This confirms design.md's assumption that per-event `EventsEmit` calls (one per `%output` notification, not manually batched) are viable as designed; no revision to design.md needed for this decision.

**Caveat**: this spike used one emitter and one terminal. It does not test the effect of *many concurrent* high-rate panes (e.g. 10+ tracked sessions each emitting near their own ceiling simultaneously) on a single Wails event bus — that is out of scope for this change (single local host, exploratory scaffold) but worth revisiting if a later change tracks many simultaneous busy panes.

### Addendum (task 4.2): an unsolicited `%begin`/`%end` block arrives immediately on `-CC attach`

Confirmed via a raw pty capture: as soon as a `-CC attach` client connects, tmux sends one `%begin <ts> <cmdnum> <flags>` / `%end <ts> <cmdnum> <flags>` block *before the client has written any command*, using the server's running global command-number counter (not starting at 0/1 for the new client). A naive command/response pairing that assumes the first `%begin`/`%end` block answers the first command sent will misroute this block as that command's result.

**Implication for task 4.2**: `tmuxconn.sessionConn` tracks whether a command is actually outstanding (an `awaiting` flag, true only for the duration of an `issueCommand` call) and only forwards a `%begin`/`%end` block to the waiting caller when `awaiting` is true; an unsolicited block arriving with nothing awaiting it is dropped. This is an implementation detail inside `tmuxconn`, not a spec or design change.

### Addendum (task 4.2): a control-mode connection is not guaranteed to survive the destruction of its own attached session

Confirmed via a live `kill-session` against the session a `-CC attach` client is currently attached to (while other sessions remain on the server): tmux detaches that client and its subprocess exits, even though the server and its other sessions keep running. Any given `sessionConn` — including whichever one is currently the engine's bootstrap/admin connection — can die independently at any time while other sessions' connections stay alive.

A related race: because `%sessions-changed` fires on every live connection (see 1.3 above), the notification for a session's destruction typically arrives redundantly on more than one connection, including the very connection whose own session is being destroyed. If discovery/admin commands (`list-sessions`, `list-windows`, `list-panes`) are always routed through one fixed connection, a command issued on a connection that is mid-detach can hang until it hits its own timeout, well past when other, unaffected connections could have answered immediately.

**Implication for task 4.2**: the engine has no single fixed "admin connection." Discovery/admin commands are dispatched via `HostConn.issueAdminCommand`, which tries every currently-tracked live connection in turn (ctrl first, then each session's own connection) rather than trusting one connection to remain usable, and `sessionConn.issueCommand` returns immediately (rather than waiting out its full timeout) if the connection's read loop exits while a command is outstanding. This is an implementation detail inside `tmuxconn`, not a spec or design change — `specs/tmux-engine/spec.md`'s session-closed scenario is unaffected; this only concerns how the engine internally keeps discovery working while it happens.

## Summary of design.md revisions needed

1. **Connection granularity**: design.md's "Local tmux connection via control-mode subprocess" decision should be amended to specify **one control-mode subprocess per tracked session** (plus optionally one lightweight per-host discovery client), not one per host. This affects task 4.2's implementation and should be called out explicitly before that task starts.
2. No other design.md decisions were overturned; `-CC` and the `capture-pane -p -S -` reconnect strategy are both confirmed as designed.

### Addendum (task 4.7): concurrency bugs surfaced by running the end-to-end suite under `go test -race`

Running the real-tmux-server integration tests (tasks 4.3-4.7) under `-race` for the first time — while implementing and verifying reconnect recovery — surfaced three genuine data races, none of them specific to reconnect logic itself:

1. **`HostConn.Close` didn't wait for its own spawned goroutines.** Every background goroutine `HostConn` spawns (read loops, `monitorConn`, notification follow-ups) touches domain-model/event-bus state after `Close` returns unless something waits for it. Fixed by tracking every such spawn through a `sync.WaitGroup` (`HostConn.spawn`), which `Close` now waits on after closing all known connections.

2. **`eventbus.Bus`'s `UnsubscribeOutput`/`UnsubscribeLifecycle` closed the subscriber's channel under the bus's lock, while `Publish*` snapshots the subscriber slice under that same lock but sends to each channel *after* releasing it** — so a send could still be in flight against a channel `Unsubscribe` was concurrently closing. Fixed by never closing the channel: once a subscriber is removed from the slice, nothing sends to it again, and it is simply garbage-collected once the caller drops its receive end.

3. **Concurrent `syncSessions` calls could each spawn a duplicate connection for the same new session.** `%sessions-changed` (and the unlinked-window notifications) fire redundantly across every live control-mode connection (see the addendum above), and each arrival independently spawns its own `syncSessions(true)` goroutine. Because diffing against `HostConn.sessions` and actually registering a new session (which does real wall-clock work — spawning a subprocess, attaching, running discovery commands) are far apart in time, two concurrent `syncSessions` calls could each observe the same not-yet-tracked session name and each spawn their own dedicated `tmux -CC attach` subprocess for it; only the last one's registration survives, silently orphaning the other(s) — a leaked process and leaked goroutines that `Close` then hangs waiting for. Fixed with a dedicated `HostConn.syncMu` serializing the whole of `syncSessions`.

4. **`domain.Model`'s accessors handed out live pointers into structures it kept mutating.** `Model.Window`/`Session` returned the actual stored `*Window`/`*Session`, whose `Panes`/`Windows` maps `UpsertPane`/`UpsertWindow` mutate in place under the model's lock — but any caller (production code in `handleNotification`'s window-renamed/layout-change handling, and every polling assertion in `host_test.go`) that ranged over or read fields of a previously-returned pointer did so without holding that lock, racing with the in-place mutation. Fixed two ways: (a) `Model.Window`/`Session`/`Sessions` now return snapshots — the struct plus a freshly copied `Panes`/`Windows` map — so a caller's copy is isolated from further live mutation; (b) the two remaining in-place field mutations (`%layout-change`'s window layout, `%window-renamed`'s window name) were replaced with new `Model.SetWindowLayout`/`SetWindowName` methods that mutate under the model's own lock by swapping in a fresh `*Window`, rather than being mutated directly by `tmuxconn` after the fact. `*Pane` values themselves were left as live pointers rather than copied, since they are always replaced wholesale by `UpsertPane` (never mutated in place) once created.

None of these required a design.md or spec change — all four are internal correctness fixes to already-decided designs (goroutine lifecycle management, event bus semantics, session discovery, and the domain model's stated "thread-safe registry" contract), not new behavior. They are recorded here because they were only found by testing against a real server with `-race`, consistent with this project's spike-driven approach to concurrency-sensitive behavior.

