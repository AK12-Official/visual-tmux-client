## ADDED Requirements

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
