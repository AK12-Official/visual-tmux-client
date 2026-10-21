## Why

Local and SSH hosts already share operation interfaces, but local registration, editing, removal, selection and status still follow special rules. Make them equal managed hosts while preserving the real differences in execution, authentication and filesystem guarantees.

## What Changes

- Persist all hosts with explicit immutable connection types (`local` or `ssh`), stable IDs and consistent revision semantics. Automatically register the local execution environment only during initial provisioning.
- Allow local and SSH hosts to be renamed, tested, selected as default and removed through the same management lifecycle. Permit an empty registry and explicit local re-registration.
- Separate the Hub-wide default host, browser recent selection and explicit operation target. Removing an active host never redirects operations to another host.
- Report execution, tmux and file capabilities consistently, including unknown and not-applicable states; retain backend-specific connection settings and filesystem guarantees.
- Classify failures by structured source/code, not host ID. Preserve drafts and invalidate stale runtime identities consistently across both connection types.
- **BREAKING**: Normal Hub startup requires writable durable host state, including local-only installations. No ephemeral local fallback remains.
- **BREAKING**: General runtime resolution and internal clients require explicit host targets. Existing explicit local URLs remain valid while that host is registered; any retained legacy implicit-local entry points are confined to compatibility adapters.
- **BREAKING**: The local host is removable and no longer automatically recreated after removal. Capability responses use one structured representation instead of boolean/object mixtures.

## Capabilities

### New Capabilities

None relative to the required `multi-host-support` baseline.

### Modified Capabilities

- `host-management`: Unified host registration, lifecycle, capabilities, defaults and explicit runtime targeting. This path is introduced by the implemented, unarchived `multi-host-support` predecessor; its baseline must land first. This change supplies deltas against that baseline rather than duplicating its ADDED requirements.
- `web-session-manager`: Uniform host management, explicit startup selection, empty/removed-host states and host-independent central authentication handling.
- `runtime-configuration`: Mandatory durable host state, transactional bootstrap/migration and authenticated default-host settings.

## Impact

Affects `hub/internal/host` registry/store/runtime/capabilities, application wiring, HTTP/WS routing and ticket validation, browser API types, host administration, selection and file/terminal state. Update English/Chinese deployment documentation and existing tests. No new remote agent or transport dependency is required.

Dependency: apply/archive `multi-host-support` before applying this change's spec deltas. Its implemented code is the source baseline. This change deliberately supersedes its immutable built-in local host and lazy local-only storage requirements; all SSH credential protection, trust, isolation and file guarantees remain in force. Do not edit or archive that predecessor as part of this planning operation. Before archiving this change, verify the predecessor requirements exist in main specs.
