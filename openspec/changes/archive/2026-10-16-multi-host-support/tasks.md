## 1. Host identity and local compatibility

- [x] 1.1 Introduce host IDs, configuration revisions, host-target references and typed unavailable/unsupported/outcome-unknown errors; verify unit tests distinguish unknown host, absent session and unreachable host.
- [x] 1.2 Refactor `app.New` and HTTP/WS service resolution around a built-in local runtime without SSH; verify existing local session, file, ticket and terminal tests pass unchanged in behavior.
- [x] 1.3 Replace boolean existence checks at remote-facing service boundaries with error-preserving lookups; verify ticket issuance never maps connectivity failure to session-not-found.
- [x] 1.4 Extract reusable tmux argument construction and parsing from local execution; verify exact targeting, Unicode, pane framing and local environment-scrubbing regression tests.

## 2. Durable registry and credentials

- [x] 2.1 Add and pin a pure-Go SQLite driver with versioned schema and exclusive state-store ownership; verify transaction rollback, stale revision rejection, second-process refusal and CGO-disabled builds on supported targets.
- [x] 2.2 Add state/key location configuration, lazy default initialization and private file permissions; verify fresh read-only local operation, explicit invalid-path rejection and config-relative path resolution.
- [x] 2.3 Implement authenticated credential encryption bound to host/type/version with an independently persisted master key; verify restart decryption, nonce variation, tamper/cross-host rejection and token-change independence.
- [x] 2.4 Implement credential replacement/retention, redacted host DTOs and safe diagnostics; verify invalid updates preserve old data and response/log/browser-storage tests contain no saved secrets.
- [x] 2.5 Implement schema/version checks, safe migration backup and missing/wrong-key failure; verify failed recovery neither replaces keys nor mutates existing state.

## 3. SSH trust and connection management

- [x] 3.1 Pin the Go SSH dependency and implement password, private-key and encrypted-key credential adapters; verify each with disposable authenticated SSH fixtures and invalid-key/passphrase cases.
- [x] 3.2 Implement pre-authentication fingerprint probing, expiring endpoint/revision-bound trust challenges and pinned-key checking; verify no credential is offered before trust and changed keys/stale challenges are rejected.
- [x] 3.3 Implement per-host single-flight dialing, separate dial/handshake budgets, keepalive detection, finite backoff and bounded concurrency; verify stalled-handshake, cancellation and reconnect-storm tests.
- [x] 3.4 Implement revisioned runtime leases, connection invalidation on edits/removal and bounded shutdown; verify in-flight work cannot change target and removing one host leaves others connected.
- [x] 3.5 Separate control, terminal and cancellable file transport lifecycles as needed; verify cancellation of a stalled file operation does not disconnect an active terminal.

## 4. SSH session and terminal adapters

- [x] 4.1 Implement a POSIX-shell argument encoder and supported-environment probe; verify end-to-end literal handling of Unicode, quotes, dollar signs and backticks, and rejection of unsupported environments.
- [x] 4.2 Implement remote list/create/rename/kill, pane summaries and working-directory queries using shared tmux logic; verify two hosts with identical session names remain isolated and creation uses the remote home.
- [x] 4.3 Implement remote PTY attach/read/write/resize/wait/detach with existing terminal backpressure; verify byte fidelity, control keys, full-screen rendering, rapid resizing, idempotent close and session survival.
- [x] 4.4 Add host/revision binding to ticket issue and redemption while preserving local route compatibility; verify cross-host same-name, expired, reused and superseded-host ticket refusal plus existing Origin tests.
- [x] 4.5 Classify transport loss and uncertain session mutation results without replay; verify injected disconnects before/after dispatch distinguish known failure from outcome-unknown and never duplicate a mutation.

## 5. Host administration API

- [x] 5.1 Implement authenticated host CRUD with immutable local identity, revision checks and explicit credential updates; verify authentication, persistence, unknown-ID rejection and removal without session termination.
- [x] 5.2 Implement probe/trust/capability endpoints with independent SSH, tmux and SFTP status; verify an offline host cannot block registry listing and missing SFTP does not disable tmux.
- [x] 5.3 Publish sanitized host errors and authenticated capability metadata without altering public client configuration; verify remote auth failure is not HTTP 401 for the central login and unauthenticated requests initiate no remote work.

## 6. Remote file backend

