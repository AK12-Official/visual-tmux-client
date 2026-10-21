## Context

See proposal.md for motivation and predecessor ordering. `host.Runtime` already separates Sessions, Files and Processes, but Registry synthesizes `local`, Store rejects its persistence, and the resolver bypasses registration for empty/local IDs. The browser seeds Local and uses local identity in selection, controls and authentication handling. Capability HTTP handling returns hard-coded local success independently of the capability checker.

The existing multi-host change is implemented but not archived. The deltas here target its host-management and persistent-storage requirements. Its unchanged SSH trust, secret protection, remote resource bounds and file contracts continue to apply.

## Goals / Non-Goals

**Goals:** Centralize host identity and lifecycle, make runtime selection explicit, preserve independent backend guarantees, and migrate without changing existing host IDs or redirecting old work.

**Non-Goals:** Local execution over loopback SSH; remote agents; cross-host session migration; multiple local OS identities; editing connection type in place; new host sorting/hiding features; redesigning existing file confinement or SSH enrollment protocols.

## Decisions

### 1. Common records with typed connections

Persist ID, name, record revision and connection revision for every host. Add a discriminated connection configuration: `local` has no SSH address, credentials or trust; `ssh` retains the existing endpoint, user, encrypted secrets and pinned key. Validate type-specific fields and reject irrelevant fields rather than accepting misleading settings. Preserve `local` as the existing installation's ID, not a permission rule. Determine behavior from connection type. A new local registration may receive a new ID; allow at most one active local registration per registry, enforced transactionally.

Connection type is immutable for both variants. Users remove and register a different target instead of silently changing an open workspace's execution model. SSH to 127.0.0.1 remains an SSH registration, potentially with a different user/environment. The local adapter always means the Hub execution environment, including its container namespace where applicable.

Alternative: make local an SSH host at 127.0.0.1. Rejected because it adds an SSH service dependency and changes identity and filesystem semantics without improving the domain abstraction.

### 2. Durable bootstrap and registry ownership

Open and validate the existing private SQLite state before serving requests, even for local-only operation. Add durable bootstrap metadata, a nullable default-host setting with its own concurrency revision, and retained per-ID revision high-water marks for deleted registrations. Bootstrap creates the local record, selects it as initial default and marks initialization complete in one transaction. An initialized empty registry remains empty after restart. Validate existing key/state before migrations; preserve exclusive ownership and backup behavior.

Persist local records without fabricating credentials. Reuse the existing vault lifecycle; maintaining a private key file even before SSH enrollment is acceptable, but never require SSH fields for a local record. An unwritable state directory fails normal server startup clearly. Help/version remain independent of state initialization.

Alternative: persist only overrides for a synthesized local host. Rejected because deletion, revision allocation and initialization would retain a second lifecycle.

### 3. Resolve registered hosts through adapters

All requests first resolve a registered record and capture its identity/version. Dispatch by connection type to a local or SSH runtime provider. Keep the existing independent Sessions, Files and ProcessFactory interfaces. Local resources can remain shared at application scope, but their runtime identity comes from the registered record; deleted registrations cannot bypass lookup.

An absent target is an invalid request; an unknown or removed ID is host-not-found. Explicit local URLs continue to work while the ID exists. Audit legacy paths: where an existing documented implicit-local entry point must remain, adapt it to the literal legacy ID at that boundary and perform normal lookup. Never translate it to the user-selected default or resurrect a removed record. Remove default-local arguments from general browser and service APIs.

Alternative: spread ID conditionals across handlers. Rejected because target validation and invalidation would remain inconsistent.

### 4. Lifecycle and revisions

Editing name increments only the record revision. Runtime validity, attachment/ticket lifetime and file observation identity use connection revision, so metadata edits do not disconnect work. Connection edits increment connection revision and invalidate only affected work after persistence succeeds. Default-host changes use their own settings revision and do not invalidate runtimes.

Deletion atomically removes registration, clears the default if necessary and retains an ID revision high-water mark. Afterwards invalidate tickets, close local/remote attachments and refuse new work. Do not issue tmux kill commands; cancellation does not claim rollback of dispatched operations. Existing local adapters need the same attachment invalidation path as remote adapters.

