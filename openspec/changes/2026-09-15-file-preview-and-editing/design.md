## Context

See `proposal.md` — Why. Constraints that shape this design:

- The hub is a **single static binary** with the frontend embedded (`hub/web/embed.go` `//go:embed all:dist`) and no external asset directory. Anything that requires runtime-loaded assets or a sidecar process is a poor fit.
- The Go side has **no filesystem code today** — no `http.Dir`, no `filepath.EvalSymlinks`, no containment helper. The path guard is greenfield.
- The frontend has **no store and no router**: `App.vue` holds all application state, and `TerminalView.vue` delegates to `TerminalSession` from `terminal.ts`. There is no routing layer for a file manager to register with.
- Config changes are expensive by construction: a new YAML section touches nine places across `config.go`, `load.go`, `defaults.go`, and `validate.go`, and the YAML validator rejects unknown keys, duplicate keys, and null values outright. `config_test.go` (920 lines of table tests) will exercise each one.
- Lint is zero-tolerance: `lll` 120, `funlen` 80/50, `gocyclo` 15, `mnd` (no bare magic numbers in business logic), `errcheck` with `check-blank`, and `nolintlint` requiring every directive to name its rule and explain itself.
- `marked` is already a dependency (used by `HelpModal.vue`), so Markdown parsing is free; a sanitizer is not.
- Frontend tests run under `node --test --experimental-strip-types` against source, with `hub/web/test/loader.mjs` redirecting `@xterm/*` to hand-written stubs. There is no DOM in the test environment.

## Goals / Non-Goals

**Goals:**
- A single, small, auditable choke point for path authorization, so that every filesystem operation provably goes through it.
- Reads and writes that behave correctly for real files (multi-megabyte logs, source trees) rather than demo-sized files.
- Writes that cannot corrupt data under concurrency or interruption, with the failure mode surfaced to the user rather than silently resolved.
- Frontend integration that fits the existing `App.vue`-centric model without introducing a state management layer for one feature.

**Non-Goals:**
- Upload. Deferred: multipart streaming, its own size-bound and resumability story, and drag-and-drop are a separable transport concern.
- Git diff preview. Deferred: a second rendering backend that depends on repository state, not just file bytes.
- Any remote-host file protocol. This repo has no outbound agent protocol (`capability 'files'`, relayed `listDir`/`readFile`), and building one is a separate project.
- Mobile-specific layout (single-column browse/edit switching, font stepper). The reference implementation added this in a separate follow-up change; the same split applies here.
- Syntax highlighting fidelity beyond what CodeMirror 6 provides out of the box.

## Decisions

### 1. In-hub service package, not a sidecar process

File operations live in a new `hub/internal/files` package (service + path guard + sentinel errors) with HTTP handlers in `hub/internal/transport/http/files.go`, mirroring the existing `session` + `transport/http` split. `app.New` wires it as a fourth constructor argument alongside `sessionService` and `ticketStore`.

*Alternatives considered:* a separate filesystem agent process the hub talks to over a socket — rejected as pure overhead with no isolation benefit here, since the hub already runs as the same user and already spawns tmux; the reference project's agent indirection exists only to support *remote* hosts, which we are not building.

### 2. Route shape `/api/hosts/{hostId}/files/...`

The repo already namespaces everything under `/api/hosts/{hostId}/...` with `hostId` pinned to the literal `local` by `requireLocalHost` (`router.go:50`). Reusing that shape keeps one authorization story and one URL vocabulary. The reference project's `/api/files/:hostId/...` shape reflects its multi-host reality and would be misleading here.

| Method + path | Purpose |
|---|---|
| `GET /api/hosts/{hostId}/files/list?path=` | Directory listing |
| `GET /api/hosts/{hostId}/files/read?path=` | Streamed read |
| `GET /api/hosts/{hostId}/files/download?path=` | Streamed read with attachment disposition |
| `PUT /api/hosts/{hostId}/files/write?path=&size=&expected_mtime=` | Write, raw `application/octet-stream` body |
| `POST /api/hosts/{hostId}/files/create` | Body `{path, kind}` |
| `POST /api/hosts/{hostId}/files/rename` | Body `{path, new_path}` |
| `POST /api/hosts/{hostId}/files/delete` | Body `{path, recursive}` |
| `GET /api/hosts/{hostId}/sessions/{name}/working-directory` | Active pane cwd |

