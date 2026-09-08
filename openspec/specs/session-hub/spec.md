# session-hub Specification

## Purpose
The session-hub capability is the server side of the product: it serves the browser client, authenticates callers, answers tmux session queries, applies session mutations, and bridges a browser connection to a live attached tmux session as a byte-faithful, correctly-sized terminal stream.

## Requirements

### Requirement: Session listing

The hub SHALL expose an operation that returns the tmux sessions currently present on the tmux server it manages. Each returned session SHALL carry at least its name, its window count, its attached state, and its creation time.

#### Scenario: Sessions exist

- **WHEN** a caller requests the session list and the tmux server has one or more sessions
- **THEN** the hub returns one entry per session with its name, window count, attached state, and creation time

#### Scenario: No sessions exist

- **WHEN** a caller requests the session list and the tmux server has no sessions
- **THEN** the hub returns an empty list and reports success, not an error

#### Scenario: No tmux server is running

- **WHEN** a caller requests the session list and no tmux server is running
- **THEN** the hub returns an empty list and reports success, treating "no server" as "no sessions"

#### Scenario: The tmux binary is unavailable

- **WHEN** a caller requests the session list and no tmux executable can be found
- **THEN** the hub returns an error identifying that tmux could not be found, distinguishable by the caller from an empty session list

### Requirement: Session creation

The hub SHALL expose an operation that creates a new detached tmux session. The caller MAY supply a name; when no name is supplied the hub SHALL generate a unique one. A created session SHALL start in the invoking user's home directory.

