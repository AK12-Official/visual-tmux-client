## Context

See proposal.md for motivation and agreed scope. `app.New` currently wires one session service, process factory and file service. HTTP and WS routes contain host IDs but reject anything except `local`. Tickets bind only session names. Frontend session and file clients hard-code `local`; connection/activity/order state uses session names as keys.

Local file writes and configured roots rely on OS handles and metadata that cannot be assumed available over SFTP. The design must preserve local guarantees while explicitly describing the remote contract. This is a cross-cutting architecture and persistence change, so a design artifact is required.

## Goals / Non-Goals

**Goals:** Keep a single distributable Hub binary, route every operation to an explicit host, preserve tmux independently of connection lifetime, bound remote work, and make remote file limitations observable.

**Non-Goals:** Remote agent installation, SSH config import, jump hosts, interactive MFA, multi-user RBAC, cross-host file transfers or session migration, and strict Hub-enforced remote directory confinement. Remote OS account/server policy supplies the remote security boundary. These are proposed first-release boundaries, not claims that the user separately selected every implementation detail.

## Decisions

### 1. Resolve a host runtime before executing operations

Introduce a host application layer owning stable IDs, revisioned configuration and runtime lookup. Reserve `local` as a non-removable built-in host. A remote host denotes one SSH endpoint, username and that user's default tmux server. Display-name edits preserve identity; address, user, credential or trust changes advance its connection revision.

```
HTTP / WS --> Host Resolver --> Local Runtime --> tmux / local files
                          |
                          +--> SSH Runtime
                                  +--> Session commands
                                  +--> PTY attachments
                                  +--> SFTP files

Host service --> Registry / Credential store
SSH Runtime --> Connection manager --> Verified SSH endpoint
```

Keep domain session validation and terminal flow control. Refactor tmux argument construction and output parsing into reusable, transport-independent logic. Local invocation keeps direct argv. Remote invocation has a separately reviewed encoder. Do not make HTTP handlers depend on concrete SSH clients or turn one backend interface into a union of unrelated session and file methods.

Replace boolean existence checks at remote boundaries with result/error lookups: unreachable is not session-not-found. Runtime leases capture a host revision so an in-flight request cannot silently switch destinations after a configuration edit. Persist a successful edit before publishing the new revision and invalidating old connections; reject a concurrent stale update.

Alternative: add `hostId` conditionals to every existing singleton handler. Rejected because lifecycle and credential selection would become distributed and easy to mismatch.

### 2. Durable host store and independent credential encryption

Use a small versioned SQLite registry with a pure-Go driver to preserve CGO-free distribution. Store stable host records, revisions, pinned host keys, encrypted credential envelopes and bounded pending-write cleanup records. Pin and verify the driver version during implementation. Use a single writer transaction for each host edit; acquire exclusive ownership of the state directory so two Hub processes cannot independently manage its runtimes.

Encrypt passwords, private keys and passphrases using authenticated encryption, a fresh nonce per envelope, and associated data binding host ID, credential kind and envelope version. Generate a random master key on first initialization and store it separately with owner-only permissions; support an operator-supplied key file. Never derive it from the browser token. Default paths use the OS user configuration directory under `visual-tmux-client`; explicit paths support containers. State directories use owner-only access, and databases, sidecars and key files must not become world-readable.

No decrypted secret appears in host GET responses, diagnostics, process arguments, public configuration or browser storage. A blank credential edit means retain, not erase; replacement is explicit and authenticated. Only initial input remains transiently in the form. A missing/wrong master key for an existing store fails startup without replacing the key or data. Corrupt or unsupported store versions likewise fail visibly. Fresh local-only startup may run without creating state until host enrollment; an unavailable store disables enrollment with a clear error rather than breaking the existing local terminal. Explicitly configured invalid storage fails startup.

Alternative: plaintext host YAML or credentials in localStorage. Rejected for secret exposure and lack of transactional updates. Encryption protects a copied database, not compromise of the running Hub user, who can read the key and already controls all enrolled terminals.

### 3. Explicit enrollment and host trust

Provide authenticated host list/create/get/update/delete, probe and trust-confirmation operations. Proposed route family: `/api/hosts`, `/api/hosts/{hostId}`, `/api/hosts/probe`, `/api/hosts/{hostId}/probe`, `/api/hosts/{hostId}/trust`, and `/api/hosts/{hostId}/capabilities`. Creation can save an offline draft, but connection remains blocked until trust is confirmed.

A probe obtains the server key before offering credentials. Return a short-lived challenge bound to endpoint, observed key and draft/configuration revision. Explicit administrator confirmation pins the exact key, and subsequent connections verify it again. Never implement probe by ignoring host-key verification and authenticating anyway. Changed keys block automatic reconnection until separately confirmed. Endpoint edits invalidate old trust; username/credential edits invalidate connections but can retain the endpoint's trust.

