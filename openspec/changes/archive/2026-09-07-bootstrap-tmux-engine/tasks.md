## 1. Spike: tmux control-mode protocol

- [x] 1.1 Start a local tmux server with 2+ sessions and multiple windows/panes, attach via `tmux -CC`, and record raw control-mode output to a file for later reference
- [x] 1.2 Determine and document whether `%output` events are scoped per-session or per-server (create a second session while attached and observe whether its output appears)
- [x] 1.3 Determine and document the practical difference between `-C` and `-CC` for this project's purposes
- [x] 1.4 Kill the control-mode connection mid-session (simulate a dropped SSH connection) and verify whether `capture-pane -p -S -` can fully reconstruct a pane's current screen and scrollback afterward
- [x] 1.5 Write up spike findings as a short note in this change directory (e.g. `spike-notes.md`) covering the outcomes of 1.2-1.4, and flag any finding that requires revisiting design.md's connection-granularity decision

## 2. Spike: Wails event throughput

- [x] 2.1 Scaffold a minimal throwaway Wails (Go) project with a single window and xterm.js embedded
- [x] 2.2 Implement a Go-side generator that emits simulated `pane.output` events at a high, sustained rate (representative of `yes` or `htop`-style output) across the Go<->JS bridge
- [x] 2.3 Render received events into xterm.js and observe for dropped output, unbounded latency growth, or UI stalls under sustained load
- [x] 2.4 Document the measured throughput ceiling and whether Go-side coalescing/batching is needed, appended to the same `spike-notes.md` from task 1.5
- [x] 2.5 Delete or clearly mark the throwaway Wails spike project as non-production (it is not the shell scaffolded in section 4)

## 3. Engine: domain model and event bus

- [x] 3.1 Create the core engine Go module/package with no dependency on Wails or any UI framework
- [x] 3.2 Implement domain model types (`Host`, `Session`, `Window`, `Pane`) per design.md, with `Session` keyed by `(HostID, Name)`, and verify with unit tests constructing and querying the model
- [x] 3.3 Implement the event bus with distinct channels/topics for high-frequency `pane.output` versus lifecycle events (`session.discovered`, `session.closed`, `window.layout-changed`, `pane.died`, `host.connected`), and verify with a unit test that a slow lifecycle subscriber does not block output delivery
- [x] 3.4 Implement the per-pane bounded raw-byte ring buffer and verify with a unit test that it retains the most recent bytes up to its bound and discards older ones

## 4. Engine: local tmux control-mode connection

- [x] 4.1 Implement a control-mode parser for the events needed by the specs in `specs/tmux-engine/spec.md` (session/window/pane discovery, output, layout changes, pane death), informed by spike 1's findings, and verify with unit tests against recorded control-mode output from task 1.1
- [x] 4.2 Implement local tmux connection handling: spawn one `tmux -CC attach -t <session>` subprocess per tracked session against a local socket (per design.md's per-session connection-granularity revision), allocate a pty for each subprocess, feed its stdout into the parser, and populate the domain model
- [x] 4.3 Verify end-to-end against a real local tmux server: connecting discovers existing sessions (Scenario: "Sessions present before connection"), creating/closing a session emits the corresponding events (Scenarios: "Session created/closed after connection")
- [x] 4.4 Verify pane output streaming end-to-end: running a command in a tracked pane produces `pane.output` events with correct pane identifiers and bytes (Scenario: "Pane produces output")
- [x] 4.5 Verify window/pane topology mirroring end-to-end: splitting a pane and renaming a window produce the expected domain model changes and events (Scenarios: "Pane split", "Window renamed")
- [x] 4.6 Verify pane lifecycle end-to-end: a pane's command exiting emits `pane.died` (Scenario: "Pane process exits")
- [x] 4.7 Implement reconnect recovery using `capture-pane -p -S -` per spike 1's findings, and verify a dropped-then-restored connection recovers each tracked pane's screen and scrollback (Scenario: "Reconnect after transient disconnect")

## 5. Validation

- [x] 5.1 Run `openspec validate --change bootstrap-tmux-engine --strict` and confirm it passes
- [x] 5.2 Confirm every scenario in `specs/tmux-engine/spec.md` has a corresponding verification step completed above
