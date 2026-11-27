# Spec Delta

## Purpose

Routes the application's connections to remote hosts through one user-configured SOCKS5 proxy, so that remote workspaces stay reachable on networks that block direct SSH.

## ADDED Requirements

### Requirement: One proxy setting for remote connections

The application SHALL provide a setting for a single SOCKS5 proxy address (`host:port`). When it is set, every connection to a remote host SHALL be established through that proxy. When it is empty, remote connections SHALL be established directly. Connections to the local tmux server SHALL NOT use the proxy.

#### Scenario: No proxy configured

- **WHEN** the proxy setting is empty and the user connects to a remote host
- **THEN** the connection is established directly to the host's configured address and port, as before this change

#### Scenario: The local host is reached without the proxy

- **WHEN** a proxy is configured and the user works with the local host
- **THEN** its sessions, terminals and files are reached without the proxy, exactly as they are without one

#### Scenario: Invalid address is rejected

- **WHEN** the user saves a proxy address that is not a valid `host:port` value or names an invalid port
- **THEN** the setting is refused with an explanation and the previous setting is kept

### Requirement: New local terminal sessions carry the proxy

When a proxy is configured, every local terminal session the application creates SHALL receive the standard proxy environment variables naming that proxy, so that commands run in the terminal use it. The proxy address SHALL be read when the session is created. Sessions on remote hosts SHALL NOT receive these variables from the application.

#### Scenario: New local terminal after setting a proxy

- **WHEN** the user saves a proxy address and then creates a local terminal session
- **THEN** commands in that session, and in windows opened in it later, find the standard proxy variables `http_proxy`, `https_proxy` and `all_proxy` in lower and in upper case, each naming that address as a SOCKS5 proxy that resolves names, and an exclusion list `no_proxy` and `NO_PROXY` holding `localhost`, `127.0.0.1`, `::1` and names under `.local`

#### Scenario: A command in the terminal uses the proxy

- **WHEN** the user runs a program in such a session that honors these variables, such as `git` over HTTPS or `curl`
- **THEN** it connects through the proxy, except for the excluded local addresses

#### Scenario: Session created earlier is unchanged

- **WHEN** a local session was created before the proxy was set, or with a different address
- **THEN** its environment is not modified

#### Scenario: Proxy cleared or changed

- **WHEN** the user clears or changes the proxy address
- **THEN** local sessions created afterwards carry no proxy variables, or name the new address, and existing sessions are unchanged

#### Scenario: No proxy configured

- **WHEN** no proxy is configured and the user creates a local terminal session
- **THEN** the application adds no proxy variables to it

#### Scenario: Remote sessions are left alone

- **WHEN** the user creates a session on a remote host while a proxy is configured
- **THEN** the application adds no proxy variables to that session

#### Scenario: The setting cannot be read

- **WHEN** a local terminal session is created and the proxy setting cannot be read
- **THEN** the session is not created and the error is reported

### Requirement: The settings dialog explains the terminal variables

The settings dialog SHALL say that new local terminal sessions use the proxy. Whenever a proxy address is entered, it SHALL also say that variables set in the shell's own configuration take precedence.

#### Scenario: An address is entered

- **WHEN** the proxy address field holds an address
- **THEN** the dialog shows the hint about the shell's own configuration

#### Scenario: The field is empty

- **WHEN** the proxy address field is empty
- **THEN** the hint is not shown

#### Scenario: The user's own configuration wins

- **WHEN** the user's shell configuration sets one of these variables
- **THEN** that value is in effect in the terminal

### Requirement: Every remote connection path uses the proxy

With a proxy configured, host probing, fingerprint confirmation and trust repair, remote terminals, remote file operations and every reconnection SHALL be established through the proxy. The target host name SHALL be passed to the proxy for resolution instead of being resolved on the local machine.

#### Scenario: Probe before a host is saved

- **WHEN** the user tests the connection of a host that is not saved yet
- **THEN** the probe reaches the target through the configured proxy and presents the same server fingerprint as a direct probe would

#### Scenario: Terminal, files and reconnection

- **WHEN** the user opens a remote terminal, browses or edits remote files, and the connection is lost and re-established
- **THEN** each of those connections is established through the configured proxy

#### Scenario: Host name the local machine cannot resolve

- **WHEN** a remote host is registered under a host name that the local machine cannot resolve but the proxy can
- **THEN** the connection through the proxy succeeds

### Requirement: A configured proxy is never bypassed

When a proxy is configured and cannot be used, the application SHALL fail the connection and SHALL NOT retry it as a direct connection.

#### Scenario: Proxy is not running

- **WHEN** a proxy is configured and nothing is listening at its address, and the user connects to a remote host
- **THEN** the connection attempt is limited to that proxy, fails within the existing connection budget and no direct connection to the target host is attempted

#### Scenario: Proxy stops accepting connections

- **WHEN** a remote connection through the proxy is lost and the proxy is no longer reachable
- **THEN** reconnection attempts use the proxy only and the user is told that the proxy is unreachable

### Requirement: Persistent proxy setting

The proxy setting SHALL be stored with the application's durable host state, persist across restarts and be shared by the desktop application and server mode that use the same state. Changing it SHALL require the current settings revision, and a change based on a stale revision SHALL be refused. The setting SHALL contain no credential.

#### Scenario: Setting survives a restart

- **WHEN** the user saves a proxy address, quits and relaunches the application
- **THEN** the same proxy address is active and shown in the settings

#### Scenario: Stale edit is refused

- **WHEN** a proxy change is submitted with a settings revision older than the current one
- **THEN** the change is refused and the current setting is kept

#### Scenario: Change does not interrupt active work

- **WHEN** the user changes or clears the proxy address while a remote terminal or file panel is open
- **THEN** existing connections stay attached and the new value applies to connections established afterwards

### Requirement: Server identity is still verified end to end

Remote server key verification SHALL be performed between the application and the target host and SHALL NOT be delegated to the proxy. Routing through a proxy SHALL NOT make an unconfirmed or changed server key trusted.

#### Scenario: First connection through a proxy

- **WHEN** the user connects to a remote host through a proxy whose server key has not been confirmed
- **THEN** the application presents the key fingerprint and sends no SSH credentials before explicit confirmation

#### Scenario: Key changed behind a proxy

- **WHEN** a previously confirmed remote host presents a different server key through the proxy
- **THEN** the application refuses authentication and reports a server identity mismatch, as it does for a direct connection

### Requirement: Connection path is visible

The application SHALL show in its settings whether remote connections currently use a proxy or go directly, and SHALL show the configured proxy address in proxy-related connection errors.

#### Scenario: Setting reflects the active path

- **WHEN** the user opens the settings
- **THEN** they show the configured proxy address, or clearly indicate that remote connections go directly when none is set

#### Scenario: Error names the proxy

- **WHEN** a remote connection fails because of the configured proxy
- **THEN** the reported error names that proxy address so the user can tell a proxy problem from a host problem
