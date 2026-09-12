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
