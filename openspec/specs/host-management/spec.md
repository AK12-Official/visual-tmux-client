# host-management Specification

## Purpose

Lets a single authenticated administrator register SSH hosts, retain their credentials across Hub restarts, verify server identity, and manage isolated remote connections from one browser workspace.

## Requirements

### Requirement: Persistent host registration

The hub SHALL provide authenticated operations to list, create, inspect, update and remove both local and SSH hosts. Every host SHALL have a stable identifier, display name, immutable connection type, configuration revision and connection revision. SSH hosts SHALL additionally have address, port, username and credential configuration; local hosts SHALL execute in the Hub environment without SSH settings. Management permissions SHALL NOT depend on a host identifier. The registry SHALL allow at most one active local connection registration and SHALL permit zero registered hosts. Accepted changes SHALL persist across restarts. Unknown identifiers SHALL be rejected without falling back to local execution. Concurrent edits based on a stale configuration revision SHALL be refused.

#### Scenario: Register and restart
- **WHEN** an administrator saves a remote host and restarts the hub
- **THEN** the host retains its identifier and connection configuration and can reconnect without re-entering credentials

#### Scenario: Rename a host
- **WHEN** an administrator changes only a host's display name
- **THEN** its identifier, file ownership and session identities remain unchanged

#### Scenario: Unknown host
- **WHEN** a caller targets a host identifier that is not registered
- **THEN** the operation returns host-not-found and performs no local or remote operation

#### Scenario: Stale edit
- **WHEN** a host update uses a revision superseded by another administrator browser
- **THEN** the update is refused without overwriting the newer configuration

#### Scenario: Local metadata persists
- **WHEN** an administrator renames a local host and restarts the Hub
- **THEN** its name and ID persist just as for an SSH host

#### Scenario: Local removal persists
- **WHEN** an administrator removes the local host and restarts the initialized Hub
- **THEN** that host remains absent and targeting its ID returns host-not-found

#### Scenario: Re-register local execution
- **WHEN** no local registration exists and an administrator adds a local connection
- **THEN** it becomes a managed host without SSH credentials, address or trust confirmation

#### Scenario: Duplicate local registration
- **WHEN** concurrent requests try to create local registrations
- **THEN** at most one succeeds and the other reports a conflict without a second local record

#### Scenario: Connection type cannot change in place
- **WHEN** a caller attempts to change an existing host's connection type
- **THEN** the update is refused and the existing registration remains unchanged

### Requirement: Single-administrator host authorization

The existing shared Bearer Token SHALL authorize all host administration and operations on all hosts. Unauthenticated callers SHALL NOT access host topology, probe endpoints, approve server keys, manage credentials or perform remote operations. Remote SSH authentication failure SHALL be reported as a host error rather than rejection of the central login.

#### Scenario: Unauthenticated probe
- **WHEN** a caller without valid central credentials attempts to probe an address
- **THEN** the hub refuses without contacting it

#### Scenario: Remote password rejected
- **WHEN** the central token is valid but a remote SSH server rejects its configured password
- **THEN** that host reports authentication failure and the browser remains logged in with access to other hosts

### Requirement: Persisted private credentials

The hub SHALL support password authentication, unencrypted private keys and passphrase-protected private keys. Saved secrets SHALL be encrypted with authenticated encryption using a persistent key independent of the browser token. Normal host responses SHALL expose only credential type and presence, never saved secret values. Secrets SHALL NOT appear in URLs, public settings, ordinary logs, process environments or browser persistent storage. Omitted replacement credentials SHALL retain existing credentials. Invalid credential input SHALL leave the saved record unchanged.

#### Scenario: Encrypted private key
- **WHEN** a host is saved with a supported encrypted private key and its correct passphrase
- **THEN** the hub can authenticate again after restart without another prompt

#### Scenario: Edit metadata only
- **WHEN** an administrator updates a host without supplying replacement credentials
- **THEN** the existing credential is retained and is not returned to the browser

#### Scenario: Missing decryption key
- **WHEN** existing encrypted state is present but its master key is missing or incorrect
- **THEN** the hub reports a startup error without generating a replacement key or rewriting the state

#### Scenario: Token changes
- **WHEN** the administrator changes the central Bearer Token and restarts
- **THEN** persisted SSH credentials remain decryptable with the existing master key

### Requirement: Explicit SSH server trust

The hub SHALL require explicit administrator confirmation of an observed server key before sending authentication credentials to that endpoint. Confirmation SHALL bind the exact endpoint and observed key to the current draft or host revision and expire if not used promptly. Every connection SHALL verify the pinned key. A changed key SHALL block automatic authentication and require explicit replacement confirmation. An endpoint edit SHALL invalidate trust for the previous endpoint.

#### Scenario: First connection
- **WHEN** an administrator probes a previously untrusted endpoint
- **THEN** the hub presents the key fingerprint and does not send SSH authentication credentials before explicit confirmation

#### Scenario: Key changes between probe and connect
- **WHEN** a connection presents a key different from the administrator-confirmed key
- **THEN** the hub refuses authentication and reports a server identity mismatch

