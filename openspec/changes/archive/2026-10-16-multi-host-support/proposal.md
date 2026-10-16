## Why

VTC currently manages only the machine running its Hub. Users need one browser workspace for tmux sessions and files across multiple SSH-accessible machines, without installing an agent on those machines.

## What Changes

- Add authenticated Web management of remote hosts with stable identities, connection testing, explicit SSH host-key trust, and password or private-key authentication (including encrypted private keys).
- Persist host configuration and encrypted credentials so Hub restarts require no credential re-entry. Retain the shared-token, single-administrator authorization model.
- Support remote session listing, creation, rename, termination, interactive terminals, and SFTP file browsing, editing, preview, download, creation, rename and deletion in the same release.
- Route operations through host-specific runtimes; refactor current singleton service wiring where necessary. Keep the built-in `local` host and existing local routes working.
- Scope tickets, terminal state, ordering, file tabs and asynchronous results to their owning host; isolate host failures and distinguish uncertain mutation outcomes from failures known not to have committed.
- Define remote file capabilities explicitly rather than claiming local filesystem confinement and identity guarantees over SFTP. Unsupported safe replacement disables overwrite instead of using destructive fallbacks.
- Keep existing local behavior and safeguards. No intentional breaking change to existing local API callers; new remote APIs and capability metadata are additive.

## Capabilities

### New Capabilities

- `host-management`: Host registry, Web enrollment, credential persistence, SSH trust, connection lifecycle, capability discovery and single-admin authorization.

### Modified Capabilities

- `session-hub`: Host-aware routing and tickets, SSH-backed session execution and attachment, safe remote command encoding.
- `web-session-manager`: Host selection and management, host-qualified session state, independent failure handling and credential errors.
- `file-manager`: Host-scoped file operations and editor state, SFTP capability contract and explicit remote consistency limitations.
- `runtime-configuration`: Durable state/key paths, SSH operational limits, local-only root configuration and private authenticated host capability projection.

## Impact

- Backend: `app`, `config`, `auth`, `session`, `tmux`, `terminal`, HTTP/WS transports and `files`; introduce host registry, credential store, SSH connection manager and remote adapters.
- Frontend: host administration and selection, API clients, terminal targeting, session ordering/activity, file manager state and capability feedback.
- APIs: authenticated host CRUD/probe/trust and capability endpoints; existing host-qualified session/file/WS paths resolve actual registered hosts.
- Dependencies: embedded Go SSH and SFTP implementations, plus a transactional host store; exact dependency versions are selected and verified during implementation.
- Operations: durable state and master-key backup, SSH host trust recovery, per-host errors, bounded connections and cancellation, remote compatibility documentation.
- Tests: retain local isolation helpers; add disposable SSH/SFTP fixtures and cross-host identity, credential, failure and file-integrity tests. No developer SSH credentials or production hosts are used.
