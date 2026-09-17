## MODIFIED Requirements

### Requirement: File access boundary configuration

YAML SHALL configure whether file operations are restricted to a set of root directories. When no root is configured, the boundary SHALL be the access the hub's own operating-system user already has, so that file operations are not confined to any directory. When one or more roots are configured, they SHALL replace that default and every operation SHALL be confined to them. The hub SHALL resolve each configured root to its canonical path at startup and SHALL reject at startup any root that does not exist, is not a directory, or cannot be resolved, while the file capability is enabled. Configuring the filesystem root SHALL be permitted.

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
