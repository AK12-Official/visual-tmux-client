## MODIFIED Requirements

### Requirement: Session listing

The hub SHALL expose an operation that returns the tmux sessions currently present on the tmux server it manages. Each returned session SHALL carry at least its name, its window count, its attached state, and its creation time. Each returned session SHALL also carry a summary of its most representative pane, comprising that pane's window name, its title, the command currently running in it, and whether its window is the session's active window.

The representative pane SHALL be chosen by skipping panes that have exited and then preferring, in order: a pane that is both in the active window and active, then an active pane, then a pane in the active window, then any remaining live pane. Panes of equal rank SHALL be separated by the lowest window index and then the lowest pane index, regardless of tmux output order. Window names, pane titles, and commands are free-form text. The pane wire format SHALL use the printable multi-character sentinel `|vtc-pane|` as its field separator; a normal `|` is valid field content. The parser SHALL require exactly the expected number of fields and omit malformed records. Each field SHALL be capped at 200 Unicode code points at a character boundary before it is returned. The line-based format assumes fields do not contain the raw newline record terminator supplied by tmux.

When no representative pane can be determined, or when the pane query fails, the hub SHALL still return the session list successfully with the summary absent for the affected sessions, rather than failing the listing.

#### Scenario: Sessions exist

- **WHEN** a caller requests the session list and the tmux server has one or more sessions
- **THEN** the hub returns one entry per session with its name, window count, attached state, and creation time

#### Scenario: Pane summary is present

- **WHEN** a caller requests the session list and a session has at least one live pane
- **THEN** that session's entry carries the representative pane's window name, title, current command, and whether its window is active

#### Scenario: Represented pane selection

- **WHEN** a session has several live panes, one of them the active pane of the active window
- **THEN** the summary describes that pane rather than any other

#### Scenario: Equal-ranked panes

- **WHEN** a session has several live panes that rank equally, and tmux reports them in an arbitrary order
- **THEN** the summary describes the one in the lowest-numbered window, or in the lowest-numbered pane when they share a window

#### Scenario: Exited panes are ignored

- **WHEN** a session's only panes have exited
- **THEN** the session is still listed and its pane summary is absent

#### Scenario: Pane query fails

- **WHEN** the pane query fails while the session listing succeeds
- **THEN** the hub returns the session list with pane summaries absent rather than reporting the whole listing as failed

#### Scenario: Free-form window name and title

- **WHEN** a session's window name, pane title, or current command contains spaces or non-ASCII text
- **THEN** the hub reports those values as tmux gave them, without misattribution between fields, each capped in length so one pane cannot inflate every listing response

#### Scenario: A value carrying the field separator itself

- **WHEN** a value contains the field separator, so its record no longer splits into the expected number of fields
- **THEN** the hub omits that record rather than reporting a summary whose fields may have shifted

#### Scenario: Oversized pane field

- **WHEN** a pane's window name, title, or current command is longer than 200 Unicode code points
- **THEN** the hub returns that field capped at 200 code points without splitting a character

#### Scenario: No sessions exist

- **WHEN** a caller requests the session list and the tmux server has no sessions
- **THEN** the hub returns an empty list and reports success, not an error

#### Scenario: No tmux server is running

- **WHEN** a caller requests the session list and no tmux server is running
- **THEN** the hub returns an empty list and reports success, treating "no server" as "no sessions"

#### Scenario: The tmux binary is unavailable

- **WHEN** a caller requests the session list and no tmux executable can be found
- **THEN** the hub returns an error identifying that tmux could not be found, distinguishable by the caller from an empty session list
