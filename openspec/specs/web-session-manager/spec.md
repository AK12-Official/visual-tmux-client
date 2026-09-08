# web-session-manager Specification

## Purpose
The web-session-manager capability is the browser client: it lists the tmux sessions on the connected machine, offers create, rename, and terminate actions, and renders one attached session as a correctly-sized interactive terminal with clear connection-state feedback and reliable reconnection.

## Requirements

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

### Requirement: Session ordering

The client SHALL offer a default ordering mode and a manual ordering mode. In manual mode the user SHALL be able to reorder sessions and pin sessions to the top. The active mode, the manual order, and the pins SHALL persist across page reloads. In default mode the ordering SHALL follow the server's listing.

#### Scenario: Enabling manual ordering

- **WHEN** the user switches to manual ordering
- **THEN** sessions appear in the user's saved order, with pinned sessions ahead of unpinned ones

#### Scenario: Reordering

- **WHEN** the user moves a session to a new position in manual mode
- **THEN** the list reflects the new position immediately and after a page reload

#### Scenario: Pinning

- **WHEN** the user pins a session in manual mode
- **THEN** the session moves to the front and remains there across page reloads until unpinned

#### Scenario: A new session appears during manual ordering

- **WHEN** a session is created while manual ordering is active
- **THEN** it appears in the list without disturbing the saved positions of the existing sessions

### Requirement: Session activity indication

The client SHALL indicate when a session's terminal has recently produced output. A session other than the currently viewed one SHALL be visually highlighted on its list card while it has recent output, and that highlight SHALL fade shortly after its output stops. The viewed session's activity SHALL be indicated by distinct means — in the terminal header and in the browser tab title — so that activity is noticeable even when the browser tab is not the visible one.

#### Scenario: A background session produces output

- **WHEN** a session other than the viewed one produces terminal output
- **THEN** its list card shows a visually distinct highlight

#### Scenario: The highlight decays

- **WHEN** a highlighted session stops producing output
- **THEN** its card highlight fades within a few seconds

#### Scenario: The viewed session produces output

- **WHEN** the viewed session produces terminal output
- **THEN** the terminal header shows an activity indicator and the browser tab title is marked, and both clear when output stops

#### Scenario: The browser tab is backgrounded

- **WHEN** the client's browser tab is not the active tab and the viewed session produces output
- **THEN** the browser tab title alone still signals the activity

### Requirement: Collapsible session sidebar

The client SHALL let the user collapse the session sidebar to a narrow form and expand it again, with the terminal area using the freed space. The collapsed or expanded state SHALL persist across page reloads.

#### Scenario: Collapsing and expanding

- **WHEN** the user toggles the sidebar
- **THEN** it collapses to a narrow form or expands back, and the terminal area resizes accordingly

#### Scenario: The state persists

- **WHEN** the page is reloaded
- **THEN** the sidebar is in the collapsed or expanded state the user last chose

### Requirement: Session creation, renaming, and termination

The client SHALL let the user create a session with a single action that requires no name — the session is created with a server-generated default name, and renaming remains the way to change it afterward. The client SHALL let the user rename an existing session and terminate a session. Termination SHALL require explicit confirmation. Failures SHALL be surfaced with the reason.

#### Scenario: Create with a name

- **WHEN** the user activates session creation and then renames the new session to a chosen name
- **THEN** the session appears in the list under that name

#### Scenario: Create without a name

- **WHEN** the user activates session creation
- **THEN** the client requests creation without a name and the resulting server-named session appears in the list once created

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

### Requirement: Batch session termination

The client SHALL let the user select multiple sessions and terminate all of the selected ones in a single confirmed action.

#### Scenario: Multi-select and terminate

- **WHEN** the user selects several sessions and confirms bulk termination
- **THEN** each selected session is terminated and leaves the list, and unselected sessions are untouched

#### Scenario: Partial failure

- **WHEN** some terminations within a bulk action fail
- **THEN** the client reports which sessions failed and leaves the list consistent with actual server state

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

The client SHALL show the user the terminal connection's current state, distinguishing connecting, connected, reconnecting, and terminated. A terminated session SHALL be visually distinct from a dropped connection that is being retried. When a terminal connection ends, the client SHALL distinguish the case where the underlying session still exists (for example, after the user detached) from the case where the session is gone, and SHALL offer an explicit re-attachment action in the former case.

