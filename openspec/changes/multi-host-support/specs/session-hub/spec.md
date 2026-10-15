## MODIFIED Requirements

### Requirement: Shell-injection-safe tmux invocation

For local operations the hub SHALL invoke tmux by passing an argument vector directly to the operating system, never constructing a shell command. For remote SSH operations the hub SHALL encode every variable argument for the supported remote command shell so that it reaches tmux literally, without shell expansion or command injection. Unsupported remote command environments SHALL produce a compatibility error rather than use unverified command encoding.

#### Scenario: Name contains shell metacharacters

- **WHEN** a caller supplies a session name containing shell metacharacters such as '$', backticks, '&', or quotes
- **THEN** the name reaches tmux as one argument with no shell interpretation of it ever occurring

#### Scenario: Remote shell metacharacters

- **WHEN** a remote session name contains legal quotes, dollar signs, backticks or Unicode
- **THEN** the operation targets that exact name and executes no additional shell command

### Requirement: Terminal connection authorization by single-use ticket

The hub SHALL authorize terminal attachment using a short-lived, single-use ticket bound to the specific host, connection-configuration revision and session being attached, issued only to an already-authenticated caller. Long-lived credentials SHALL NOT be required or accepted in the terminal connection's address.

#### Scenario: Ticket is redeemed

- **WHEN** an authenticated caller requests a ticket for a session and opens a terminal connection presenting it
- **THEN** the hub authorizes the attachment and invalidates the ticket

#### Scenario: Ticket is reused

- **WHEN** a caller presents a ticket that has already been redeemed
- **THEN** the hub refuses the connection

#### Scenario: Ticket has expired

- **WHEN** a caller presents a ticket issued longer ago than its lifetime allows
- **THEN** the hub refuses the connection

#### Scenario: Ticket is presented for a different session

- **WHEN** a caller presents a ticket that was issued for one session while attaching to another
- **THEN** the hub refuses the connection

#### Scenario: Same session name on another host

- **WHEN** a ticket issued for host A session dev is presented for host B session dev
- **THEN** the attachment is refused and the ticket cannot be reused

#### Scenario: Host configuration superseded

- **WHEN** a ticket is presented after its host connection configuration changed or the host was removed
- **THEN** the attachment is refused without connecting with either the old or new configuration

## ADDED Requirements

### Requirement: Host-scoped session operations

The hub SHALL resolve each session and terminal request to its explicit host. Existing listing, naming, exact targeting, pane summary, mouse configuration and session persistence behavior SHALL apply on that host. Remote creation SHALL use the SSH user's home rather than the Hub user's home. Local routes SHALL remain compatible. An unreachable host SHALL NOT be reported as an empty list or missing session.

#### Scenario: Identical names
- **WHEN** hosts A and B both have a session named dev and a caller terminates A's dev
- **THEN** only A's dev is terminated and B's dev remains untouched

#### Scenario: Remote host unavailable
- **WHEN** session lookup fails because SSH cannot connect
- **THEN** the hub reports host unavailability rather than session-not-found or an empty successful list

#### Scenario: Existing local caller
- **WHEN** a caller uses the existing local session and ticket routes
- **THEN** the request continues to operate on the local tmux server with existing behavior

### Requirement: SSH terminal lifecycle

Remote terminals SHALL provide byte-faithful duplex input/output, initial and subsequent PTY sizing, bounded backpressure, and distinguish remote session exit from transport failure. Disconnect, host removal and Hub shutdown SHALL release attachments without terminating remote tmux sessions. Reconnection SHALL create a fresh attachment to the same host/session and SHALL NOT replay past input.

#### Scenario: Resize remote terminal
- **WHEN** an attached browser terminal changes dimensions
- **THEN** the remote PTY receives the final dimensions and tmux renders to that size

#### Scenario: Disconnect and reconnect
- **WHEN** the browser connection drops while the remote session is running
- **THEN** the attachment is closed, the tmux session survives and a later connection can reattach without repeating old input

#### Scenario: SSH transport ends without exit status
- **WHEN** SSH is lost before a remote exit result is received
- **THEN** the hub reports a connection failure rather than claiming the session terminated