When an ID is reused, allocate revisions greater than every previously issued revision for that ID. Old tickets, responses and editor observations remain stale even after restart/re-registration. Apply this rule to both connection types. Prefer a fresh generated ID for new registrations, while supporting existing IDs safely.

### 5. Default and browser selection

Expose authenticated read/update of nullable `defaultHostId` with optimistic concurrency (for example `/api/host-settings`); keep it out of public client config. Any registered host can be default regardless of current reachability. Initial bootstrap chooses local once. Removing the default clears it atomically, without selecting another host.

Browser bootstrap loads authenticated hosts and settings before session work. Select a still-registered browser recent host, otherwise a registered default, otherwise show selection. Store recent choice scoped to the Hub origin. A failed host-list fetch is a retryable error, not evidence of an empty registry. An empty successful list shows Add host and no session/file/terminal requests. Reachability failure does not switch hosts.

If the active host is removed during use, enter a removed-target state and require an explicit new selection. Preserve dirty drafts for copying, stop old-target input/saves and reject late responses. Do not apply bootstrap fallback during active removal. Existing legacy session ordering migration remains one-time and bound to the historical `local` identity, without recreating it.

### 6. Uniform controls and truthful capabilities

Offer edit, test, set-default and confirmed remove for both connection types. Common metadata uses the same form; connection-specific sections use type rather than ID. Label testing as checking host capabilities so local execution does not imply a network connection. Keep SSH key confirmation only where applicable.

Use one structured capability status with explicit `available`, `unavailable`, `unknown` and `not_applicable` states plus diagnostic code and observation time. Report execution access, tmux and file functionality independently; SSH/SFTP are backend details. Local tests inspect actual configured services and report SSH/SFTP as not applicable, never synthetic success. Global files-disabled and configured local roots must be reflected truthfully. Retain separate local confinement and remote replacement guarantees. Use bounded checks and never probe all hosts synchronously while listing.

Normalize frontend DTOs to this representation and render status fields rather than object truthiness. Replace duplicated local capability construction in HTTP and connection code with adapter-backed checks. This is a coordinated API/UI change; old boolean/object mixtures are not part of the new contract.

### 7. Errors follow origin, not identity

Return structured codes with central-auth, host-auth/trust, availability, revision and outcome-unknown categories. Any central authentication rejection prompts re-login, including on SSH-host pages. SSH failures leave the Hub login intact and offer host repair. Poll/retry decisions follow code/category; remove message-substring and local-ID heuristics. Preserve existing no-mutation-replay behavior.

## Risks / Trade-offs

- Mandatory persistence breaks fresh read-only local deployments -> document writable state mounts and fail visibly before listening; never claim unpersisted settings were saved.
- Deletion/re-registration can revive old identities -> transactional revision high-water marks and regression tests across restarts for both backends.
- Metadata edits currently trigger broad host-change hooks -> separate runtime invalidation from metadata publication and test active terminals/drafts.
- Empty registries expose assumptions throughout bootstrap -> explicit nullable selection and tests asserting zero target requests before selection.
- Capability refactoring can weaken file guarantees -> retain existing file adapters and validate reported guarantees against effective configuration.
- Predecessor specs are unarchived -> apply its spec baseline first; validate archive compatibility before merging this delta. No changes to predecessor artifacts during this proposal.

## Migration Plan

1. Establish the implemented `multi-host-support` spec baseline before applying these deltas; retain its unchanged requirements.
2. Back up state and its key, then migrate schema transactionally. Existing records become SSH records without ID, ciphertext, trust or revision changes. Add the historical local record, bootstrap marker and initial default once; seed revision high-water marks.
3. Fresh installations perform equivalent initialization. Subsequent startups never infer first-run from an empty hosts table. Failed migrations leave recoverable original state and do not serve a partial registry.
4. Release backend and embedded UI together. Preserve existing explicit routes and host-qualified browser state; no arbitrary host ID renaming. Document mandatory persistence and host removal behavior in both READMEs.
5. Rollback requires stopping the new Hub and restoring the pre-migration database and matching key before using an older binary. Do not run an old binary against a newer schema or delete state as a fallback; explain that post-upgrade registration changes are lost on restore. No migration or rollback step terminates tmux sessions.
