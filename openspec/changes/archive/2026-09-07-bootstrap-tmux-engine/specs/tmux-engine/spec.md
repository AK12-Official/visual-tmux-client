## Purpose

The tmux-engine capability connects to a tmux server and continuously mirrors its live session/window/pane state and output into a queryable domain model, publishing every change as an event so any consumer (UI or otherwise) can observe it without polling.

## ADDED Requirements

### Requirement: Session discovery
The engine SHALL discover all sessions present on a connected tmux server at connection time, and SHALL detect sessions created or closed on that server afterward without requiring a reconnect.

#### Scenario: Sessions present before connection
- **WHEN** the engine connects to a tmux server that already has one or more sessions
- **THEN** the engine's domain model contains one entry per existing session, each with its current windows and panes

#### Scenario: Session created after connection
- **WHEN** a new tmux session is created on a server the engine is connected to
- **THEN** the engine emits a `session.discovered` event and the new session appears in the domain model

#### Scenario: Session closed after connection
- **WHEN** a session the engine is tracking is closed (all windows destroyed, or explicitly killed)
- **THEN** the engine emits a `session.closed` event and removes the session from the domain model

### Requirement: Window and pane topology mirroring
The engine SHALL mirror the window and pane structure of each tracked session, including window layout and pane identity, and SHALL keep this structure current as it changes on the server.

#### Scenario: Pane split
- **WHEN** a pane in a tracked window is split into two panes
- **THEN** the engine's domain model reflects two panes for that window, and emits a `window.layout-changed` event

#### Scenario: Window renamed
- **WHEN** a tracked window is renamed on the server
- **THEN** the engine's domain model reflects the new window name

### Requirement: Live pane output streaming
The engine SHALL stream raw output produced by each pane on a tracked session as it is produced, without waiting for the pane to become idle or for a manual refresh.

#### Scenario: Pane produces output
- **WHEN** a process running in a tracked pane writes to its terminal
- **THEN** the engine emits a `pane.output` event containing the pane's identifier and the raw bytes written, without blocking delivery of other events

### Requirement: Pane scrollback recovery after reconnect
The engine SHALL be able to recover a tracked pane's current on-screen content and recent scrollback after a connection to the tmux server is interrupted and re-established, without requiring the tmux session itself to still exist in its pre-interruption form beyond normal tmux persistence.

#### Scenario: Reconnect after transient disconnect
- **WHEN** the engine's connection to a tmux server drops and is re-established while the session is still alive on the server
- **THEN** the engine recovers each tracked pane's current screen content and available scrollback, and resumes streaming new output for that pane

### Requirement: Pane lifecycle notification
The engine SHALL notify consumers when a tracked pane terminates (its running command exits and the pane closes).

#### Scenario: Pane process exits
- **WHEN** the command running in a tracked pane exits and the pane is destroyed
- **THEN** the engine emits a `pane.died` event identifying the pane

### Requirement: Local tmux server connectivity
The engine SHALL be able to connect to a tmux server running on the local machine via its control-mode interface, without requiring any network transport.

#### Scenario: Connect to local tmux server
- **WHEN** the engine is configured to connect to a local tmux server (identified by its socket)
- **THEN** the engine establishes a control-mode connection and begins mirroring that server's sessions without any SSH or network step
