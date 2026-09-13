## ADDED Requirements

### Requirement: Browser runtime configuration bootstrap

The client SHALL retrieve and validate public runtime configuration before starting session polling or creating a terminal. It SHALL use the configured polling, activity, resize, reconnect, scrollback and font parameters without requiring a frontend rebuild. If configuration cannot be loaded or is invalid, it SHALL show a recoverable initialization error with a retry action and SHALL NOT silently use independent hard-coded runtime defaults. A successful retry SHALL initialize exactly once without duplicate timers or attachments. Reloading the page SHALL fetch configuration again.

#### Scenario: Changed server settings
- **WHEN** the hub restarts with changed browser settings and the page is reloaded
- **THEN** subsequent polling and terminal creation use those settings from the same embedded frontend build

#### Scenario: Configuration unavailable
- **WHEN** the configuration request fails or returns an unsupported version or malformed settings
- **THEN** the page shows an initialization error and retry action, and no polling or attachment starts

#### Scenario: Retry succeeds
- **WHEN** initialization failed and a subsequent retry loads valid settings
- **THEN** initialization proceeds once and the normal credential flow is available

### Requirement: Configured terminal font preferences

The client SHALL use the configured font default when no valid persisted font preference exists. A persisted integer preference SHALL take precedence within the configured minimum and maximum; out-of-range preferences SHALL be clamped and the corrected value persisted. Font controls SHALL respect those bounds while preserving immediate re-fit and persistence.

#### Scenario: Fresh browser
- **WHEN** no valid font preference exists
- **THEN** the configured default is used

#### Scenario: Valid personal preference
- **WHEN** a stored font size differs from the server default but is within its bounds
- **THEN** the stored size is used

#### Scenario: Server narrows range
- **WHEN** the stored size lies outside the newly loaded range
- **THEN** it is clamped to the nearest bound and saved before rendering terminals
