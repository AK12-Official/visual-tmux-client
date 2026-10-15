## ADDED Requirements

### Requirement: Web host administration and selection

After central authentication the browser SHALL offer host listing, addition, editing, explicit trust confirmation, connection testing and confirmed removal. Forms SHALL support password and private-key authentication with optional passphrase, without retrieving saved secrets. The workspace SHALL identify the selected host in session, terminal and file contexts. Batch session termination SHALL operate within the selected host and confirmations SHALL identify that host.

#### Scenario: Add remote host
- **WHEN** an administrator supplies host details, confirms its observed fingerprint and passes authentication
- **THEN** the host can be saved and selected for session and file operations without installing a remote agent

#### Scenario: Remove host with unsaved files
- **WHEN** the user removes a host with unsaved editor content
- **THEN** the browser warns before discarding those drafts and explains that removing the host does not terminate its tmux sessions

### Requirement: Host-qualified workspace state

Sessions, terminal connections, activity, ordering and pins SHALL be scoped by host and session identity. File state SHALL include host identity and connection revision. Host switching SHALL preserve unsaved content and SHALL NOT redirect input or saves. Late responses from another host or an obsolete revision SHALL NOT overwrite current state. Legacy saved ordering SHALL migrate once to the local host only.

#### Scenario: Duplicate session names
- **WHEN** A and B both have dev and A's dev produces output
- **THEN** only A's dev activity and connection state change

#### Scenario: Late list response
- **WHEN** A's list response arrives after the user selects B
- **THEN** it does not replace B's visible session list

#### Scenario: Old ordering preference
- **WHEN** a browser with pre-multi-host pins opens the new version
- **THEN** those pins apply to local sessions only and migration does not overwrite later edits

### Requirement: Host failures preserve workspace intent

Failed host polls SHALL NOT be treated as successful empty lists or cause panels and dirty editors to be discarded. Cached results SHALL be marked stale. Remote authentication or server-trust errors SHALL stop automatic authentication retries for that condition and offer host repair without clearing the central login. Uncertain mutations SHALL be shown as uncertain, without automatically retrying them or reporting success.

#### Scenario: Offline host with open files
- **WHEN** a host becomes unreachable
- **THEN** its cached state is visibly stale, its edits remain available and other hosts remain usable

#### Scenario: Host replaced through another browser
- **WHEN** a host revision changes or is removed while an editor is open
- **THEN** the browser retains drafts but blocks saving with the old revision until the user resolves the change

#### Scenario: Remote authentication fails
- **WHEN** a host rejects its SSH credentials
- **THEN** the browser offers editing that host's credentials and does not return to the central login prompt