A caller-supplied name SHALL be valid when all of the following hold: it is non-empty; it is valid UTF-8 containing no control characters; it contains neither `:` nor `.` (which tmux's own name and target syntax reserves) nor their full-width lookalikes `：` and `．` (which render identically to the reserved characters); it has no leading or trailing whitespace; and it is at most 64 characters long, counted in code points. The hub SHALL accept any valid name, including names outside ASCII such as CJK text.

#### Scenario: Create with an explicit name

- **WHEN** a caller requests session creation with a name that is valid and not in use
- **THEN** the hub creates a detached session with that name and returns the created session's details

#### Scenario: Create with a non-ASCII name

- **WHEN** a caller requests session creation with a CJK name that satisfies the validity rules
- **THEN** the hub creates the session and subsequent session listings report that name verbatim

#### Scenario: Create without a name

- **WHEN** a caller requests session creation without supplying a name
- **THEN** the hub generates a unique name, creates the session, and returns the created session's details including the generated name

#### Scenario: Auto-generated name collides

- **WHEN** two nameless creations would resolve to the same generated name
- **THEN** the hub still creates two distinct uniquely-named sessions rather than reporting a name conflict

#### Scenario: Name already in use

- **WHEN** a caller requests session creation with a name that already exists
- **THEN** the hub does not create or modify any session and returns an error identifying the name as already in use

#### Scenario: Name is rejected

- **WHEN** a caller requests session creation with a name that contains a control character, `:` or `.` or their full-width lookalikes, leading or trailing whitespace, or that exceeds 64 code points
- **THEN** the hub does not invoke tmux and returns a validation error naming the constraint that was violated

### Requirement: Session renaming

The hub SHALL expose an operation that renames an existing tmux session, subject to the same name validation as session creation.

#### Scenario: Rename succeeds

- **WHEN** a caller requests that an existing session be renamed to a valid, unused name
- **THEN** the hub renames the session and subsequent session listings report the new name

#### Scenario: Rename to a non-ASCII name

- **WHEN** a caller requests that an existing session be renamed to a CJK name that satisfies the validity rules
- **THEN** the hub renames the session and subsequent session listings report the new name verbatim

#### Scenario: Target session is absent

- **WHEN** a caller requests a rename of a session name that does not exist
- **THEN** the hub returns a not-found error and no session is modified

#### Scenario: New name is already in use

- **WHEN** a caller requests a rename to a name held by a different session
- **THEN** the hub returns an error identifying the conflict and no session is modified

### Requirement: Session termination

The hub SHALL expose an operation that terminates an existing tmux session, destroying its windows and panes.

#### Scenario: Kill succeeds

- **WHEN** a caller requests termination of an existing session
- **THEN** the hub terminates it and subsequent session listings omit it

#### Scenario: Target session is absent

- **WHEN** a caller requests termination of a session name that does not exist
- **THEN** the hub returns a not-found error

#### Scenario: Session has connected terminals

- **WHEN** a session is terminated while browser terminals are attached to it
- **THEN** each attached terminal is informed that its session ended and its connection is closed

### Requirement: Exact session targeting

The hub SHALL target tmux sessions by exact name. A caller-supplied name SHALL NOT be interpreted by tmux as a prefix, pattern, or partial match against other session names.

#### Scenario: A name is a prefix of another session's name

- **WHEN** sessions named `api` and `api-staging` both exist and a caller targets `api`
- **THEN** the operation applies to `api` only and never to `api-staging`

### Requirement: Shell-injection-safe tmux invocation

The hub SHALL invoke tmux by passing an argument vector directly to the operating system, never by constructing a command string interpreted by a shell.

#### Scenario: Name contains shell metacharacters

- **WHEN** a caller supplies a session name containing shell metacharacters such as `;`, `$`, backticks, or quotes
- **THEN** the name reaches tmux as one argument with no shell interpretation of it ever occurring

### Requirement: Terminal attachment

The hub SHALL expose a bidirectional streaming connection that attaches a caller to a named tmux session, forwarding the session's terminal output to the caller and the caller's input to the session.

#### Scenario: Attach to an existing session

- **WHEN** a caller opens a terminal connection for an existing session with an initial size
- **THEN** the hub attaches to that session at the requested size and begins forwarding its terminal output

#### Scenario: Attach to an absent session

- **WHEN** a caller opens a terminal connection for a session that does not exist
- **THEN** the hub reports the failure over the connection and closes it, without creating a session

#### Scenario: Multiple concurrent attachments

- **WHEN** two callers attach to the same session at the same time
- **THEN** both receive that session's output and either may send input, matching the behavior of two conventionally attached tmux clients

### Requirement: Byte-faithful terminal output

The hub SHALL forward terminal output to the caller exactly as produced, preserving every byte including escape sequences, color and attribute codes, and multi-byte character encodings. The hub SHALL NOT reinterpret, reflow, line-join, transcode, or strip terminal output.

#### Scenario: Colored and styled output

- **WHEN** a program in the attached session emits color and text-attribute escape sequences
- **THEN** the caller receives those sequences intact and the rendered terminal shows the same colors and attributes as a conventionally attached tmux client

#### Scenario: A multi-byte character is split across reads

- **WHEN** a multi-byte character's bytes arrive in two separate reads from the terminal
- **THEN** the hub forwards the bytes without alteration such that the caller reassembles the original character

#### Scenario: Full-screen application

- **WHEN** a full-screen terminal application runs in the attached session
- **THEN** the caller receives its cursor-positioning and screen-clearing sequences intact and renders it as a conventionally attached tmux client would

### Requirement: Terminal size negotiation

The hub SHALL apply a caller-supplied terminal size to the attached session's terminal at attach time, and SHALL apply subsequent size changes for the lifetime of the connection, so that tmux lays out its own content — including any splits it is displaying — to the caller's actual viewport.

#### Scenario: Initial size applied at attach

- **WHEN** a caller attaches with a given column and row count
- **THEN** the hub sets the attached terminal to that size before or as it begins streaming, and tmux wraps and lays out content to that size

#### Scenario: Caller resizes

- **WHEN** an attached caller reports a new column and row count
- **THEN** the hub applies the new size to the terminal and tmux reflows its content, including redistributing space among its own splits

#### Scenario: Size is out of range

- **WHEN** a caller reports a size whose dimensions are non-positive or implausibly large
- **THEN** the hub rejects that size, retains the last valid size, and keeps the connection usable

#### Scenario: Repaint after attach

- **WHEN** a caller attaches and the session's existing terminal size already equals the requested size
- **THEN** the hub still causes tmux to repaint the screen, so the caller is not left with a blank view waiting for the next output

#### Scenario: Two callers of differing sizes

- **WHEN** two callers are attached to one session with different viewport sizes
- **THEN** the session's layout follows the most recently active caller's size rather than being constrained to the smallest attached size

### Requirement: Authenticated access

The hub SHALL require every caller to present valid credentials before it will list, create, rename, or terminate sessions, or attach a terminal. The hub SHALL NOT expose any tmux state or accept any tmux operation from an unauthenticated caller.

#### Scenario: Missing or invalid credentials

- **WHEN** a caller invokes any session operation without credentials, or with credentials that do not match
- **THEN** the hub refuses the operation with an authentication error and performs no tmux invocation

#### Scenario: Credential comparison

- **WHEN** the hub compares a presented credential against the expected value
- **THEN** the comparison is performed in a way that does not reveal the expected value through timing differences

#### Scenario: No credential is configured

- **WHEN** the hub starts without a credential configured
- **THEN** it either generates one and surfaces it to the operator, or refuses to start — it never serves unauthenticated callers

### Requirement: Startup credential disclosure

The hub SHALL print its effective access credential to the operator at startup, whether the credential was generated by the hub or loaded from the environment, so the operator can always authenticate a browser client. The startup output SHALL also state the address the browser should open.

#### Scenario: Credential comes from the environment

- **WHEN** the hub starts with a credential provided in its environment
- **THEN** it prints that credential in its startup output before serving requests

#### Scenario: Credential is generated

- **WHEN** the hub starts without a credential provided
- **THEN** it generates one and prints it in its startup output before serving requests

#### Scenario: Startup output names the listen address

- **WHEN** the hub starts
- **THEN** its startup output includes the address on which it is listening

### Requirement: Terminal connection authorization by single-use ticket

The hub SHALL authorize terminal attachment using a short-lived, single-use ticket bound to the specific session being attached, issued only to an already-authenticated caller. Long-lived credentials SHALL NOT be required or accepted in the terminal connection's address.

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

### Requirement: Cross-origin connection rejection

The hub SHALL reject terminal connections whose declared origin is not one it is configured to serve, so that a page on an unrelated site cannot open a terminal on the user's behalf.

#### Scenario: Foreign origin

- **WHEN** a terminal connection arrives declaring an origin the hub is not configured to serve
- **THEN** the hub refuses the connection before attaching to any session

### Requirement: Output backpressure

The hub SHALL avoid unbounded memory growth when a caller consumes terminal output more slowly than the session produces it, by pausing its reading of session output while the caller is behind and resuming once the caller has caught up.

#### Scenario: Caller falls behind

- **WHEN** the volume of undelivered output for a caller exceeds the hub's threshold
- **THEN** the hub stops reading further session output until the backlog drains, rather than buffering without limit

#### Scenario: Caller catches up

- **WHEN** a previously-behind caller's backlog drains below the resume threshold
- **THEN** the hub resumes reading session output and delivery continues

### Requirement: Bounded pre-ready output staging

The hub SHALL retain terminal output produced before the caller's connection is ready to receive it, up to a fixed bound, and deliver it once the connection is ready. When the bound is exceeded the hub SHALL discard the oldest retained output, and SHALL do so in whole output units so that no partial escape sequence or partial multi-byte character is ever delivered.

#### Scenario: Output arrives before ready

- **WHEN** the session produces output between attachment and the connection becoming ready
- **THEN** that output is delivered to the caller once the connection is ready, in the order produced

#### Scenario: Staged output exceeds the bound

- **WHEN** more output accumulates before ready than the bound permits
- **THEN** the hub discards whole oldest units to stay within the bound and never delivers a truncated escape sequence or truncated character

### Requirement: Session end notification

The hub SHALL inform an attached caller when its session ends or its attachment terminates, distinguishing an ordinary end from a failure.

#### Scenario: Session is terminated

- **WHEN** the attached session is terminated
- **THEN** the hub informs the caller that the session ended and closes the connection

#### Scenario: Attachment fails

- **WHEN** the attachment fails after being established, for a reason other than the session ending normally
- **THEN** the hub informs the caller of the failure in a way the caller can distinguish from a normal end, and closes the connection signalling that a retry is appropriate

### Requirement: Secret isolation from session processes

The hub SHALL NOT expose its own credentials or configuration secrets in the environment of any process it starts for a terminal attachment.

#### Scenario: Attached session inspects its environment

- **WHEN** a program running in an attached session reads its environment
- **THEN** it finds none of the hub's credentials or secrets

### Requirement: Loopback-by-default network exposure

The hub SHALL bind to the loopback interface unless explicitly configured otherwise, so that installing it does not expose a terminal to the local network by default.

#### Scenario: Default start

- **WHEN** the hub starts with no network binding configured
- **THEN** it accepts connections only from the local machine

### Requirement: Graceful shutdown

The hub SHALL shut down in bounded time on an operating-system termination signal, closing caller connections and terminating the processes it started for attachments, without leaving them orphaned.

#### Scenario: Termination signal with active attachments

- **WHEN** the hub receives a termination signal while terminals are attached
- **THEN** it closes those connections, terminates the processes it started for them, and exits within a bounded time even if a component fails to close

### Requirement: tmux session persistence across hub restarts

The hub SHALL NOT own the lifetime of tmux sessions. Sessions SHALL survive hub restarts and caller disconnections, and remain listable and attachable afterwards.

#### Scenario: Hub restarts

- **WHEN** the hub is stopped and started again while tmux sessions exist
- **THEN** those sessions are still present, listable, and attachable, with their contents intact
