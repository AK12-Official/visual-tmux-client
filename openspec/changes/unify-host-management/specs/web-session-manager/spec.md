## MODIFIED Requirements

### Requirement: Web host administration and selection

After central authentication the browser SHALL offer host listing, addition, editing, capability testing, default selection and confirmed removal for both local and SSH connections. Common metadata controls SHALL behave consistently; connection-specific fields and explicit trust confirmation SHALL be shown only when applicable. Management controls SHALL NOT be hidden solely because a host uses local execution. Forms SHALL support password and private-key authentication with optional passphrase, without retrieving saved secrets. The workspace SHALL identify the selected host in session, terminal and file contexts. Batch session termination SHALL operate within the selected host and confirmations SHALL identify that host.

#### Scenario: Add remote host
- **WHEN** an administrator supplies host details, confirms its observed fingerprint and passes authentication
- **THEN** the host can be saved and selected for session and file operations without installing a remote agent

#### Scenario: Remove host with unsaved files
- **WHEN** the user removes a host with unsaved editor content
- **THEN** the browser warns before discarding those drafts and explains that removing the host does not terminate its tmux sessions

#### Scenario: Edit and remove local host
- **WHEN** the administrator opens local host management
- **THEN** rename, capability test, set-default and confirmed removal are available without SSH settings

#### Scenario: Capability rendering
- **WHEN** a capability response reports unavailable or not-applicable
- **THEN** the browser displays that state accurately rather than treating a response object as success

## ADDED Requirements

### Requirement: Explicit host selection and empty registry

After authenticating and loading hosts/settings, the browser SHALL restore a still-registered recent host, otherwise select a registered default, otherwise present host selection. A successful empty list SHALL show an add-host state without issuing session, file or terminal requests. Fetch failure SHALL show a recoverable error without synthesizing local registration. Host unavailability SHALL NOT automatically select another host. Removal of the active host during use SHALL stop target operations, preserve dirty drafts for recovery and require explicit selection of another host; it SHALL NOT use startup fallback. Stale responses SHALL NOT repopulate removed-host state.

#### Scenario: Recent host precedes default
- **WHEN** the browser remembers registered host A while B is the default
- **THEN** startup selects A

#### Scenario: Recent host is absent
- **WHEN** the remembered host is absent and a registered default exists
- **THEN** startup selects that default

#### Scenario: No valid preference
- **WHEN** neither a registered recent host nor a registered default exists
- **THEN** the browser presents host selection without guessing a target

#### Scenario: Empty registry
- **WHEN** the server returns no registered hosts
- **THEN** the browser offers adding a host and sends no target operations

#### Scenario: Host listing fails
- **WHEN** authenticated host discovery fails
- **THEN** the browser offers retry and does not fabricate a local host or treat the failure as an empty success

#### Scenario: Active host removed elsewhere
- **WHEN** another browser removes the active host while an editor is dirty
- **THEN** input and saves to that target stop, drafts remain recoverable and another host is selected only by explicit user action

### Requirement: Authentication handling follows error origin

Central authentication rejection SHALL return the browser to the login flow regardless of selected connection type. Host credential, trust and availability errors SHALL remain scoped to that host and preserve central login. The browser SHALL classify these failures using structured error codes/source rather than host IDs or message substrings.

#### Scenario: Central token expires on an SSH host page
- **WHEN** the Hub rejects the browser token while an SSH host is selected
- **THEN** the browser clears the rejected central credential and requests login rather than advising SSH credential repair

#### Scenario: SSH credential rejected
- **WHEN** the central token is valid and SSH authentication fails
- **THEN** the browser offers host repair, keeps central login and leaves other hosts usable
