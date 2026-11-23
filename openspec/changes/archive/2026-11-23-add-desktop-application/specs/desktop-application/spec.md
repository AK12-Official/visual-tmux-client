## Purpose

Enables users to launch the existing Visual Tmux Client workspace as a Linux or macOS desktop application with automatic authentication, durable preferences and managed backend lifetime.

## ADDED Requirements

### Requirement: Desktop launch of the existing workspace

The desktop application SHALL start its own backend and display the embedded existing workspace in a native window without requiring a separately launched server, external browser, Go or Node installation. Its backend SHALL bind only to IPv4 loopback on an automatically assigned port, regardless of server-mode address settings. It SHALL retain the existing authenticated HTTP APIs and binary terminal protocol. The ordinary server command, browser login and headless release artifacts SHALL remain available with their existing behavior.

#### Scenario: Launch from desktop icon
- **WHEN** a user launches the installed application without a terminal
- **THEN** the application starts its backend, waits for readiness and displays the workspace without opening an external browser or requiring a token to be copied

#### Scenario: Existing default port is occupied
- **WHEN** another service occupies port 7690
- **THEN** desktop startup uses another available loopback port and does not stop or connect to that service

#### Scenario: Server configuration specifies external binding
- **WHEN** desktop configuration contains a public listening address
- **THEN** the desktop backend still binds only to loopback and reports that transport binding is managed by desktop mode

#### Scenario: Ordinary server startup
- **WHEN** the existing server command is used instead of the desktop entry
- **THEN** its configured address, credential disclosure, browser login and transport behavior remain unchanged

### Requirement: Automatic and bounded desktop authentication

The owned desktop document SHALL authenticate automatically using a fresh per-process credential delivered through a short-lived, single-use launch exchange. Launch authorization SHALL expire within 30 seconds and SHALL be removed from the visible document address before application initialization. The backend SHALL reject invalid, expired, reused or foreign-origin launch exchanges. The bearer SHALL NOT appear in URLs, command arguments, logs, durable preference files, public configuration or terminal process environments. Desktop API access SHALL retain bearer authentication, and terminal attachment SHALL retain host/revision/session-bound tickets and existing origin checks. Publicly loading the application address SHALL NOT grant automatic authorization.

#### Scenario: Initial authenticated startup
- **WHEN** the owned window redeems its valid launch authorization
- **THEN** it obtains credentials without a login prompt and authenticates before requesting protected workspace state

#### Scenario: Invalid launch exchange
- **WHEN** an exchange uses an expired or already-redeemed authorization, or originates outside the owned application origin
- **THEN** the backend discloses neither credentials nor saved preferences and performs no host/session/file operation

#### Scenario: Unauthenticated local caller
- **WHEN** another local browser or client opens the bare desktop URL or calls a protected API without authorization
- **THEN** it receives no automatic credential and protected operations are refused

#### Scenario: Desktop document reload
- **WHEN** the user reloads the owned document during the same backend process
- **THEN** authentication and initialization recover once without manual token entry, duplicate polling or duplicate attachments

#### Scenario: Central authorization recovery fails
- **WHEN** the owned document cannot obtain a valid current-process credential
- **THEN** it shows a desktop initialization error with a recoverable reload/reopen action and does not start protected operations or request that the user copy a token

### Requirement: Port-independent desktop preferences

Desktop language preference, terminal font size, sidebar state, recent host, per-host session ordering and pins SHALL persist across application restarts independently of the listener port. Preferences SHALL be hydrated before language initialization and workspace rendering. Only validated allowlisted UI settings SHALL be persisted; credentials, file contents and unsaved drafts SHALL NOT be included. Existing locale/font validation and host identity rules SHALL continue to apply. Persistence SHALL use private, bounded, versioned storage and atomic replacement; failures SHALL be visible without blocking terminal use or falsely reporting durability. Browser preferences SHALL continue using their existing storage independently.

#### Scenario: Restart with a different port
- **WHEN** the user changes language, font size, sidebar, recent host, ordering and pins, then normally quits and relaunches on another port
- **THEN** those settings are restored before the workspace is displayed, with recent-host and font values validated against current state and configuration

