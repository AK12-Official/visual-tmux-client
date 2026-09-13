## MODIFIED Requirements

### Requirement: Terminal text copy

The client SHALL let the user select terminal text with the mouse (using Shift-drag or platform modifier drag when terminal mouse reporting is active) and copy it, without that selection being consumed by the session as mouse input. Copying SHALL NOT interfere with sending an interrupt to the session when no selection exists.

#### Scenario: Select and copy

- **WHEN** the user selects terminal text (using Shift-drag when mouse reporting is active) and issues the copy command
- **THEN** the selected text is placed on the system clipboard

#### Scenario: Copy shortcut with no selection

- **WHEN** the user issues the copy shortcut with nothing selected
- **THEN** the corresponding control character is sent to the session instead of a copy being performed

#### Scenario: Clipboard write is blocked

- **WHEN** the browser refuses the clipboard write
- **THEN** the client tells the user the copy did not happen rather than indicating success

## ADDED Requirements

### Requirement: Terminal session scrolling

The client SHALL support scrolling up and down through session context and history using mouse wheel and trackpad gestures when session output exceeds the viewport height.

#### Scenario: Scrolling up into session history

- **WHEN** the user scrolls the mouse wheel or swipes up on the trackpad over an attached terminal whose output exceeds the viewport
- **THEN** mouse scroll events are forwarded to the session and the view scrolls upward through earlier context and history

#### Scenario: Scrolling down to the bottom

- **WHEN** the user scrolls downward while reviewing session history and reaches the bottom
- **THEN** the view returns to the active command line prompt without requiring manual exit keys

#### Scenario: Text copying remains functional

- **WHEN** mouse support is active and the user selects text using Shift-drag (or the platform selection modifier) and issues copy
- **THEN** the selected text is placed on the system clipboard