- [x] 6.1 Select and pin the SFTP adapter after verifying bounded directory iteration, extension negotiation and effective cancellation; deliver targeted adapter tests demonstrating work does not scale with an entire oversized directory.
- [x] 6.2 Implement remote POSIX path validation, canonical-path checks, pseudo-filesystem refusal and account-bound capability reporting; verify local roots remain local and remote limits do not claim root-handle confinement.
- [x] 6.3 Implement bounded remote list/read/download with full-content classification, stable bounded spool/revision and metadata-only preview decisions; verify large directories, growing/shrinking files, binary tails and non-regular/stalled targets.
- [x] 6.4 Implement file/directory create, non-overwriting rename, confirmed recursive delete and working-directory startup/fallback; verify collision handling, whitespace paths, namespace mismatch and remote/local path separation.
- [x] 6.5 Implement content-derived save preconditions and serialization of conflicting Hub operations, including ancestor rename/delete; verify same-time/same-size content changes, two browser saves and save/delete races.
- [x] 6.6 Implement exclusive same-directory staging, length/size checks, metadata handling and capability-gated atomic replacement; verify unsupported servers never use delete-then-rename or truncate-in-place, forced saves recheck target type/existence, and hard-link limitations are surfaced.
- [x] 6.7 Implement bounded owned-staging recovery records and outcome-unknown save responses; verify disconnects before/after replacement, restart cleanup, no mutation replay and refusal to remove unidentifiable staging candidates.
- [x] 6.8 Wire remote files into host-scoped HTTP routes while preserving local file services; verify global disablement contacts no SFTP/filesystem and all existing local confinement/write-integrity tests still pass.

## 7. Browser host workspace

- [x] 7.1 Add host administration forms and trust/connection-test feedback using the project's UI conventions; verify keyboard operation, field errors, secret retention semantics and fingerprint confirmation in component tests.
- [x] 7.2 Add host selection, host-aware API/WS clients and host identity in terminal/file headers and destructive confirmations; verify creating and attaching same-named sessions on two hosts targets the selected host.
- [x] 7.3 Namespace session ordering, pins, activity and connection state by host; verify one-time local preference migration and that A's output never highlights B's same-named session.
- [x] 7.4 Guard polling and connection results by host/revision/request epoch; verify rapid host switching, late responses and host edits cannot overwrite another workspace or cause central logout.
- [x] 7.5 Bind file tabs, pending operations and save observations to host/revision/path; verify switching hosts preserves drafts, saves target the owning host and cross-host terminal path insertion is refused.
- [x] 7.6 Add remote capability and replacement-semantics feedback, hard-link acknowledgement and uncertain-save reconciliation; verify unsupported overwrite is disabled, lost acknowledgements keep tabs dirty and successful rereads do not silently discard later edits.
- [x] 7.7 Handle host removal/edit from this or another browser with dirty-editor preservation and invalidation; verify confirmations precede discard and stale revisions cannot save to a changed host.

## 8. Integration, performance and release documentation

- [x] 8.1 Add disposable SSH/SFTP fixtures with fresh keys, private files and isolated tmux servers; verify fixture setup/cleanup uses the existing environment/socket isolation rules and every new tmux-starting test package installs ParentGuard.
- [x] 8.2 Run a two-remote-host plus local acceptance flow covering enrollment, all authentication modes, session operations, terminal interaction and all file operations; verify restart restores credentials and existing tmux sessions remain attachable.
- [x] 8.3 Test real disposable OpenSSH SFTP replacement support and fault injection around commit, cleanup and coarse metadata; record supported server/shell combinations and verify failure behavior matches capability responses.
- [x] 8.4 Exercise simultaneous large file transfer, slow terminal consumers and an offline host; verify finite memory/concurrency, responsive independent terminals and bounded shutdown without orphaned attachment processes.
- [x] 8.5 Update README variants, example configuration and security/deployment documentation with server trust, single-admin authority, persistent mounts, key/state backup recovery, remote file limitations and rollback; verify examples match actual fields and contain no secrets.
- [x] 8.6 Run the repository's frontend and Go checks, race checks for connection/registry/file coordination, lint and supported-target builds; record results and any unavailable environment coverage before declaring implementation complete.
- [x] 8.7 Validate final implementation against every delta scenario and run `openspec validate multi-host-support --strict`; verify all planned capabilities ship together and no local behavior regression is accepted as an SFTP limitation.
