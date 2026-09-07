# tmux-engine Specification

## Purpose
The tmux-engine capability connects to a tmux server and continuously mirrors its live session/window/pane state and output into a queryable domain model, publishing every change as an event so any consumer (UI or otherwise) can observe it without polling.

## Requirements

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

### Requirement: Session management operations
The engine SHALL provide operations to create a new session on a connected host, kill a tracked session, and rename a tracked session.

#### Scenario: Create session
- **WHEN** a caller requests a new session be created on a connected host
- **THEN** tmux creates the session and it subsequently appears in the engine's domain model per the existing session-discovery behavior

#### Scenario: Kill session
- **WHEN** a caller requests a tracked session be killed
- **THEN** tmux destroys the session and it is removed from the engine's domain model per the existing session-discovery behavior

#### Scenario: Rename session, engine-initiated or external
- **WHEN** a tracked session is renamed, whether by a caller's rename request or by another tmux client renaming it directly
- **THEN** the engine updates the session's key and name in its domain model and emits a `session.renamed` event, without requiring a reconnect

### Requirement: Window management operations
The engine SHALL provide operations to create a new window in a tracked session, kill a tracked window, and rename a tracked window.

#### Scenario: Create window
- **WHEN** a caller requests a new window be created in a tracked session
- **THEN** tmux creates the window and it appears in the engine's domain model with its initial pane

#### Scenario: Kill window
- **WHEN** a caller requests a tracked window be killed
- **THEN** tmux destroys the window and the engine removes it (and its panes) from the domain model

#### Scenario: Rename window
- **WHEN** a caller requests a tracked window be renamed
- **THEN** tmux renames the window and the engine's domain model reflects the new name

### Requirement: Pane management operations
The engine SHALL provide operations to split a tracked pane (horizontally or vertically), kill a tracked pane, and select (focus) a pane within its window.

#### Scenario: Split pane
- **WHEN** a caller requests a tracked pane be split
- **THEN** tmux creates a new pane in that window and the engine's domain model reflects both resulting panes with updated layout geometry

#### Scenario: Kill pane
- **WHEN** a caller requests a tracked pane be killed
- **THEN** tmux destroys the pane and the engine emits a `pane.died` event and removes it from the domain model

#### Scenario: Select (focus) a pane
- **WHEN** a caller requests a pane within a tracked window be made active, or the active pane changes for any other reason (e.g. another client's action)
- **THEN** the engine's domain model marks that pane active and any previously active pane in the same window inactive, and emits a `pane.focus-changed` event

### Requirement: Pane input forwarding
The engine SHALL forward raw input bytes to a specific tracked pane on behalf of a caller, without interpreting or translating the bytes, so a consumer's terminal input (including control sequences and pasted text) is delivered to the pane unmodified.

#### Scenario: Forward input to a live pane
- **WHEN** a caller submits raw bytes for a tracked, live pane
- **THEN** the engine delivers those bytes to the pane such that the pane's running program receives them exactly as if typed at an attached terminal

#### Scenario: Forward input to an untracked or dead pane
- **WHEN** a caller submits input for a pane the engine is not tracking, or that has already died
- **THEN** the engine returns an error and does not affect any other pane

### Requirement: Window layout geometry
The engine SHALL derive each pane's position and size within its window's layout from tmux's layout data, in addition to the pane-membership topology it already mirrors, and SHALL keep this geometry current as the layout changes.

#### Scenario: Geometry available for a tracked window
- **WHEN** the engine mirrors a tracked window's layout
- **THEN** each pane in that window has a position and size in the engine's domain model matching tmux's own layout

#### Scenario: Geometry updates on layout change
- **WHEN** a tracked window's layout changes (e.g. a pane is split or the window is resized)
- **THEN** the position and size of every affected pane in the domain model is updated to match
