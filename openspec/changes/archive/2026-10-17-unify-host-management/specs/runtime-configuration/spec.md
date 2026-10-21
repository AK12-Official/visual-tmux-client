## MODIFIED Requirements

### Requirement: Persistent host state configuration

Configuration SHALL support durable host-state and master-key file locations with documented OS-user defaults and configuration-relative path resolution. Normal server startup SHALL require writable durable host state even for local-only installations. First initialization SHALL atomically persist the local registration, initial local default and initialization marker. An initialized registry SHALL NOT recreate removed hosts, including when empty. Existing SSH records SHALL migrate without changing IDs, credentials, trust or revisions. Migration SHALL be transactional, backed up and restart-safe. Startup SHALL fail before listening if required persistence is unavailable, without falling back to an ephemeral local host. Informational help/version commands SHALL remain usable without initializing state. Explicit invalid storage configuration, corrupt existing state, unsupported schemas and missing keys for existing encrypted state SHALL fail startup without replacing data. State and key files SHALL be private to the Hub user. Only one Hub process SHALL own a state store at a time. Documentation SHALL describe backup/restore of both state and key, and persistent mounts for container deployments.

#### Scenario: Fresh installation
- **WHEN** a fresh installation starts with writable default state
- **THEN** it persists one local registration, the initial default and initialization marker before serving requests

#### Scenario: Read-only fresh local deployment
- **WHEN** a fresh local-only deployment has no writable state directory
- **THEN** startup fails visibly before listening and no temporary local-only mode is served

#### Scenario: First enrollment
- **WHEN** a local-only installation saves its first SSH host after startup initialized durable state
- **THEN** the Hub persists the registration and encrypted credentials in that existing private state, or rejects the save clearly without claiming persistence

#### Scenario: Initialized empty registry
- **WHEN** all registrations have been removed and the Hub restarts
- **THEN** the registry stays empty and the default stays unset

#### Scenario: Existing installation migration
- **WHEN** an installation using the previous host schema upgrades
- **THEN** existing SSH IDs, encrypted credentials, trust and revisions are retained and the historical local identity is persisted once

#### Scenario: Interrupted migration
- **WHEN** migration fails or is interrupted before commit
- **THEN** restart recovers a consistent schema without duplicate bootstrap hosts or partial settings

#### Scenario: Restore registry without key
- **WHEN** an operator restores only the encrypted registry
- **THEN** startup identifies the missing key and leaves the registry untouched

#### Scenario: Second Hub using the same state
- **WHEN** another Hub already owns the state store
- **THEN** startup refuses that store instead of creating competing connection and credential state
