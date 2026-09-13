# runtime-configuration Specification

## Purpose

Allows operators to manage backend and browser runtime settings from one YAML file, with deterministic compatibility overrides, startup validation, and a public projection that keeps credentials private.

## Requirements

### Requirement: Central YAML configuration

The application SHALL accept `--config <path>` and a single YAML mapping with optional `version: 1`. It SHALL use `visual-tmux-client.yaml` in the current working directory when no path is supplied. Missing implicit files SHALL select built-in defaults; missing or unreadable explicit files and unreadable implicit files SHALL fail startup. Omitted fields SHALL retain their defaults. Relative file paths in YAML SHALL resolve against the configuration file directory. Configuration SHALL be loaded once per process start.

#### Scenario: No configuration file
- **WHEN** the application starts without an explicit path and the implicit file does not exist
- **THEN** it starts with existing defaults including loopback address, generated token, request-host Origin matching and PATH lookup for tmux

#### Scenario: Explicit partial configuration
- **WHEN** a file specifies only server.addr and a relative tmux.path
- **THEN** those values are used, the binary path resolves relative to that file, and all other fields retain defaults

#### Scenario: Missing explicit file
- **WHEN** the supplied --config path does not exist or cannot be read
- **THEN** startup fails before any listener or attachment starts and identifies the path

#### Scenario: Restart applies edits
- **WHEN** an operator changes YAML while the hub runs
- **THEN** the process retains its configuration snapshot and a subsequent restart loads the edits

### Requirement: Deterministic legacy overrides

Effective values SHALL use explicit CLI flags over present legacy environment variables over YAML over defaults. The application SHALL retain --addr, --help, --version, VISUAL_TMUX_CLIENT_TOKEN, VISUAL_TMUX_CLIENT_ORIGIN, and VISUAL_TMUX_CLIENT_TMUX_PATH. An explicitly empty environment value SHALL override YAML: empty token generates a credential, empty origin selects request-host matching, and empty tmux path selects PATH lookup. An omitted CLI flag SHALL NOT override YAML. Help and version SHALL work without reading configuration files. No new general-purpose environment override or YAML environment interpolation SHALL be required.

#### Scenario: CLI address is explicitly the default
- **WHEN** YAML selects another address and --addr explicitly supplies 127.0.0.1:7690
- **THEN** the explicit CLI value wins

#### Scenario: Environment absent versus empty
- **WHEN** YAML supplies a token and the token environment variable is absent
- **THEN** that token is used
- **AND** setting the environment variable to an empty string instead causes a new token to be generated

#### Scenario: Help with invalid configuration
- **WHEN** --help or --version is requested while the implicit YAML is invalid
- **THEN** the requested informational output succeeds without starting the server

### Requirement: Strict configuration validation

The application SHALL reject malformed YAML, multiple documents, duplicate or unknown keys, null values, unsupported versions, incorrect types, invalid addresses, invalid non-empty browser origins, unsafe credential header characters, non-positive durations and invalid limits before listening. Errors SHALL identify the file and field where applicable without disclosing credential values. Explicit numeric zero SHALL NOT be treated as omission. Durations SHALL be unit-bearing strings; byte limits SHALL be integers. Cross-field validation SHALL require low water below high water, staging capacity at least one PTY read chunk, reconnect initial delay no greater than maximum, and ordered font bounds containing the default.

#### Scenario: Typo and duplicate key
- **WHEN** a file contains an unknown field or duplicate mapping key
- **THEN** startup fails rather than silently ignoring or selecting one value

#### Scenario: Invalid operational relationship
- **WHEN** output low water is at least high water, staging capacity is below one read chunk, or font default is outside its bounds
- **THEN** startup fails and identifies the conflicting settings

#### Scenario: Invalid secret
- **WHEN** a configured token contains a newline
- **THEN** startup fails with a field-specific error that does not echo the token

### Requirement: Configurable operational behavior

YAML SHALL configure HTTP address, header and idle timeouts, API request-body limit, bearer token, ticket lifetime and cleanup interval, tmux executable path, WebSocket origin, input-message limit and write timeouts, terminal dimension limit, pre-ready staging capacity, output watermarks and backpressure polling interval, attachment and HTTP shutdown timeouts. YAML SHALL also configure browser session polling, activity decay and throttling, resize debounce, reconnect delays, terminal scrollback and font defaults/range, and toast lifetimes and stack bound. Defaults SHALL preserve existing values. Configuration SHALL NOT disable authentication, ticket single-use binding, secret scrubbing, byte-faithful forwarding, exact session targeting, mouse support or session persistence.

#### Scenario: Non-default ticket and input limits
- **WHEN** an operator configures a shorter ticket lifetime and a different WebSocket message limit
- **THEN** ticket expiry uses that lifetime and inbound message enforcement uses that limit

#### Scenario: Non-default output buffering
- **WHEN** a slow client reaches the configured output high watermark
- **THEN** reading pauses and resumes after draining below the configured low watermark, without unbounded buffering or discarding live output

#### Scenario: Notification configuration
- **WHEN** configured notification lifetimes and a stack bound differ from defaults
- **THEN** browser notifications expire by the configured level-specific lifetime and drop the oldest notification when the bound is exceeded

### Requirement: Public browser configuration projection

The hub SHALL expose unauthenticated GET /api/client-config returning JSON with schema version 1 and an explicit allowlist of effective browser settings. Durations SHALL be numeric milliseconds in the browser response. The response SHALL use Cache-Control: no-store and SHALL NOT contain credentials, backend paths, backend configuration, or tmux state. Reading it SHALL NOT invoke tmux or authorize session operations.

#### Scenario: Read before login
- **WHEN** a caller without a bearer token requests client configuration
- **THEN** it receives only public browser settings and session APIs remain protected

#### Scenario: Secret isolation
- **WHEN** a token is configured in YAML or the environment
- **THEN** neither the token nor its configuration field appears in the client response or child-process environment

### Requirement: Configuration distribution and diagnostics

The application SHALL report the chosen configuration source and effective credential source at startup, redact secrets from ordinary configuration diagnostics, and retain the dedicated startup credential disclosure. Releases SHALL include a documented example YAML without a real credential. The binary SHALL remain runnable without YAML or external web assets.

#### Scenario: Packaged binary without configuration
- **WHEN** an operator extracts a release and starts only its binary
- **THEN** the embedded UI is served with built-in defaults and startup reports the selected sources