#### Scenario: Connecting

- **WHEN** a terminal connection is being established
- **THEN** the client indicates it is connecting, without showing a failure

#### Scenario: Connection drops

- **WHEN** an established terminal connection drops unexpectedly
- **THEN** the client indicates it is reconnecting and shows that retries are in progress

#### Scenario: Session ended

- **WHEN** the server reports the attached session has ended
- **THEN** the client shows that the session ended and does not present it as a recoverable connection problem

#### Scenario: Detached but session alive

- **WHEN** the terminal connection ends because the user detached, and the session still exists
- **THEN** the client indicates the session is detached and offers an explicit action to re-attach it

#### Scenario: Attachment refused

- **WHEN** the server refuses the attachment
- **THEN** the client shows the reason and does not silently retry indefinitely

### Requirement: Terminal reconnection

The client SHALL automatically attempt to restore a terminal connection that dropped unexpectedly, spacing successive attempts by an increasing delay up to a bound. It SHALL NOT automatically retry when the server indicated the session ended or the attachment was refused. When a terminal connection has ended and its session still exists, the client SHALL offer the user an explicit action that builds a fresh attachment to that session without reloading the page. Only the most recent attempt SHALL be allowed to take effect.

#### Scenario: Reconnect succeeds

- **WHEN** a dropped connection is re-established
- **THEN** the terminal resumes showing live output and accepting input, without the user re-navigating

#### Scenario: Repeated failures

- **WHEN** reconnection attempts keep failing
- **THEN** the delay between attempts increases up to a bound rather than retrying without pause

#### Scenario: Session ended

- **WHEN** the connection closed because the session ended
- **THEN** the client does not attempt to reconnect automatically

#### Scenario: User re-attaches after detaching

- **WHEN** the user detached from a session and activates the offered re-attach action
- **THEN** the client builds a new terminal connection to that session and live output resumes without a page reload

#### Scenario: A stale attempt completes late

- **WHEN** an earlier connection attempt completes after a newer one has already been started
- **THEN** the stale attempt is discarded and does not replace the current connection

### Requirement: Terminal header controls

The client SHALL present a header above the attached terminal that identifies the session by name and its connection state, and offers font-size decrease and increase, fullscreen, and close actions. Closing SHALL return the user to the session-selection state without terminating the session. Font-size changes SHALL take effect immediately, trigger a re-fit of the terminal grid, and persist across page reloads. Fullscreen SHALL use the browser's fullscreen facility for the terminal area and re-negotiate the terminal size on both entering and leaving.

#### Scenario: The header identifies the session

- **WHEN** a session is selected
- **THEN** the terminal header shows that session's name and its current connection state

#### Scenario: Changing the font size

- **WHEN** the user increases or decreases the font size
- **THEN** the terminal re-renders at the new size, reports the re-measured grid dimensions, and the choice persists across page reloads

#### Scenario: Entering fullscreen

- **WHEN** the user enters fullscreen
- **THEN** the terminal area fills the screen and its grid is re-negotiated for the new size; leaving fullscreen restores the previous layout and size

#### Scenario: Closing the terminal panel

- **WHEN** the user closes the terminal panel
- **THEN** the session-selection state is shown and the session remains alive and listed

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

The client SHALL prompt for the server credential when it does not have a valid one, retain it across page reloads so the user need not re-enter it each visit, and return to the prompt when the server rejects it. The client SHALL NOT enter the main view until the submitted credential has been accepted by the server, and SHALL reject at the prompt — without storing it — a credential that cannot be sent in an HTTP header.

#### Scenario: No credential held

- **WHEN** the client loads without a stored credential
- **THEN** it prompts for one and does not display a session list until authenticated

#### Scenario: An invalid credential is submitted

- **WHEN** the user submits a credential the server rejects
- **THEN** the client stays on the credential prompt, states that authentication failed, and does not enter the main view

#### Scenario: A credential that cannot be sent in a header is submitted

- **WHEN** the user submits a credential containing characters that cannot appear in an HTTP header value, such as full-width IME characters
- **THEN** the client rejects it at the prompt with an explanatory message and stores nothing

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
