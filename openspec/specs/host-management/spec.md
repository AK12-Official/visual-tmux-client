# host-management Specification

## Purpose

Lets a single authenticated administrator register SSH hosts, retain their credentials across Hub restarts, verify server identity, and manage isolated remote connections from one browser workspace.

## Requirements

### Requirement: Persistent host registration

The hub SHALL provide authenticated operations to list, create, inspect, update and remove remote hosts. A host SHALL have a stable identifier, display name, address, port, username and credential type. The built-in `local` host SHALL remain available and SHALL NOT be removed through host administration. Accepted changes SHALL persist across restarts. Unknown identifiers SHALL be rejected without falling back to local execution. Concurrent edits based on a stale configuration revision SHALL be refused.

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

The hub SHALL report network, trust, authentication, tmux and SFTP outcomes separately with bounded probes. A remote host SHALL need no VTC agent. Missing SFTP SHALL disable file operations without disabling working tmux operations, and missing tmux SHALL NOT hide available SFTP. Status SHALL distinguish stale or untested observations from current successful results. Listing registered hosts SHALL NOT wait for all remote probes.

#### Scenario: One host is unreachable
- **WHEN** one registered host times out while another is available
- **THEN** the available host remains usable and the unreachable host remains registered with a distinct failure state

#### Scenario: SFTP unavailable
- **WHEN** SSH and tmux work but SFTP is disabled
- **THEN** terminal operations remain available and file actions report the missing capability

### Requirement: Host connection lifecycle

Connection establishment, remote work, retries and shutdown SHALL have finite budgets and bounded concurrency. Recovery SHALL NOT automatically replay mutations or terminal input. A mutation whose acknowledgement is lost after dispatch SHALL be reported as outcome-unknown. Updating connection-affecting configuration SHALL invalidate old tickets and connections; already dispatched operations SHALL NOT be silently redirected or represented as rolled back. Removing a host SHALL stop future operations and close its connections without terminating its tmux sessions.

#### Scenario: Lost mutation acknowledgement
- **WHEN** the connection fails after a rename was sent but before its result is known
- **THEN** the hub reports an uncertain outcome and does not automatically issue the rename again

#### Scenario: Remove connected host
- **WHEN** the administrator removes a host with attached terminals
- **THEN** the hub disconnects those attachments and removes its saved credentials without issuing session-kill commands

#### Scenario: Configuration changes during a request
- **WHEN** an address or credential edit succeeds while older work is in flight
- **THEN** new work uses the new revision and old work cannot switch destinations or publish results as belonging to that revision
