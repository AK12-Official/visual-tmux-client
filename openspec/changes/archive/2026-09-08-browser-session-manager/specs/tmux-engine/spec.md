## REMOVED Requirements

### Requirement: Session discovery

**Reason**: The engine's control-mode connection is removed. Session discovery is no longer a continuously-mirrored, event-driven model; it becomes an on-demand query answered by executing a one-shot `tmux list-sessions`.

**Migration**: Use `session-hub`'s "Session listing" requirement. Consumers that previously observed `session.discovered`/`session.closed` events now re-query the session list; the browser client polls rather than subscribing.

### Requirement: Window and pane topology mirroring

**Reason**: tmux now renders its own windows and panes inside a single attached terminal, so the product no longer needs a mirrored topology to position anything. Maintaining a live window/pane model has no consumer.

**Migration**: None required. Window and pane structure is visible to the user because tmux draws it directly. Window count per session is available from `session-hub`'s "Session listing".

### Requirement: Live pane output streaming

**Reason**: Output is no longer demultiplexed per pane. The product streams one raw byte stream per attached session, exactly as tmux produces it, because tmux composes its own panes into that stream.

**Migration**: Use `session-hub`'s "Terminal attachment" and "Byte-faithful terminal output" requirements. Per-pane `pane.output` events no longer exist and have no equivalent.

### Requirement: Pane scrollback recovery after reconnect

**Reason**: Scrollback is no longer reconstructed by the engine. A fresh attachment causes tmux to repaint from its own history, which is authoritative and preserves color and attributes — unlike the previous `capture-pane` snapshot, which discarded them.

**Migration**: Use `session-hub`'s "Terminal size negotiation" (specifically the post-attach repaint scenario). The per-pane ring-buffer scrollback API is removed; `session-hub`'s "Bounded pre-ready output staging" covers only output produced between attach and readiness.

### Requirement: Pane lifecycle notification

**Reason**: Pane death is no longer tracked, because no consumer positions or renders individual panes. Session-level termination is what callers need to react to.

**Migration**: Use `session-hub`'s "Session end notification". Pane exits are visible to the user through tmux's own rendering.

### Requirement: Local tmux server connectivity

**Reason**: Control mode (`tmux -CC`) is abandoned. Connectivity is no longer a persistent connection to be established and maintained; it is a combination of one-shot command execution and a pty-backed attachment created per terminal.

**Migration**: Use `session-hub`'s "Terminal attachment" and "Shell-injection-safe tmux invocation". There is no longer a connect/disconnect lifecycle to manage.

### Requirement: Session management operations

**Reason**: These operations move out of an in-process engine library and become authenticated server operations, gaining validation, exact-target semantics, and conflict reporting that the engine's version did not specify.

**Migration**: Use `session-hub`'s "Session creation", "Session renaming", "Session termination", and "Exact session targeting".

### Requirement: Window management operations

**Reason**: Out of scope for the session-manager product. Window creation, renaming, and termination are performed by the user inside tmux itself, using tmux's own key bindings, in the attached terminal.

**Migration**: None. Users manage windows through tmux directly.

### Requirement: Pane management operations

**Reason**: Out of scope for the session-manager product. Splitting, killing, and selecting panes are performed by the user inside tmux itself, using tmux's own key bindings, in the attached terminal.

**Migration**: None. Users manage panes through tmux directly.

### Requirement: Pane input forwarding

**Reason**: Input is no longer addressed to a specific pane via `send-keys`. It is written to the attached session's terminal, and tmux routes it to whichever pane it considers active — matching how a conventionally attached tmux client behaves.

**Migration**: Use `session-hub`'s "Terminal attachment". Input reaches the active pane without the caller identifying it.

### Requirement: Window layout geometry

**Reason**: Layout geometry has no consumer. The product no longer positions panes; tmux draws its own layout, including split borders, into the attached terminal's byte stream.

**Migration**: None. Layout correctness is now tmux's responsibility, achieved by `session-hub`'s "Terminal size negotiation" giving tmux the caller's true viewport size.
