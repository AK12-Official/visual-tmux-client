## MODIFIED Requirements

### Requirement: Session list view

The client SHALL present the available tmux sessions, showing for each its name. The list SHALL also show a subtitle beneath each session's name derived from that session's representative pane, so a user can tell what a session is running without attaching to it. The list SHALL reflect server-side changes without the user having to trigger a refresh.

A row SHALL show only its name and, when one is available, its subtitle. Window count and attached state SHALL NOT be shown on the row.

The subtitle SHALL be taken from the first of these that is non-empty: the window name, suffixed with an asterisk when the window is active, followed by `: ` and the pane title when a title is present; otherwise the window name alone; otherwise the command currently running in the pane. When none is available the client SHALL omit the subtitle entirely rather than showing an empty or placeholder line. A subtitle SHALL NOT be shown for a session whose summary is absent, and its absence SHALL NOT be presented as an error.

The subtitle is supplementary: the session name and the actions available on the row SHALL remain present and usable whether or not a subtitle is shown.

#### Scenario: Sessions exist

- **WHEN** the client loads and sessions are available
- **THEN** it lists each session by name, with a subtitle beneath it when one is available

#### Scenario: Row content

- **WHEN** a session is listed
- **THEN** its row shows its name and, when available, its subtitle, and does not show its window count or attached state

#### Scenario: Subtitle shows the window and title

- **WHEN** a session's representative pane has a window name and a non-empty pane title
- **THEN** the client shows `window: title` beneath the session name, marking the window name when that window is active

#### Scenario: Subtitle falls back to the window name

- **WHEN** a session's representative pane has a window name but no title
- **THEN** the client shows the window name as the subtitle

#### Scenario: Subtitle falls back to the running command

- **WHEN** a session's representative pane has neither a window name nor a title, but a current command
- **THEN** the client shows the command as the subtitle

#### Scenario: Subtitle is absent

- **WHEN** a session's pane summary is absent, or all of its components are empty
- **THEN** the client renders the row without a subtitle and without an empty line

#### Scenario: Subtitle is supplementary

- **WHEN** a session has a subtitle
- **THEN** its name and the row's actions remain available as before

#### Scenario: Subtitle updates without a manual refresh

- **WHEN** the command running in a session changes
- **THEN** the client's subtitle reflects the new value on its next poll without the user taking any action

#### Scenario: No sessions exist

- **WHEN** the client loads and no sessions are available
- **THEN** it shows an explanatory empty state offering session creation, not an error

#### Scenario: A session is created or removed externally

- **WHEN** a session is created or terminated outside the client
- **THEN** the client's list reflects that change without the user taking any action

#### Scenario: Listing fails

- **WHEN** the session list cannot be retrieved
- **THEN** the client shows the reason and offers a retry, without displaying a stale list as if current
