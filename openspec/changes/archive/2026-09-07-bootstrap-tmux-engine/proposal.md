## Why

We want to build a full visual tmux client (not just a session manager): live, interactive pane rendering across hosts, in an embedded desktop shell rather than a browser-served app. Before committing to an implementation, two architectural assumptions are unverified: (1) the exact scoping/semantics of tmux's control-mode protocol (`-CC`, `%output`, reconnection via `capture-pane`), and (2) whether a Wails (Go) shell's Go<->JS event bridge can sustain high-frequency pane-output events without the frontend stalling. Both are load-bearing for the engine/shell split this project depends on, and both are cheap to test before writing production code.

## What Changes

- Establish the target architecture: a headless **core engine** (pure Go, no UI dependency) that mirrors tmux state via the control-mode protocol, paired with a **Wails (Go) shell** embedding a native webview (xterm.js for pane rendering) — no HTTP server, no browser-accessed port.
- Define the engine's domain model (`Host > Session > Window > Pane`) and its event bus contract (`pane.output`, `session.discovered`, `host.connected`, etc., with high-frequency `pane.output` isolated from lifecycle events).
- Adopt system SSH subprocess (`ssh <host> tmux -CC attach`) as the remote connection strategy, deferring to `~/.ssh/config` for auth/ProxyJump/agent semantics instead of a native SSH library.
- Run two spikes to de-risk the above before implementation begins:
  - Control-mode protocol spike: verify `%output` scoping (per-session vs per-server), `-C` vs `-CC` differences, and whether `capture-pane -p -S -` can rebuild pane state after a dropped connection.
  - Wails event-throughput spike: verify the Go<->JS event bridge can handle high-frequency simulated `pane.output` events without dropped frames or backpressure.
- Scaffold the core engine package skeleton (domain model types, event bus, no SSH/UI wiring yet) against a **local** tmux server (Unix socket, no SSH) to validate the model end-to-end in the simplest environment.

## Capabilities

### New Capabilities
- `tmux-engine`: headless engine capability that connects to a local tmux server via control mode, mirrors its session/window/pane state into the domain model, and publishes state changes on an event bus (including raw pane output and lifecycle events).

### Modified Capabilities
(none — greenfield project, no existing specs)

## Impact

- New Go module: core engine package (control-mode parser, domain model, event bus, per-pane byte ring buffer).
- New Wails project scaffold: shell embedding xterm.js, wired to the engine's event bus for at least a throughput spike.
- No SSH, multi-host, or write/management operations (create/kill session, split pane, etc.) in this change — those are follow-on changes once the engine's local-tmux path and spike results are validated.
- No production dependencies on external services; spikes are throwaway harnesses, not shipped code (though the engine skeleton they validate is kept).