#### Scenario: Old confirmation targets an edited address
- **WHEN** a trust confirmation belongs to an earlier host address or revision
- **THEN** it is rejected and does not authorize the new endpoint

### Requirement: Independent host capabilities and availability

The hub SHALL report execution access, tmux and file functionality for every host through bounded checks of its actual configured execution environment. Network, trust, authentication and SFTP details SHALL be reported where applicable. Status SHALL use one structured representation distinguishing available, unavailable, unknown and not-applicable, with observation freshness; local SSH and SFTP status SHALL be not-applicable, not successful. A remote host SHALL need no VTC agent. Missing SFTP SHALL disable file operations without disabling working tmux operations, and missing tmux SHALL NOT hide available SFTP. Status SHALL distinguish stale or untested observations from current successful results. Listing registered hosts SHALL NOT wait for all remote probes.

#### Scenario: One host is unreachable
- **WHEN** one registered host times out while another is available
- **THEN** the available host remains usable and the unreachable host remains registered with a distinct failure state

#### Scenario: SFTP unavailable
- **WHEN** SSH and tmux work but SFTP is disabled
- **THEN** terminal operations remain available and file actions report the missing capability

#### Scenario: Local capability check
- **WHEN** an administrator tests a local host
- **THEN** results reflect actual tmux and file configuration, and SSH/SFTP are shown as not applicable

#### Scenario: File policy is preserved
- **WHEN** files are disabled or local roots are configured
- **THEN** capability reporting reflects those settings without weakening local confinement or asserting local guarantees for SFTP

#### Scenario: Untested host
- **WHEN** a registered host has no current capability observation
- **THEN** it reports unknown or stale status rather than claiming success

### Requirement: Host connection lifecycle

Connection establishment, remote work, retries and shutdown SHALL have finite budgets and bounded concurrency. Recovery SHALL NOT automatically replay mutations or terminal input. A mutation whose acknowledgement is lost after dispatch SHALL be reported as outcome-unknown. Updating connection-affecting configuration SHALL invalidate old tickets and connections; already dispatched operations SHALL NOT be silently redirected or represented as rolled back. Removing a host SHALL stop future operations and close its connections without terminating its tmux sessions.

Metadata-only edits SHALL preserve connection revision and active terminal/file work. Removal and connection invalidation SHALL apply equally to local and SSH hosts. Reusing a removed ID SHALL NOT make tickets, runtime leases, responses or file observations from the previous registration valid again, including after restart. Removing the default host SHALL atomically clear the default setting.

#### Scenario: Lost mutation acknowledgement
- **WHEN** the connection fails after a rename was sent but before its result is known
- **THEN** the hub reports an uncertain outcome and does not automatically issue the rename again

#### Scenario: Remove connected host
- **WHEN** the administrator removes a host with attached terminals
- **THEN** the hub disconnects those attachments and removes its saved credentials without issuing session-kill commands

#### Scenario: Configuration changes during a request
- **WHEN** an address or credential edit succeeds while older work is in flight
- **THEN** new work uses the new revision and old work cannot switch destinations or publish results as belonging to that revision

#### Scenario: Rename does not disconnect
- **WHEN** a local or SSH host with an active terminal is renamed
- **THEN** its record revision changes but its connection revision and attachment remain valid

#### Scenario: Remove local attachment
- **WHEN** an administrator removes a local host with an attached terminal
- **THEN** the attachment closes and new operations are refused without terminating its tmux session

#### Scenario: Reused identity
- **WHEN** a removed host ID is registered again, including after a Hub restart
- **THEN** old tickets and runtime observations remain invalid and cannot authorize the new registration

### Requirement: Explicit registered operation targets

Session, terminal and file operations SHALL resolve an explicit registered host before execution. Missing targets SHALL be rejected by general operation interfaces and unknown targets SHALL return host-not-found without execution. An existing compatibility entry point that implicitly identifies the historical local host SHALL map only to that identity and perform normal registration checks; it SHALL NOT map to the default host or recreate a removed host.

#### Scenario: Missing operation target
- **WHEN** a caller omits a target on a general host-aware operation
- **THEN** the request is rejected without performing local or remote work

#### Scenario: Legacy local link after removal
- **WHEN** a compatibility request refers to the historical local identity after its removal
- **THEN** it returns host-not-found even if another host is the default

### Requirement: Persistent independent default host

An authenticated administrator SHALL be able to select any registered host as the Hub-wide default or clear the selection. This setting SHALL persist across restarts, use concurrency protection and SHALL NOT change any host's connection identity. Invalid or removed IDs SHALL be rejected. Host lists and default settings SHALL remain unavailable through public client configuration.

#### Scenario: Remote default survives restart
- **WHEN** an administrator selects an SSH host as default and restarts the Hub
- **THEN** authenticated settings retain that selection without reconnecting existing sessions solely because of the settings edit

#### Scenario: Default host removed
- **WHEN** the selected default host is removed
- **THEN** the default becomes unset atomically and no replacement is selected automatically

#### Scenario: Conflicting default updates
- **WHEN** a caller updates default selection using an obsolete settings revision
- **THEN** the update is rejected without overwriting the newer choice
