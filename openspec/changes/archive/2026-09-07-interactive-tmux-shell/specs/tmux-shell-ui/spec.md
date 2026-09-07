## Purpose

The tmux-shell-ui capability is the desktop product shell: it renders a connected host's sessions, windows, and panes as an interactive terminal client, replacing raw `tmux` CLI usage for everyday session navigation, pane interaction, and session/window/pane management.

## ADDED Requirements

### Requirement: Session/window/pane navigation tree
The shell SHALL display the tracked sessions, windows, and panes of a connected host as a navigable tree, reflecting the engine's domain model, and SHALL keep it current as the model changes.

#### Scenario: Tree reflects current state
- **WHEN** the shell is connected to a host with one or more sessions
- **THEN** the tree shows every tracked session with its windows and panes, matching the engine's domain model

#### Scenario: Tree updates on model change
- **WHEN** a session, window, or pane is created, closed, or renamed on the connected host
- **THEN** the tree updates to reflect the change without requiring the user to manually refresh

### Requirement: Geometrically accurate pane rendering
The shell SHALL render the panes of the selected window as a grid of interactive terminal views positioned and sized according to the engine's mirrored layout geometry, matching tmux's own on-screen arrangement.

#### Scenario: Panes positioned per tmux layout
- **WHEN** a window with multiple panes in a non-trivial split arrangement is selected
- **THEN** each pane's terminal view is positioned and sized to match tmux's layout for that window

#### Scenario: Layout change re-renders panes
- **WHEN** the selected window's layout changes (e.g. a pane is split)
- **THEN** the shell's pane grid updates to match the new layout without a full window reload

### Requirement: Live pane output rendering
The shell SHALL render each visible pane's live output in its corresponding terminal view as the engine streams it, including previously buffered scrollback when a pane is first displayed.

#### Scenario: New output appears live
- **WHEN** a process in a visible pane produces output
- **THEN** the corresponding terminal view updates with that output without user action

#### Scenario: Scrollback shown on first display
- **WHEN** a pane is displayed for the first time in the current session (e.g. after selecting its window)
- **THEN** its terminal view is seeded with the pane's available scrollback before live output continues

### Requirement: Keyboard input to focused pane
The shell SHALL forward keyboard input (including control sequences and pasted text) from the focused pane's terminal view to that pane via the engine's input forwarding, and SHALL make clicking a pane's terminal view change which pane is focused.

#### Scenario: Typing reaches the focused pane
- **WHEN** the user types while a pane's terminal view is focused
- **THEN** the corresponding pane's running program receives that input

#### Scenario: Clicking a pane focuses it
- **WHEN** the user clicks a pane's terminal view that is not currently focused
- **THEN** that pane becomes focused and subsequent keyboard input is forwarded to it

### Requirement: Session, window, and pane management actions
The shell SHALL provide UI actions to create, kill, and rename sessions and windows, and to split and kill panes, invoking the corresponding engine operations.

#### Scenario: Create a session from the UI
- **WHEN** the user invokes the create-session action and provides a name
- **THEN** the shell requests session creation from the engine and the new session appears in the navigation tree once created

#### Scenario: Kill a pane from the UI
- **WHEN** the user invokes the kill-pane action on a pane
- **THEN** the shell requests that pane be killed from the engine and the pane grid updates once it is removed

#### Scenario: Management action fails
- **WHEN** a requested management action (create, kill, rename, split) fails at the engine or tmux level
- **THEN** the shell surfaces the failure to the user without leaving the navigation tree or pane grid in an inconsistent state
