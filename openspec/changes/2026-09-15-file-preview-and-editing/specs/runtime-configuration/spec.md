## MODIFIED Requirements

### Requirement: Configurable operational behavior

YAML SHALL configure HTTP address, header and idle timeouts, API request-body limit, bearer token, ticket lifetime and cleanup interval, tmux executable path, WebSocket origin, input-message limit and write timeouts, terminal dimension limit, pre-ready staging capacity, output watermarks and backpressure polling interval, attachment and HTTP shutdown timeouts. YAML SHALL also configure browser session polling, activity decay and throttling, resize debounce, reconnect delays, terminal scrollback and font defaults/range, and toast lifetimes and stack bound. YAML SHALL also configure the file manager's enablement, its root directories, the maximum size of a single file that may be read or written, and the maximum number of directory entries returned per listing. Defaults SHALL preserve existing values. Configuration SHALL NOT disable authentication, ticket single-use binding, secret scrubbing, byte-faithful forwarding, exact session targeting, mouse support or session persistence. When root directories are configured, configuration SHALL NOT permit file access outside them.

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

## ADDED Requirements

### Requirement: File access boundary configuration

YAML SHALL configure whether file operations are restricted to a set of root directories. When no root is configured, the boundary SHALL be the access the hub's own operating-system user already has, so that file operations are not confined to any directory. When one or more roots are configured, they SHALL replace that default and every operation SHALL be confined to them. The hub SHALL resolve each configured root to its canonical path at startup and SHALL reject at startup any root that does not exist, is not a directory, or cannot be resolved. Configuring the filesystem root SHALL be permitted.

#### Scenario: No roots configured
- **WHEN** the configuration omits the file root directories
- **THEN** the hub permits file operations on any regular path its own operating-system user can access, including paths outside the user's home directory

#### Scenario: Roots configured
- **WHEN** an operator configures one or more root directories
- **THEN** the hub permits file operations only within those directories and refuses paths outside all of them

#### Scenario: Root does not exist
- **WHEN** a configured root does not exist or is not a directory
- **THEN** the hub refuses to start and identifies the offending root

#### Scenario: Root resolves through a symlink
- **WHEN** a configured root is a symbolic link to a directory
- **THEN** the hub stores its canonical resolution and containment is decided against that canonical path

#### Scenario: Filesystem root is configured explicitly
- **WHEN** an operator deliberately configures `/` as a root
- **THEN** the hub starts and permits file operations throughout the filesystem except the always-rejected `/proc`, `/sys`, and `/dev`
