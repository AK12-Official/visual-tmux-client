## MODIFIED Requirements

### Requirement: Configurable operational behavior

YAML SHALL configure HTTP address, header and idle timeouts, API request-body limit, bearer token, ticket lifetime and cleanup interval, tmux executable path, WebSocket origin, input-message limit and write timeouts, terminal dimension limit, pre-ready staging capacity, output watermarks and backpressure polling interval, attachment and HTTP shutdown timeouts. YAML SHALL also configure browser session polling, activity decay and throttling, resize debounce, reconnect delays, terminal scrollback and font defaults/range, and toast lifetimes and stack bound. YAML SHALL also configure the file manager's enablement, its local-host root directories, the maximum size of a single file that may be read or written, and the maximum number of directory entries returned per listing. Defaults SHALL preserve existing values. Configuration SHALL NOT disable authentication, ticket single-use binding, secret scrubbing, byte-faithful forwarding, exact session targeting, mouse support or session persistence. When local root directories are configured, configuration SHALL NOT permit local file access outside them. Remote file access follows the remote account boundary, not these local roots.

#### Scenario: Non-default ticket and input limits
- **WHEN** an operator configures a shorter ticket lifetime and a different WebSocket message limit
- **THEN** ticket expiry uses that lifetime and inbound message enforcement uses that limit

#### Scenario: Non-default output buffering
- **WHEN** a slow client reaches the configured output high watermark
- **THEN** reading pauses and resumes after draining below the configured low watermark, without unbounded buffering or discarding live output

#### Scenario: Notification configuration
- **WHEN** configured notification lifetimes and a stack bound differ from defaults
- **THEN** browser notifications expire by the configured level-specific lifetime and drop the oldest notification when the bound is exceeded

#### Scenario: Non-default file limits
- **WHEN** an operator configures a smaller per-file size limit and a different directory listing limit
- **THEN** reads, writes, and downloads beyond that size are refused and directory listings return at most that many entries

#### Scenario: File manager disabled
- **WHEN** an operator disables the file manager
- **THEN** file operations are refused without reading the filesystem, while terminal and session operations continue to work

### Requirement: File access boundary configuration

For the built-in local host, YAML SHALL configure whether file operations are restricted to a set of root directories. When no root is configured, the boundary SHALL be the access the hub's own operating-system user already has, so that file operations are not confined to any directory. When one or more roots are configured, they SHALL replace that default and every operation SHALL be confined to them. The hub SHALL resolve each configured root to its canonical path at startup and SHALL reject at startup any root that does not exist, is not a directory, or cannot be resolved, while the file capability is enabled. Configuring the filesystem root SHALL be permitted.

A hub whose file capability is disabled SHALL resolve no root and open none: a root that cannot be used is not a boundary for a capability that is off, so it SHALL NOT stop the hub, and the operations that have nothing to do with files SHALL be unaffected by it. The same configuration SHALL still be refused by a hub that has the capability enabled.

#### Scenario: No roots configured
- **WHEN** the configuration omits the file root directories
- **THEN** the hub permits file operations on any regular path its own operating-system user can access, including paths outside the user's home directory

#### Scenario: Roots configured
- **WHEN** an operator configures one or more root directories
- **THEN** the hub permits file operations only within those directories and refuses paths outside all of them

#### Scenario: Root does not exist
- **WHEN** a configured root does not exist or is not a directory, and the file capability is enabled
- **THEN** the hub refuses to start and identifies the offending root

#### Scenario: Root does not exist while the file capability is disabled
- **WHEN** a configured root does not exist and the file capability is disabled
- **THEN** the hub starts, resolves no root, and its terminal and session operations are unaffected

#### Scenario: Root resolves through a symlink
- **WHEN** a configured root is a symbolic link to a directory
- **THEN** the hub stores its canonical resolution and containment is decided against that canonical path

#### Scenario: Filesystem root is configured explicitly
- **WHEN** an operator deliberately configures `/` as a root
- **THEN** the hub starts and permits file operations throughout the filesystem except the always-rejected `/proc`, `/sys`, and `/dev`

#### Scenario: Remote host with local roots configured

- **WHEN** a remote host is accessed while local roots are configured
- **THEN** local root paths are not applied to the remote filesystem, whose boundary is the remote account and server policy

## ADDED Requirements

### Requirement: Persistent host state configuration

Configuration SHALL support durable host-state and master-key file locations with documented OS-user defaults and configuration-relative path resolution. A fresh default local-only run SHALL remain usable without initialized host storage; first enrollment SHALL initialize it or report a persistence error. Explicit invalid storage configuration, corrupt existing state, unsupported schemas and missing keys for existing encrypted state SHALL fail startup without replacing data. State and key files SHALL be private to the Hub user. Only one Hub process SHALL own a state store at a time. Documentation SHALL describe backup/restore of both state and key, and persistent mounts for container deployments.

#### Scenario: First enrollment
- **WHEN** a default local-only installation saves its first remote host
- **THEN** the hub creates private durable state and key material or rejects the save clearly without claiming persistence

#### Scenario: Read-only fresh local deployment
- **WHEN** a fresh local-only deployment has no writable default state directory
- **THEN** local operations remain usable and host enrollment reports that durable storage is unavailable

#### Scenario: Restore registry without key
- **WHEN** an operator restores only the encrypted registry
- **THEN** startup identifies the missing key and leaves the registry untouched

#### Scenario: Second Hub using the same state
- **WHEN** another Hub already owns the state store
- **THEN** startup refuses that store instead of creating competing connection and credential state

### Requirement: Bounded remote operational configuration

Configuration SHALL provide positive bounds for SSH dial and handshake time, remote operation time, retry backoff and remote connection concurrency. Existing local defaults and configuration precedence SHALL remain compatible. Global file enablement and size/list limits SHALL apply to both backends, while local roots SHALL remain local. Host topology, server fingerprints and capabilities SHALL require authentication and SHALL NOT be added to public client configuration.

#### Scenario: Invalid remote timeout
- **WHEN** an operator configures a non-positive SSH timeout
- **THEN** configuration is rejected with a field-specific error without credential values

#### Scenario: Public configuration with saved hosts
- **WHEN** an unauthenticated caller reads client configuration on a hub with saved remote hosts
- **THEN** the response contains neither host records nor credentials, and authenticated host capability discovery remains separate
