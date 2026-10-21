## 1. Baseline and durable host model

- [x] 1.1 Establish the `multi-host-support` main-spec baseline before applying this change's deltas; verify its host-management, persistent host state and web host administration requirements exist and these deltas preserve unchanged SSH/file contracts.
- [x] 1.2 Add typed local/SSH records and validation to registry/store/API projections; verify tests cover local metadata edits, SSH field retention, irrelevant-field rejection, immutable connection type and concurrent singleton-local registration.
- [x] 1.3 Add transactional schema migration, bootstrap marker, default settings and per-ID revision high-water marks; verify fixtures retain old SSH IDs, ciphertext, trust and revisions, and interrupted/repeated migrations cannot duplicate initialization.
- [x] 1.4 Make application startup initialize private durable state before listening; replace lazy-local assumptions and verify unwritable state, wrong/missing key, corrupt/newer schema and competing ownership fail without data replacement, while help/version still work.
- [x] 1.5 Implement persistent local registration/removal/re-registration and nullable default updates with concurrency checks; verify restart preserves names, defaults and empty registries, rejects stale updates and clears a removed default atomically.

## 2. Runtime routing and lifecycle

- [x] 2.1 Resolve every target through registration and dispatch by connection type using existing Runtime service interfaces; verify empty/unknown/deleted IDs perform no work and an arbitrary local-type ID uses the local adapter.
- [x] 2.2 Separate metadata notifications from connection invalidation, including HTTP hooks, leases, tickets and WS attachments; verify local/SSH rename preserves active terminals while connection edits invalidate obsolete work only after successful persistence.
- [x] 2.3 Implement uniform removal invalidation and revision allocation on ID reuse; verify local/SSH attachments close without session-kill calls and old tickets, requests and file observations remain invalid across deletion, restart and re-registration.
- [x] 2.4 Audit existing documented compatibility routes and retain any implicit-local behavior only at those boundaries; verify they resolve the historical ID, fail after its removal and never follow a different default. Remove default targets from general service/browser APIs and verify callers supply explicit IDs.

## 3. Management API and capabilities

- [x] 3.1 Expose uniform authenticated host CRUD and default-settings APIs; verify local edit/delete acceptance, credential redaction, stale-update handling and refusal of unauthenticated topology/settings access.
- [x] 3.2 Implement adapter-backed structured capability status and remove hard-coded local HTTP results; verify actual local tmux/file configuration, not-applicable SSH/SFTP, disabled files, local roots and remote replacement limits are represented accurately with bounded checks.
- [x] 3.3 Normalize structured error source/code across session, file and terminal boundaries; verify central token failures differ from host authentication/trust failures and uncertain mutations are never automatically replayed.

## 4. Browser management and workspace

- [x] 4.1 Update API types and HostManagerModal for connection-type forms and uniform edit/test/default/remove controls; verify DOM tests cover local and SSH hosts, explicit trust only for SSH, and status-field rendering rather than object truthiness.
- [x] 4.2 Replace fabricated local bootstrap with authenticated hosts/settings loading and nullable selection; verify recent-host/default precedence, retry on discovery failure, empty-registry add flow and zero target requests before selection.
- [x] 4.3 Add removed-target handling and eliminate automatic local fallback during active use; verify dirty drafts survive, input/saves stop, late responses are ignored and switching requires explicit selection.
- [x] 4.4 Use connection identity for runtime state validity while allowing metadata updates; verify rename preserves terminal/editor state, re-registration cannot reuse stale state, and historical ordering migration remains one-time without recreating local.
- [x] 4.5 Replace host-ID/message-substring error heuristics with structured handling; verify Hub token rejection prompts login on either connection type and SSH authentication failure preserves login and other-host access.

## 5. Integration and release readiness

- [x] 5.1 Run focused backend and browser suites plus the repository's relevant type/build checks; verify the end-to-end matrix covers bootstrap, rename, remote default, remove-last-host, restart-empty and explicit local re-add with session survival. Exercise tmux only through existing isolated Go test helpers, outside tmux as required by repository guidance; never use ad hoc tmux cleanup.
- [x] 5.2 Update both READMEs and applicable configuration/API documentation for writable state, typed hosts, defaults, removal, capability changes and backup/rollback; verify examples match implemented behavior and explicitly describe restored-backup loss of post-upgrade changes.
- [x] 5.3 Validate final specs against the landed predecessor baseline with `openspec validate unify-host-management --strict`, review scenario coverage and confirm no unresolved implementation tasks before requesting archive.