#### Scenario: Ordering remains host-scoped
- **WHEN** two hosts contain identically named sessions
- **THEN** restoring desktop preferences applies ordering and pins to their own host only

#### Scenario: Preference write fails
- **WHEN** durable preference storage becomes unwritable
- **THEN** the application reports the persistence failure, keeps the running workspace usable and does not claim the changes were saved

#### Scenario: Corrupt preference storage
- **WHEN** the preference file cannot be parsed or has an unsupported version
- **THEN** the application reports the issue, preserves the original file and initializes safe UI defaults without replacing host state or credentials

#### Scenario: Credentials are excluded
- **WHEN** desktop preferences are written
- **THEN** the durable preference file contains no bearer, launch authorization, SSH credential or edited file content

### Requirement: Predictable desktop configuration and state ownership

The desktop entry SHALL support explicit configuration selection and otherwise discover its implicit configuration in the application's OS user configuration directory independently of its working directory. Relative configured paths SHALL retain configuration-relative resolution. Listener address, central bearer and browser origin SHALL be desktop-managed and identified as such without disclosing configured secrets. Other operational limits and the existing durable host database/key locations and single-owner constraints SHALL remain effective. Help/version SHALL work without starting a GUI or opening durable host state. Missing local tmux SHALL yield an actionable local capability failure without preventing remote-host access; desktop discovery SHALL honor explicit executable configuration before PATH and documented platform fallback locations.

#### Scenario: Desktop launch from another working directory
- **WHEN** the same desktop profile is launched from different working directories without an explicit configuration
- **THEN** it selects the same implicit configuration and persistent state locations

#### Scenario: Explicit configuration is invalid
- **WHEN** an explicit file is missing, malformed or contains invalid operational settings
- **THEN** startup fails visibly without opening a workspace or replacing existing state

#### Scenario: Local executable absent
- **WHEN** tmux cannot be found locally
- **THEN** the workspace identifies local tmux unavailability and remote host management remains usable

#### Scenario: State already owned by server mode
- **WHEN** an independently running server already owns the selected host state
- **THEN** desktop startup reports the ownership conflict without stopping the server, copying its credential or creating substitute state

#### Scenario: Repeat desktop launch
- **WHEN** the user opens an already-running desktop profile again
- **THEN** its existing main window is focused rather than opening another backend against the same state

### Requirement: Desktop terminal interaction parity

The desktop workspace SHALL preserve byte-faithful terminal output, control-key input, Chinese input method composition, paste, mouse/trackpad history scrolling, selection/copy, measured resize, fullscreen refit and reconnect behavior. Copy without a terminal selection SHALL NOT consume the terminal interrupt key. Native menus SHALL NOT intercept required terminal input. Clipboard failures SHALL be visible. Continuous output SHALL retain the existing bounded backpressure behavior.

#### Scenario: Input method and paste
- **WHEN** the user composes Chinese text and pastes multiline text into a focused desktop terminal
- **THEN** the committed text reaches the selected session without duplicate composition input or lost characters

#### Scenario: Copy versus interrupt
- **WHEN** the user copies selected terminal text and later sends Ctrl+C with no selection
- **THEN** the first action copies the selection and the second reaches the terminal as an interrupt rather than being swallowed by a native menu

#### Scenario: Resize and fullscreen
- **WHEN** the desktop window is resized or the terminal enters and leaves fullscreen
- **THEN** the terminal reports its measured final grid size and tmux renders to the visible viewport

#### Scenario: Sustained output and reconnection
- **WHEN** a session continuously produces output and its attachment is later interrupted and restored
- **THEN** output remains correctly rendered with bounded buffering and reconnection does not replay previous input

### Requirement: Desktop file download and native action boundaries