Probe SSH authentication, tmux and SFTP separately. SFTP failure does not make a usable terminal unavailable. Host lists contain no secrets and do not synchronously wait for every host. Status is a last observation, not a guarantee of reachability. Only rejection of the central Bearer Token returns a central authentication failure; remote authentication errors are host errors.

### 4. Embedded SSH with bounded, isolated lifecycles

Use `golang.org/x/crypto/ssh` behind an adapter. It exposes host-key verification, password/private-key authentication, PTY requests and window changes. System `ssh` was considered, but managing password interaction and subprocess lifecycle would complicate Web-owned credentials; importing user SSH configuration is not required.

Maintain independent per-host connection state with single-flight dial, bounded connection/channel counts, dial AND handshake deadlines, keepalive failure detection and exponential backoff. Share a bounded control connection for short commands, use separate managed connections for terminal attachments and file work where needed so cancelling a blocked SFTP call cannot close a terminal. Cancel the underlying transport if a library call has no effective per-request cancellation. Release idle connections and close all managed transports within shutdown budgets.

Read-only probes/listing may retry within bounded budgets. Never automatically replay session mutations, file writes/deletes/renames or terminal input after connection loss. Report `outcome_unknown` when a request might have committed; re-query state and require a deliberate user action for another mutation. Disconnecting a browser closes only its attachment. Host deletion invalidates tickets and connections without killing tmux sessions.

### 5. SSH tmux command and terminal adapters

Support Unix-like remote hosts with a POSIX-compatible login command shell, tmux meeting the project's supported minimum, and matching SSH/SFTP filesystem paths. Detect and report unsupported shell/path environments. No remote Python, Node or VTC executable is required. Do not broaden the claim to arbitrary login shells in this release.

Build remote command text only from fixed operations and a tested POSIX argument encoder, including tmux executable selection and exact session targets. Reject invalid names before execution and preserve legal Unicode and shell metacharacters literally. Do not concatenate raw addresses, names or paths into commands. Keep stderr separate from parsed stdout for non-PTY operations. The remote user and its home directory govern creation; never send the Hub's home path. Preserve existing global-option and mouse behavior on the selected tmux server only.

Remote Process implements Reader, Writer, Resize, Wait and idempotent Detach over an SSH session with PTY requested before attach. Map window changes and remote exit status into current terminal lifecycle messages, and retain bounded backpressure. Tests must prove byte fidelity through the PTY, no local line discipline/double echo, and tmux survival after channel close. Transport loss is not evidence of session termination.

Bind browser tickets to `{hostId, connectionRevision, sessionName}`. Existing local ticket request/WS paths remain accepted, with the local identity resolved internally. Preserve single-use expiry and Origin checks. Never forward browser tickets or Bearer credentials to the remote server.

### 6. Remote file adapter with explicit guarantees

Use SFTP for file data and operations. Candidate library: `github.com/pkg/sftp`; version selection must verify cancellable operations and bounded directory iteration. Do not call a whole-directory convenience method then truncate in memory. If the selected API lacks bounded iteration, implement the bounded adapter or select a maintained compatible API before integrating. No shell-based file data fallback.

Keep local root handles and local write identity checks unchanged. `files.roots` applies to `local`; do not reinterpret Hub paths on remote machines. Remote files use the SSH account's permissions and server isolation, expose that boundary in authenticated capability metadata, and do not offer a misleading configurable remote root sandbox. Canonical/lexical pseudo-filesystem denials and path validation remain defense in depth, not a race-proof remote confinement claim. Use remote POSIX path handling, independent of Hub OS.

Remote list/read/create/rename/delete/download and existing preview/editor flows ship together. Enforce byte and entry limits during transfer, reject non-regular targets, preserve full-content text classification, and cap/cancel stalled requests. SFTP namespace and tmux working-directory paths must agree; otherwise report working-directory mapping unavailable and offer an explicit remote directory selection rather than mapping paths by guesswork.

For remote editing, read through a bounded private local spool when needed to provide a stable body, length and content-derived revision. A revision includes a digest of the exact bytes plus available metadata; re-read/hash before commit to detect same-size changes within coarse timestamp resolution. This is optimistic detection, not a server-side compare-and-swap. Recheck existence, type and observed metadata immediately before replacing, including forced saves. A replacement with indistinguishable contents/metadata cannot be detected as a different inode; disclose that limit. Serialize conflicting operations from this Hub, including parent delete/rename conflicts, but do not claim to lock out remote processes.

