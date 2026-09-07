## Purpose

The web-session-manager capability is the browser client: it lists the tmux sessions on the connected machine, offers create, rename, and terminate actions, and renders one attached session as a correctly-sized interactive terminal with clear connection-state feedback and reliable reconnection.

## ADDED Requirements

### Requirement: Session list view

The client SHALL present the available tmux sessions, showing for each its name, window count, and whether it is attached elsewhere. The list SHALL reflect server-side changes without the user having to trigger a refresh.

#### Scenario: Sessions exist

- **WHEN** the client loads and sessions are available
- **THEN** it lists each session with its name, window count, and attached state

#### Scenario: No sessions exist

- **WHEN** the client loads and no sessions are available
- **THEN** it shows an explanatory empty state offering session creation, not an error

#### Scenario: A session is created or removed externally

- **WHEN** a session is created or terminated outside the client
- **THEN** the client's list reflects that change without the user taking any action

#### Scenario: Listing fails

- **WHEN** the session list cannot be retrieved
- **THEN** the client shows the reason and offers a retry, without displaying a stale list as if current

### Requirement: Session creation, renaming, and termination

The client SHALL let the user create a session with an optional name, rename an existing session, and terminate a session. Termination SHALL require explicit confirmation. Failures SHALL be surfaced with the reason.

#### Scenario: Create with a name

- **WHEN** the user submits a session name
- **THEN** the client requests creation and the new session appears in the list once created

#### Scenario: Create without a name

- **WHEN** the user requests creation without entering a name
- **THEN** the client requests creation without one and the resulting server-named session appears in the list

#### Scenario: Rename

- **WHEN** the user renames a session to a new name
- **THEN** the client requests the rename and the list reflects the new name once applied

#### Scenario: Terminate is confirmed

- **WHEN** the user requests termination and confirms it
- **THEN** the client requests termination and the session leaves the list once removed

#### Scenario: Terminate is not confirmed

- **WHEN** the user requests termination and dismisses the confirmation
- **THEN** no termination is requested and the session remains

#### Scenario: An action fails

- **WHEN** a create, rename, or terminate request fails
- **THEN** the client shows the failure reason and leaves the list consistent with actual server state

### Requirement: Terminal view

The client SHALL render an attached session as a single interactive terminal that displays the session's output and sends the user's keyboard input to it, without the client positioning or subdividing the session's contents.

#### Scenario: Output renders

- **WHEN** the attached session produces output
- **THEN** the terminal displays it, including colors, text attributes, and full-screen application rendering

#### Scenario: Typing reaches the session

- **WHEN** the user types while the terminal is focused
- **THEN** the program running in the session receives that input, including control characters and escape sequences for keys such as arrows

#### Scenario: Pasting reaches the session

- **WHEN** the user pastes text into the focused terminal
- **THEN** the pasted content is delivered to the session

#### Scenario: tmux draws its own splits

- **WHEN** the attached session has its own tmux splits
- **THEN** they appear as tmux renders them inside the single terminal view, and the client does not attempt to lay them out itself

### Requirement: Viewport-accurate terminal sizing

The client SHALL report the terminal's actual character grid dimensions to the server on attach and whenever they change, so the session's content is laid out to what the user can actually see. Reported dimensions SHALL be derived from measurement of the rendered terminal, never assumed.

#### Scenario: Initial size on attach

- **WHEN** the client attaches a session
- **THEN** it reports the measured column and row count of the rendered terminal, and the session's content wraps to the visible width with no truncated or wrapped-early lines

#### Scenario: Window is resized

- **WHEN** the browser window or terminal container is resized
- **THEN** the client re-measures, reports the new dimensions, and the content reflows to the new size

#### Scenario: Rapid resizing

- **WHEN** the user drags the window edge, producing a continuous stream of size changes
- **THEN** the client limits how often it reports, and the size it reports last matches the final dimensions

#### Scenario: Container is not measurable

- **WHEN** a size measurement is attempted while the terminal container has no renderable dimensions
- **THEN** the client skips that measurement without error and reports once dimensions are available

### Requirement: Connection state feedback

The client SHALL show the user the terminal connection's current state, distinguishing connecting, connected, reconnecting, and terminated. A terminated session SHALL be visually distinct from a dropped connection that is being retried.

#### Scenario: Connecting

- **WHEN** a terminal connection is being established
- **THEN** the client indicates it is connecting, without showing a failure

#### Scenario: Connection drops

- **WHEN** an established terminal connection drops unexpectedly
- **THEN** the client indicates it is reconnecting and shows that retries are in progress

#### Scenario: Session ended

- **WHEN** the server reports the attached session has ended
- **THEN** the client shows that the session ended and does not present it as a recoverable connection problem

#### Scenario: Attachment refused

- **WHEN** the server refuses the attachment
- **THEN** the client shows the reason and does not silently retry indefinitely

### Requirement: Terminal reconnection

The client SHALL automatically attempt to restore a terminal connection that dropped unexpectedly, spacing successive attempts by an increasing delay up to a bound. It SHALL NOT retry when the server indicated the session ended or the attachment was refused. Only the most recent attempt SHALL be allowed to take effect.

#### Scenario: Reconnect succeeds

- **WHEN** a dropped connection is re-established
- **THEN** the terminal resumes showing live output and accepting input, without the user re-navigating

#### Scenario: Repeated failures

- **WHEN** reconnection attempts keep failing
- **THEN** the delay between attempts increases up to a bound rather than retrying without pause

#### Scenario: Session ended

- **WHEN** the connection closed because the session ended
- **THEN** the client does not attempt to reconnect

#### Scenario: A stale attempt completes late

- **WHEN** an earlier connection attempt completes after a newer one has already been started
- **THEN** the stale attempt is discarded and does not replace the current connection

### Requirement: Terminal text copy

The client SHALL let the user select terminal text with the mouse and copy it, without that selection being consumed by the session as mouse input. Copying SHALL NOT interfere with sending an interrupt to the session when no selection exists.

#### Scenario: Select and copy

- **WHEN** the user selects terminal text and issues the copy command
- **THEN** the selected text is placed on the system clipboard

#### Scenario: Copy shortcut with no selection

- **WHEN** the user issues the copy shortcut with nothing selected
- **THEN** the corresponding control character is sent to the session instead of a copy being performed

#### Scenario: Clipboard write is blocked

- **WHEN** the browser refuses the clipboard write
- **THEN** the client tells the user the copy did not happen rather than indicating success

### Requirement: Credential entry and session persistence

The client SHALL prompt for the server credential when it does not have a valid one, retain it across page reloads so the user need not re-enter it each visit, and return to the prompt when the server rejects it.

#### Scenario: No credential held

- **WHEN** the client loads without a stored credential
- **THEN** it prompts for one and does not display a session list until authenticated

#### Scenario: Credential is rejected

- **WHEN** a stored credential is rejected by the server
- **THEN** the client discards it, prompts again, and states that authentication failed

#### Scenario: Page is reloaded

- **WHEN** the user reloads the page while holding a valid credential
- **THEN** the client authenticates without prompting again

### Requirement: Long-lived credentials absent from addresses

The client SHALL NOT place long-lived credentials in any URL, including the terminal connection's address, so credentials are not recorded in browser history, server logs, or proxy logs.

#### Scenario: Opening a terminal

- **WHEN** the client opens a terminal connection
- **THEN** the address it uses contains no long-lived credential, only a short-lived single-use authorization