Desktop downloads SHALL offer a native destination chooser and save the selected source's bytes without changing the active host or source revision. Source authorization, file enablement, roots/account boundaries and transfer limits SHALL remain enforced. Cancellation SHALL create no destination file; replacement SHALL require confirmation; failed writes SHALL NOT leave a partial replacement or report success. Native actions SHALL be restricted to the owned application origin. Activating an external HTTP/HTTPS link SHALL open the system browser rather than navigating the privileged workspace; unsupported external schemes SHALL be rejected.

#### Scenario: Download from SSH host
- **WHEN** the user downloads an allowed remote file and chooses a local destination
- **THEN** the chosen file is saved with matching bytes and the source request is authorized against the originally selected host and connection revision

#### Scenario: Source access refused
- **WHEN** file management is disabled or the source exceeds configured access/size limits
- **THEN** the desktop download is refused and no destination file is committed

#### Scenario: Cancel or failed destination write
- **WHEN** the user cancels the chooser or saving fails
- **THEN** cancellation creates no file and failure reports the reason without replacing an existing destination with partial content

#### Scenario: Host changes during save selection
- **WHEN** the user switches hosts or the source connection revision changes while a download is pending
- **THEN** the operation remains bound to its original source or fails explicitly, and never downloads a same-named file from the new target

#### Scenario: External link
- **WHEN** the user activates an external HTTPS link from rendered content
- **THEN** it opens in the system browser and the application window retains its authenticated workspace

### Requirement: Desktop quit and failure lifecycle

User-initiated window close and explicit Quit SHALL follow one application shutdown flow on both platforms. Unsaved editor content SHALL require confirmation before discard; cancelling SHALL retain the running workspace and connections. A confirmed normal quit SHALL flush pending preferences and release owned attachments, listener, file resources and host connections within bounded shutdown budgets while preserving local and remote tmux sessions. Termination signals SHALL trigger bounded cleanup without indefinite UI confirmation. Partial startup or backend failure SHALL show an actionable error and release acquired resources. Shutdown SHALL be idempotent and SHALL NOT kill unrelated processes or tmux servers.

#### Scenario: Close the only window
- **WHEN** the user closes the only window with no unsaved edits
- **THEN** the application quits through bounded cleanup without leaving an invisible backend

#### Scenario: Cancel quit with unsaved editor
- **WHEN** the user requests Quit with dirty editor content and cancels the discard confirmation
- **THEN** the same workspace remains open with its unsaved content and active attachments

#### Scenario: Confirm quit and relaunch
- **WHEN** the user confirms quitting while local and SSH tmux sessions are attached and later relaunches
- **THEN** application-owned resources have been released and those tmux sessions remain listable and attachable

#### Scenario: Startup or backend failure
- **WHEN** the native window fails after backend startup or the backend fails during use
- **THEN** an actionable error is shown and cleanup releases the owned listener and durable-state lock

#### Scenario: Repeated termination paths
- **WHEN** window quit and an OS termination signal overlap
- **THEN** resources are shut down once within the configured budgets without killing underlying tmux sessions

### Requirement: Linux and macOS desktop distribution evidence

Desktop release artifacts SHALL be provided for the documented initial targets: Ubuntu 24.04 amd64 and macOS arm64/amd64 with a documented minimum deployment target. Packages SHALL include embedded frontend assets and launch integration, declare native runtime/tmux requirements, and be distinct from existing headless artifacts. Claimed supported combinations SHALL have native build/package checks and recorded real-WebView acceptance evidence for startup, authentication, input, clipboard, resize/fullscreen, file saving, preference restart and session survival. Linux evidence SHALL cover Wayland and X11. Unsigned macOS development packages SHALL be labeled; signed/notarized release claims SHALL require verification.

#### Scenario: Install desktop artifact
- **WHEN** a user installs the artifact on a documented target with its declared dependencies
- **THEN** a desktop launcher starts the embedded workspace without building assets or manually starting a backend

#### Scenario: Platform not exercised
- **WHEN** runtime acceptance has not been performed for a target combination
- **THEN** its verification remains explicitly pending and it is not represented as validated support

#### Scenario: Headless build remains independent
- **WHEN** the original headless CI and release process runs without native desktop development libraries
- **THEN** its ordinary tests and four existing platform builds remain usable independently of desktop packages