Stage uploads in exclusively created, unpredictable, owner-only files in the target directory. Validate length/limits and close successfully before commit. Enable overwrite only when the server advertises and passes tests for POSIX replacement; use no delete-then-rename or truncate-in-place fallback. Preserve current permission bits and owner/group where permitted, report metadata preservation failures, and explain that ACLs/xattrs and other hard-link names do not follow replacement. Where link count is unavailable, show the limitation and require explicit acknowledgement before replacing rather than claiming hard-link detection.

Publish a capability/profile warning when remote editing is first used; do not silently impose local guarantees on the SFTP backend. Unsupported replacement leaves browse/download and supported entry operations usable, with overwrite clearly disabled. Lack of server-side fsync means commit acknowledgement is not a promise of survival through remote power loss.

Connection loss after commit was sent yields `outcome_unknown`: keep the editor dirty and reconcile by reading the target, never mark saved solely because reconnect succeeded. Track only Hub-created staging paths with ownership tokens and cleanup metadata. Attempt cleanup on failure; reconnect/restart cleanup removes only identifiable owned staging entries, never arbitrary matching filenames. Report pending cleanup when the server is unreachable instead of promising immediate removal. Replacement acknowledgement and cleanup recovery must be tested against a real disposable OpenSSH SFTP server.

### 7. Browser state and compatibility

Add a host selector and host administration view; show sessions for the selected host and identify the host in terminal headers, file managers and destructive confirmations. Keep one visible terminal as today. Host switches retain existing terminal/file state without sending input to the wrong host. Store session state under structured host/name keys and file state under host/revision/path keys; protect all late responses with request epochs and runtime revisions. Scope batch termination to the selected host.

Keep drafts when a host disconnects or is edited; invalidate old file observations and require reload/reconciliation before saving to a changed runtime. Host removal requires explicit confirmation and handling of dirty editors before their state is discarded. Backend removal from another browser leaves drafts available for local copying but disables remote actions. Migrate old ordering/pins into the `local` namespace once. Do not persist SSH secrets in browser storage.

Keep `/api/client-config` public and free of host topology. Fetch host capabilities after authentication. Global `files.enabled: false` disables all local and remote file operations before filesystem or SFTP work; existing `files.roots` stays local, while size/list limits apply to both backends. Host capability failures do not log the user out.

## Risks / Trade-offs

- SFTP cannot provide the existing local inode/root-handle contract -> publish separate remote semantics, gate overwrite capability, preserve local tests, and require integration evidence before shipping.
- SSH commands are shell text -> constrain supported remote shells and test hostile but valid arguments end to end, including tmux target separators.
- Lost responses can hide committed mutations -> distinct uncertainty errors, no automatic replay and editor reconciliation.
- Central credential store broadens the impact of shared-token compromise -> keep the agreed single-admin model explicit, encrypt secrets at rest, verify host keys and document HTTPS deployment.
- Large file transfers can affect interactive latency -> isolate file transports, bound concurrency and measure terminal responsiveness during transfer.
- Host edits race active operations -> revisioned leases, cancellation and refusal of stale work; cancellation does not imply rollback of a mutation already sent.
- Default-shell and SFTP namespace differences reduce compatibility -> report unsupported capabilities precisely, test the supported Linux/macOS/OpenSSH matrix and document limits.

## Migration Plan

1. Introduce host-aware routing behind the built-in local runtime and prove existing local behavior before enabling remote adapters.
2. Add versioned storage and lazy first-use initialization; document explicit durable mounts and backup of both registry and key. Back up before migrations and refuse newer schemas without altering them.
3. Add SSH session/terminal and SFTP adapters, then the Web enrollment and host-scoped workspace; release them together after acceptance tests.
4. Migrate browser ordering into `local`; preserve local URLs, token flow and configuration defaults.
5. Rollback stops the new Hub and runs the previous binary with previous YAML. Retain remote state/key files untouched for re-upgrade; older binaries only manage local sessions. Remove new YAML fields when rolling back because parsing is strict. No rollback step kills remote or local tmux sessions.

## Reference Checks

- [Go SSH API](https://pkg.go.dev/golang.org/x/crypto/ssh): host verification and PTY/window-change primitives; TCP dial timeout alone does not bound the complete workflow.
- [Go SFTP API](https://pkg.go.dev/github.com/pkg/sftp): POSIX rename support and available directory/read APIs; exact bounded-iteration and cancellation behavior must be checked for the pinned version.
- [OpenSSH protocol extensions](https://github.com/openssh/openssh-portable/blob/master/PROTOCOL): extension negotiation, POSIX rename and fsync. Atomic replacement is distinct from conditional replacement and durable commit.

Dependency versions, default concurrency numbers and visual layout details remain implementation choices constrained by these contracts, not unresolved scope decisions.
