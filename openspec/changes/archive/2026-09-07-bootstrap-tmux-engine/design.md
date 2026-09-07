## Context

See proposal.md for motivation. This change scaffolds only the engine's local-tmux path plus two spikes; SSH, multi-host, and write operations (create/kill session, split pane, etc.) are deliberately out of scope and will build on this foundation in later changes.

Constraints from exploration:
- No HTTP server / browser-accessed port — the shell is an embedded webview (Wails), not a served web app.
- The engine must be a pure Go library with no dependency on Wails or any other UI framework, so it can be tested and driven headlessly.
- Remote connectivity (deferred, but shapes the connection abstraction now) will use system `ssh` subprocesses rather than a native SSH library, to inherit `~/.ssh/config` semantics (agent, ProxyJump, hardware keys) for free.

## Goals / Non-Goals

**Goals:**
- Prove out tmux control-mode as the mirroring mechanism (this is the whole architecture's foundation).
- Prove out Wails' Go<->JS event bridge can carry high-frequency pane output without frontend stalls.
- Land a real (not throwaway) engine skeleton: domain model, event bus, control-mode parser, connected to a local tmux server end-to-end.
- Establish the event bus contract (event names, payload shapes) that later changes (SSH, multi-host, write operations) will extend rather than redesign.

**Non-Goals:**
- SSH/remote hosts. Local Unix-socket connection only in this change.
- Any write/management operation (create session, split pane, send keystrokes). This change is read/observe only.
- Multi-host aggregation, host registry, or any persistence of host config.
- Final shell UI/UX. The Wails spike is a throughput harness, not product UI.

## Decisions

### Decision: Two upfront spikes gate implementation
**What**: Before writing the production engine, run (1) a tmux control-mode protocol spike and (2) a Wails event-throughput spike, both throwaway harnesses.
**Why**: Both are load-bearing assumptions for the entire architecture (engine/shell split, connection granularity, event delivery strategy). They are cheap to falsify now and expensive to discover wrong after the domain model and event bus are built around a false assumption.
**Alternatives considered**: Skipping spikes and writing the engine directly against best-effort reading of tmux docs — rejected because control-mode's exact scoping behavior (`%output` per-session vs per-server; `-C` vs `-CC`) is not fully unambiguous from documentation alone, and because Wails' cross-language event bridge throughput under load is not something to discover after the domain model is built around it.

**Spike 1 — control-mode protocol**: start a local tmux server with multiple sessions, attach via `tmux -CC`, and confirm from observed behavior: whether `%output` is scoped to the attached session or the whole server (determines whether the engine needs one control-mode client per session or can use one per server); the practical difference between `-C` and `-CC`; whether `capture-pane -p -S -` can fully reconstruct a pane's screen and scrollback after a dropped and re-established connection.

**Spike 2 — Wails event throughput**: a minimal Wails project where the Go side emits simulated `pane.output` events at a high rate (representative of a busy pane, e.g. `yes` or `htop` output) and the JS side renders them via xterm.js; confirm no dropped frames, unbounded queue growth, or UI stalls. If throughput is insufficient, the mitigation is coalescing/batching output in Go before crossing the bridge, decided from spike results rather than assumed upfront.

Both spikes' findings get folded into this design (via a follow-up edit or the next change) before any SSH or multi-host work begins.

### Decision: Domain model shape — Host > Session > Window > Pane
**What**: `Host` wraps a connection; `Session`, `Window`, `Pane` mirror tmux's own hierarchy. `Session` is keyed by `(HostID, Name)` as a compound key from the start, even though this change only has one host, because tmux session names are only unique per-server and will collide across hosts later.
**Why**: Matches tmux's own mental model (least translation cost), and baking in the compound key now avoids a breaking rename once multi-host lands.
**Alternatives considered**: Flat pane-indexed model (no session/window nesting) — rejected, loses the layout information the UI needs to render splits faithfully.

### Decision: Event bus separates high-frequency output from lifecycle events
**What**: `pane.output` is delivered on its own channel/topic, distinct from lifecycle events (`session.discovered`, `session.closed`, `window.layout-changed`, `pane.died`, `host.connected`, etc.).
**Why**: Mixing a high-frequency stream with low-frequency structural events risks head-of-line blocking or ordering surprises for consumers that care about structure but not every byte of output.
**Alternatives considered**: Single unified event stream — simpler but rejected for the reason above; can be revisited if the Wails spike shows no practical difference.

### Decision: Per-pane raw byte ring buffer, not headless terminal emulation
**What**: The engine keeps a bounded ring buffer of raw bytes per pane (for reconnect recovery and future cross-pane search), but does not run a headless terminal emulator (no interpreted screen/cursor state). Rendered terminal state (cursor position, screen contents) lives in the shell's xterm.js instance, not the engine.
**Why**: Headless emulation is a large scope increase (correctness of an ANSI/VT state machine) that isn't needed yet; the ring buffer is enough for this change's reconnect requirement and leaves the door open for future features without committing to them now.
**Alternatives considered**: Engine-side headless emulation (enables cross-pane search, output summarization later) — deferred, not rejected; revisit if/when those features are actually prioritized.

### Decision: Local tmux connection via control-mode subprocess, not a tmux client library
**What**: The engine connects to a local tmux server by spawning `tmux -CC attach` (or equivalent) against the local socket and treating its stdout as the control-mode stream, matching how the eventual SSH-based remote connection will work (`ssh <host> tmux -CC attach`).
**Why**: Keeps local and future-remote connection handling structurally identical (both are "a process whose stdout is a control-mode stream"), so the SSH change later is additive rather than a rework of the local path.
**Alternatives considered**: A Go tmux control-mode library, if one exists — rejected for this change to keep local and remote paths consistent; would also add a dependency of unknown maturity for a protocol we're independently validating via spike.
**Subprocess I/O requirement (confirmed by spike 1)**: `tmux -CC attach` requires a real pty on its stdin/stdout — a plain OS pipe fails with `tcgetattr failed`. The engine's subprocess handling must allocate a pty (e.g. via `github.com/creack/pty`) for every control-mode subprocess, local or remote.

### Decision: One control-mode subprocess per tracked session, not per host (revised after spike 1)
**What**: Spike 1 confirmed `%output` is scoped to the session a control-mode client is attached to — a client attached to session A never receives output from session B on the same server, even though structural notifications (`%sessions-changed`, `%unlinked-window-add`/`-close`) do fire server-wide regardless of which session is attached. The engine therefore spawns **one `tmux -CC attach -t <session>` subprocess per session it actively tracks**, not one per host. A `Host`'s connection lifecycle (`host.connected`/`host.disconnected`) refers to the host's underlying transport (local socket reachability, or later the SSH connection) being viable at all; each `Session` under that host has its own control-mode subprocess and its own attach/output lifecycle, independent of the others.
**Why**: This is a direct empirical finding (see spike-notes.md, section 1.2), not a preference — a per-host client cannot receive `%output` for sessions it isn't attached to, so per-host granularity would silently miss output from every session except whichever one the client happened to attach to.
**Alternatives considered**: A single per-host client that repeatedly `switch-client`s between sessions to poll each in turn — rejected: doesn't provide true live streaming for unattended sessions, and reintroduces a polling loop the control-mode protocol exists to avoid.
**Correction (this change's scope does require host-wide session discovery)**: `specs/tmux-engine/spec.md`'s "Session discovery" requirement mandates discovering *all* sessions present on a connected server at connection time and detecting sessions created/closed afterward without a reconnect — this is host-wide auto-discovery, not limited to explicitly pre-configured sessions. An earlier draft of this decision incorrectly deferred that as a "future refinement, not required for this change's scope"; it is required. Because `%sessions-changed`/`%unlinked-window-add`/`%unlinked-window-close` fire server-wide on *any* control-mode connection to that host (spike 1, section 1.2), no dedicated discovery-only subprocess is needed: the engine's first per-session subprocess for a host (or, once any tracked session exists, any one of that host's live subprocesses) also serves as the host's discovery listener. On `%sessions-changed`, the engine runs `list-sessions` on that same connection, diffs the result against the domain model's known sessions for that host, emits `session.discovered`/`session.closed` for the difference, and spawns/tears down the corresponding per-session subprocess for each newly discovered/closed session. If a host has zero tracked sessions yet (nothing to attach to), the engine must still be able to discover its first session — this requires one bootstrap `list-sessions` (or a control-mode attach to any existing session) at connect time, which also serves scenario "Sessions present before connection".
**Impact on this change**: `Session` in the domain model now owns its own subprocess handle and lifecycle state (not shared with sibling sessions under the same `Host`). Task 4.2's implementation spawns and manages one subprocess per session being tracked, and additionally wires host-wide `%sessions-changed` handling on at least one live subprocess per host to satisfy full session discovery, matching the specs in `specs/tmux-engine/spec.md`.

## Risks / Trade-offs

- [Risk] ~~Control-mode's `%output` scoping is per-server, not per-session~~ — **Resolved by spike 1**: scoping is per-session (see "One control-mode subprocess per tracked session" decision above). No further disambiguation needed; the connection-granularity revision above is the direct outcome.
- [Risk] One subprocess per tracked session (rather than per host) increases the number of long-lived child processes and ptys the engine manages as tracked-session count grows → Mitigation: acceptable for this change's single-host, explicitly-tracked-sessions scope; revisit process/pty resource limits if a later change tracks large numbers of sessions per host.
- [Risk] ~~Wails' event bridge can't sustain realistic output rates~~ — **Resolved by spike 2**: sustained 20,000 events/sec with zero backlog and no UI stalls, far above realistic pane output rates. No coalescing/debouncing needed for this change's scope.
- [Risk] Per-pane ring buffer sizing is a guess without real workload data → Mitigation: start with a conservative fixed size (e.g. last N KB), revisit once real usage patterns (long-running builds, verbose logs) are observed.
- [Trade-off] Deferring headless terminal emulation means the engine cannot answer "what does this pane currently show" without the shell's xterm.js state — acceptable because this change has exactly one consumer (the spike shell) and no search/summarization requirement yet.

## Migration Plan

Not applicable — greenfield change, no existing users or deployed state.

## Open Questions

- Exact ring buffer size per pane: deferred pending real usage observation; does not change the spec (recovery behavior) or task breakdown, only a constant.
- ~~Whether Wails' throughput (once measured) requires coalescing at the Go layer or is a non-issue~~ — **Resolved by spike 2**: no coalescing needed. At 20,000 events/sec (40x a realistic sustained rate), backlog stayed at 0 with no dropped frames or UI stalls (see spike-notes.md). Per-event `EventsEmit` calls, one per `%output` notification, are viable as designed.