`read` and `download` share one handler that differs only in `Content-Disposition`; splitting them keeps the browser side honest about intent and makes the download path independently testable.

*Alternatives considered:* POST-for-everything as the reference does. Its stated reason is URL length limits on long paths — a real concern, but it is bounded here: paths are capped at 4096 characters and Go's default header limit is 1 MiB, so a query string comfortably fits. Rejected in favour of matching this repo's existing RESTful routes (`PATCH`/`DELETE` on sessions) and getting cache-correct, curl-friendly streaming reads.

### 3. Canonical-path containment as the single choke point

Every filesystem operation resolves its target through one guard. The guard:

1. Rejects non-absolute paths and any path containing a `..` segment.
2. Rejects `/proc`, `/sys`, and `/dev` **unconditionally, before the root check**, so no root configuration can expose them.
3. Calls `filepath.EvalSymlinks` on the target and checks containment against the canonical roots.
4. For a target that does not exist yet, resolves the **parent** canonically, re-appends the remaining segment, and validates that — so a write can neither follow a symlinked parent out of a root nor create a file at a resolved location outside one.

Containment is a relative-path test (`filepath.Rel` plus "not `..` and not absolute"), not a string-prefix test, which avoids the classic `/home/user` matching `/home/user-backup` bug.

*Alternatives considered:* lexical `filepath.Clean` containment alone — rejected outright; it is exactly the check a symlink defeats, and the reference project's hardening change exists because that mistake was made. `os.OpenRoot`/`openat`-style descriptor-relative access would be stronger still, but it does not cover the listing and `stat` paths uniformly and would make the guard harder to review than the `EvalSymlinks` approach it replaces.

### 4. Boundary defaults to the OS user's own access; roots narrow it

`files.roots` is an **optional** restriction. When it is absent the boundary is whatever the hub process's operating-system user can already reach — no directory containment at all. When it is configured, every operation is confined to those canonicalized roots, and a root that does not exist or is not a directory is a startup error.

This reverses an earlier draft of this design, which defaulted the root to the hub user's home directory. The reference project shipped exactly that default and then **deliberately removed it** (`openspec/changes/archive/2026-08-25-default-unrestricted-file-paths`):

> TmuxHub users who can open a fully interactive terminal on one of their hosts can already read and write every regular path available to the Agent's operating-system user. Requiring the file API to stay under the home directory or a currently open pane directory **does not create an effective security boundary for that same user**. Instead it causes otherwise valid File Manager, CLI and application operations to fail when a project is outside those inferred roots.

That argument applies here unchanged. This hub issues a bearer token that authorizes *both* the file API *and* a byte-faithful interactive terminal as the same OS user. Anyone holding the token can run `cat /etc/passwd` in the terminal regardless of what `files.roots` says. So a `$HOME` root does not withhold any capability from an attacker who has the token — it only breaks legitimate work on projects under `/srv`, `/data`, `/var/log`, or `/opt`.

*Alternatives considered:* a `$HOME` default (the earlier draft, and the reference's original design). Rejected for the reason above — it is real friction purchased with an illusory boundary. Deny-by-default with a required allowlist, which is what the reference does for its *local hub host*. That is the right posture for their shared deployment where a hub serves many users; it is the wrong default for a single-user loopback tool, and it would make the feature dead on arrival. Note the reference's two tiers are not in conflict — its remote-agent tier is unrestricted-by-default with an optional allowlist, which is precisely what this decision adopts, because visual-tmux-client is architecturally the single-host case.

### 5. Pane working directory is a navigation seed, never an authorization input

The browser asks for the session's active pane working directory when the manager opens, uses it as the initial directory, and then lets the user navigate freely — including going up to a parent and selecting any directory in the tree as the current one. Only the initial grab is one-shot; changing the active pane afterwards does not move an already-open manager.

This distinction matters because the reference **deleted** pane-directory-based authorization in the same change that removed the `$HOME` default, on the grounds that it was "no longer part of authorization". An earlier draft of this design said the manager's directory would be "fixed for the lifetime of the opened manager", which was meant to express "don't follow the pane" but would also have frozen navigation. The spec now separates the two concerns explicitly.

*Alternatives considered:* re-resolving the pane directory on every pane change — the tree would be invalidated under the user mid-task. Treating the pane directory as an authorization root — removed by the reference for good reason, and unnecessary once decision 4 is adopted.

### 6. Streaming reads as the primary path

`read`/`download` stream via `http.ServeContent`-style copying with `Content-Length`, `X-File-Size`, `X-File-Mtime`, and `Cache-Control: no-store`. Body size is bounded by the configured `files.max_file_size` (default 100 MiB), checked via `stat` **before** any bytes are sent so an oversized read fails cleanly.

The reference implementation makes a 1 MiB JSON/base64 fallback its compatibility path and streams beyond it; here the streamed path is the only path. A 1 MiB ceiling would make the feature feel broken on exactly the files people want to open — real logs.

*Alternatives considered:* base64-in-JSON for everything. Rejected: 33% inflation, whole-file-in-memory on both ends, and a ceiling low enough to be useless.

### 7. Atomic writes with `expectedMtime` optimistic concurrency

A write carries the modification time the caller last observed. Order of operations:

1. Authorize the path through the guard.
2. Compare `expected_mtime` against the current file's mtime; mismatch → `conflict`, nothing written. If `expected_mtime` is supplied but the file does not exist → `not_found` (never silently create — a caller that thinks it is editing an existing file must not be given a new one).
3. Create a sibling temp file with `O_EXCL|O_CREATE|O_WRONLY`, mode `0600`.
4. Copy the body, enforcing both the configured maximum and the declared `size`; mismatch → `invalid_body`.
5. `Sync()`, `Close()`, then `Rename()` over the target.
6. On any failure, remove the temp file.

Omitting `expected_mtime` forces an overwrite — this is what the browser sends after the user confirms the conflict prompt, and it is the only way `create`-like overwrite happens.

**Modification times travel as integer milliseconds since the epoch**, and a successful write returns the target's resulting modification time. The reference sends whole seconds and has the browser optimistically assume `Math.floor(Date.now() / 1000)` after a save. That is wrong twice over: a file written at second boundary can carry an mtime one second below what the browser assumed, producing a spurious conflict on the very next save. Returning the real mtime removes the guess, and millisecond precision shrinks the same-timestamp blind spot from one second to one millisecond. Nanoseconds would be finer still but are unusable — Go's `UnixNano()` exceeds JavaScript's `Number.MAX_SAFE_INTEGER` and would be silently corrupted by `JSON.parse`.

*Alternatives considered:* last-write-wins. Rejected: with several browser tabs on one hub, it silently destroys work, and the failure is invisible until much later. `O_EXCL`-only creation with no overwrite was also rejected — it makes editing a file that another process touches a dead end. Locking was rejected as overkill and unenforceable against editors outside the hub. Sticking with second-granularity mtimes was rejected once the round-trip is already available: conforming to the reference here would import a known false-conflict bug for no benefit.

### 8. Directory listing bounds

Listings return `name`, `is_dir`, `size`, `mtime`, ordered directories-first then by name, capped at `files.max_dir_entries` (default 1000, hard cap 10000) with a `truncated` boolean. Only files are `stat`'d; directories report size 0. Unreadable entries are reported as existing without failing the whole listing.

*Alternatives considered:* unbounded listings. Rejected — a single `node_modules` or a 100k-file directory would produce a multi-megabyte JSON response and a hung browser tab.

### 9. CodeMirror 6 rather than Monaco

The reference idle-loads Monaco behind a dynamic `import()`, so it is **not** in the main bundle — an earlier draft of this design leaned on raw bundle size, and that argument does not survive contact with their code. The real costs are elsewhere:

- Monaco needs worker assets wired through the bundler. The reference's `MonacoEditorPanel` carries a `loader.config({ monaco })` call with a comment recording that the CDN default fails with a worker MIME-type error — a configuration the project has to keep working indefinitely.
- It needs targeted platform workarounds: their panel injects `.monaco-editor textarea.inputarea { font-size: 16px }` because iOS Safari auto-zooms on form controls below 16px.
- Its integration surface is materially larger for the same feature set.

CodeMirror 6 is ESM-native with no worker plumbing, lazy-loads language packs via dynamic import, and needs no global loader configuration. The `EditorTabs`/dirty-tracking/save-flow behavior carries over unchanged from the reference; only the editor binding differs. The reference's own `EditorTabs` turns out to be about 30 lines with no keyboard handling beyond a document-level Ctrl/Cmd+S inside the editor panel — so there is very little editor-specific behavior to match.

*Alternatives considered:* Monaco for exact parity — rejected on worker/build configuration and mobile quirks rather than on bytes; if the project later wants minimap or deeper language intelligence, the binding is isolated in one component and can be swapped. A bare `<textarea>` with highlight-only preview — rejected as too weak to call "editing".

### 10. Sanitized Markdown, and no HTML preview

Markdown renders through `marked` (already present) into `DOMPurify.sanitize(html, {USE_PROFILES: {html: true}})`. The browser never inserts raw file content as markup, and there is no HTML preview at all — opening a `.html` file shows its source in the editor. Rendered Markdown is capped to a bounded prefix of the source and annotated when truncated; the cap applies to the in-memory buffer, not to a separate read, because the whole file is already in the editor. This matches the reference, which truncates rather than refusing and appends a notice.

*Alternatives considered:* shipping without sanitization — not defensible, since Markdown files are frequently attacker-influenced in this exact workflow. Sanitizing with a hand-rolled allowlist — rejected; `DOMPurify` is the maintained implementation and one small dependency. Refusing to render oversized Markdown (an earlier draft) — rejected in favour of truncate-with-notice, which still shows the user most of the document.

### 11. Frontend: overlay owned by the terminal region, state in one component

A `files` button joins the existing terminal header button row in `App.vue` (alongside `A−`/font size/`A+`/`⛶`/`✕`). `App.vue` gains only a boolean open-state and the resolved root directory; all manager state (open tabs, tree expansion, dirty set) lives inside `FileManagerOverlay.vue` and is discarded on close. This deliberately does not introduce a store or router.

New modules under `hub/web/src/files/`: `api.ts` (reusing `authFetch`/`errorText` from `api.ts`), `pathUtils.ts`, `binaryExtensions.ts`, `preview.ts` (pure dispatch logic + the sanitizing render), and the components `FileManagerOverlay.vue`, `FileTree.vue`, `FileTreeNode.vue`, `EditorTabs.vue`, `CodeEditor.vue`, `ImagePreview.vue`, `MarkdownPreview.vue`, `FileContextMenu.vue`.

The overlay is positioned **within the terminal region**, not over the whole app — the recent notification fix (`03e5692`) established that overlays in this app must not escape the terminal area.

*Alternatives considered:* a new route/page for the file manager. Rejected: there is no router, and per-session context (the pane cwd) is naturally available only inside the terminal view.

### 12. Pane working directory endpoint

The browser requests `GET /api/hosts/{hostId}/sessions/{name}/working-directory` when the manager is opened, and uses the result as the initial directory. The hub answers it with `tmux display-message -p -t =<name> '#{pane_current_path}'`, using the existing `ExactTarget` helper so the session name is never treated as a prefix. This is the navigation seed of decision 5, not an authorization input.

This requirement is specified inside the `file-manager` capability rather than `session-hub` because it exists solely to serve the file manager; should a second consumer appear, it belongs in `session-hub`.

*Alternatives considered:* putting the working directory on the session list DTO — would cost a tmux invocation per session on every 2-second poll, which is exactly what the existing `fastGetter` optional-capability idiom exists to avoid.

### 13. Error vocabulary and central mapping

The wire error codes are `invalid_path`, `path_not_allowed`, `not_found`, `permission_denied`, `file_too_large`, `conflict`, `invalid_body`, `dir_not_empty`, `write_failed`. They map to HTTP statuses in one `mapFileError` function next to the existing `mapSessionError` (`router.go:58`), keeping the `{"error": "<code>"}` envelope. The browser matches on the code string, not the status.

*Alternatives considered:* free-form messages. Rejected — the conflict flow in the browser depends on reliably distinguishing `conflict` from other failures.

### 14. Config wiring follows the nine-touchpoint convention

A `files` section with `enabled` (bool), `roots` ([]string), `max_file_size` (int bytes), and `max_dir_entries` (int). Pointers in `rawFilesConfig` distinguish "omitted" from "explicitly zero"; both scalars are classified as integer fields in `validate.go`; a cross-field check rejects non-positive limits. The example YAML and both READMEs document it.

*Alternatives considered:* reading these from environment variables only, to avoid the nine touchpoints. Rejected — the repo just finished centralizing configuration precisely to stop scattering runtime parameters, and env vars bypass the provenance and validation machinery.

### 15. The write route gets its own body bound

The global `MaxRequestBodyBytes` (64 KiB, `router.go:17`) is far too small for a file write. The `PUT .../write` handler installs its own `http.MaxBytesReader` derived from `files.max_file_size` plus a small allowance, rather than raising the global limit — raising it would weaken every other endpoint.

*Alternatives considered:* raising the global limit. Rejected: it would let an unauthenticated-sized body be read on every other route.

### 16. Path insertion reuses the existing attachment, with no new server surface

"Insert path into terminal" needs to put text into a session's input. This repo already has exactly that channel: the `TerminalSession` in `terminal.ts` holds an open WebSocket to the session and writes keystrokes to it. The file manager opens *from* that terminal, so the attachment is live by construction. Insertion therefore requires **no new endpoint, no `tmux send-keys` route, and no protocol change** — the manager asks `App.vue` to write the path bytes through the existing attachment.

Two safety properties are specified: the inserted text never contains a line terminator, so an insertion can never execute anything; and a path containing characters a shell would interpret is quoted, so the inserted text denotes the path literally rather than being subject to word splitting or expansion.

The reference ships this only on mobile (added by `archive/2026-06-24-file-manager-mobile-adaptation`), where the motivation is the absence of a hardware keyboard. The motivation here is different and applies on desktop too — dropping a long path into a command without retyping it — so it is not gated by viewport.

*Alternatives considered:* a REST endpoint running `tmux send-keys -l`. Rejected as strictly worse: it would need its own authorization story, would go through tmux rather than the attachment the user is actually looking at, and would work even when the browser has no terminal open — permitting input injection with no visible terminal, which is harder to reason about. `<textarea>`-style insertion into the terminal's DOM. Rejected: xterm has no such input element.

## Risks / Trade-offs

- **[Risk] The path guard is the entire security model, and a gap in it is a filesystem disclosure bug.** → *Mitigation:* the guard is one function with table-driven tests covering traversal, symlinked parents, symlinked targets, roots that are themselves symlinks, `/proc`-under-an-explicit-`/`-root`, and the create-nonexistent-parent case. Lint's `gocyclo`/`funlen` limits keep it small enough to review in one screen.
- **[Risk] `EvalSymlinks` is a TOCTOU window: a symlink swapped between check and open could redirect the operation.** → *Mitigation:* accepted. Exploiting it requires an attacker with concurrent write access to the very directories the user has already granted, which is a strictly weaker position than the access they are being denied. Closing it fully needs `openat`-relative access throughout; noted, not built.
- **[Risk] A large file read blocks a browser tab or a slow client.** → *Mitigation:* size is checked before any bytes are sent, listings are bounded with a `truncated` flag, and preview bounds keep the browser from rendering anything huge.
- **[Risk] Adding CodeMirror 6 and DOMPurify breaks `npm test`,** because `hub/web/test/loader.mjs` mocks browser-only dependencies and the test environment has no DOM. → *Mitigation:* add both to the loader's mock map; keep `preview.ts`'s dispatch logic pure and DOM-free so it is unit-testable, and assert the sanitizer is invoked via a source-contract test as the reference project does. Explicitly documented as a real limitation: sanitization itself is not behaviorally unit-tested in this repo's current test setup.
- **[Risk] The overlay and the terminal fight over keyboard and mouse input** (xterm captures keys; tmux mouse mode is on). → *Mitigation:* the overlay takes focus on open and releases it on close, and the manager is measured against a real session in verification. This repo has already been bitten by input-focus issues once.
- **[Trade-off] By default there is no directory containment at all**, so a hub running as a shared or service account exposes everything that account can read — including `/etc` and other users' home directories where permissions allow. → *Mitigation:* this grants nothing the token does not already grant through the terminal (decision 4), and operators who want containment configure `roots`. Both READMEs and the example YAML state the default plainly rather than describing it as "home only".
- **[Trade-off] No upload means the manager cannot move files into a host** — a common reason to open one. → *Accepted for this iteration;* tracked as the immediate next step.

## Migration Plan

Additive only. With no `files` section in YAML the section takes its defaults, the feature is available, and every existing route, response shape, and WebSocket message is unchanged. Rolling back is reverting the release; no persisted state is introduced, and no existing file is modified by the hub outside an explicit user action.

## Open Questions

- Whether `files.enabled: false` should also hide the toolbar button entirely or leave it disabled with an explanation. Leaning toward hiding it, since a disabled button with no explanation invites support questions; the decision does not affect the specs or the task breakdown.
- Whether the manager should remember the last directory per session across reconnects. Deferred; the spec pins the directory to open time, and persisting it would be an additive browser-side change.
