## 1. Prove native container compatibility

- [x] 1.1 Add an implementation verification record with the Ubuntu 24.04 amd64 Wayland/X11 and macOS arm64/amd64 matrix, runtime/toolchain versions, commands and pending evidence; verify unexecuted combinations are explicitly pending.
- [x] 1.2 Add a minimal desktop-tagged Wails v3 entry using the existing embedded frontend and injected fake services; pin the tested framework/toolchain versions and verify native Linux and macOS builds while the original CGO-disabled headless command still builds without desktop libraries.
- [ ] 1.3 Exercise loopback external-URL loading, reload, Bearer-authenticated exact-origin HTTP native-action calls, native save chooser and byte-preserving download in real Linux/macOS WebViews; record evidence and stop for a design revision if the selected host cannot provide these integration points.

## 2. Establish desktop configuration and backend ownership

- [x] 2.1 Add desktop configuration loading with OS-user implicit discovery, explicit file selection, configuration-relative paths and desktop-managed address/token/origin; verify precedence, malformed config, working-directory independence and secret-redacted diagnostics with table-driven tests while existing config tests remain green.
- [x] 2.2 Add desktop tmux executable resolution using explicit config, PATH and documented macOS fallback locations without a shell; verify resolution order and actionable missing-local-tmux behavior using injected lookups, with remote host access unaffected.
- [x] 2.3 Add the testable desktop controller around `app.New/Start/Addr/Shutdown`, optional desktop-only handler registration and a readiness check; verify startup order and cleanup on state, listener, readiness and window-construction failures using fake services/native adapters.
- [x] 2.4 Wire the native main-thread window to the controller, observe backend failures and add actionable startup/runtime error surfaces; verify the window loads the actual dynamic port and backend failure releases the listener and registry lock.
- [x] 2.5 Add single-instance focus behavior for the same desktop profile and retain existing registry ownership enforcement; verify a repeated desktop launch focuses its window and a headless state owner produces a conflict without modifying or stopping that owner.
- [x] 2.6 Keep desktop imports behind explicit native build boundaries and implement help/version without GUI or state initialization; verify ordinary `go test ./...`, CGO-disabled headless builds and informational desktop flags require no display or state store.

## 3. Implement launch authentication and frontend initialization

- [x] 3.1 Add fresh desktop bearer generation and a bounded one-use launch-code exchange with exact bound Host/Origin, JSON/body-size validation and no-store responses; verify success, expiry, reuse, malformed/foreign requests, no-secret logging and absence of desktop handlers in ordinary server mode.
- [x] 3.2 Split `main.ts` into minimal platform bootstrap and deferred application imports; consume/remove the launch fragment before initialization and hydrate desktop credentials before protected requests; verify bootstrap ordering, rejected exchange and unchanged ordinary browser login in frontend tests.
- [x] 3.3 Add desktop document-session credential recovery and native-controlled fresh-code reload while keeping tokens out of durable preference storage; verify reload succeeds once, stale tokens show recoverable initialization errors and session/host failures retain their existing classification.
- [x] 3.4 Verify auth boundaries through HTTP/WS integration tests: bare URL and unauthorized clients receive no credential, public client config remains secret-free, protected routes require bearer and terminal tickets retain host/revision/session binding and origin rejection.

## 4. Persist desktop preferences across dynamic ports

- [x] 4.1 Add the versioned, private preference store with a 1 MiB total bound, validated key/value limits, atomic replacement and serialized writes; verify corrupt/unsupported files, write failure, interrupted replacement, unknown keys and credential/draft exclusion using temporary state directories.
- [x] 4.2 Add desktop-only authenticated snapshot read/update APIs with version/conflict handling; verify only current authenticated callers can read/write allowed preferences and rejected updates leave the prior durable snapshot intact.
- [x] 4.3 Install a seeded synchronous preference adapter before i18n/application module evaluation and queue serialized durable updates; verify first-render locale, configured font clamping, sidebar/recent host restoration, host-scoped ordering/pins and unchanged browser localStorage behavior.
- [x] 4.4 Add visible persistence errors and bounded pending-write flush for normal quit; verify rapid consecutive updates preserve the latest value, flush failure is reported and an unwritable store leaves terminals and editor drafts usable.
- [x] 4.5 Exercise a complete preference-change, normal-quit and restart sequence with a different listener port; record that all specified preferences restore and no credentials appear in `desktop-preferences.json`.

## 5. Adapt native interactions without changing transport

- [x] 5.1 Introduce the small frontend platform action interface with existing browser behavior as its default; verify ordinary browser download, clipboard and fullscreen call paths continue passing their existing checks.
- [ ] 5.2 Implement desktop save-dialog download using the original host/revision, existing source authorization/limits and atomic destination completion; verify byte equality, disabled/out-of-bound/oversized sources, cancellation, overwrite confirmation, host switching, obsolete revision and failed destination writes without partial replacement.
- [ ] 5.3 Implement Bearer-authenticated exact-Origin/Host HTTP native-action restrictions and external-link routing; verify activated HTTP/HTTPS links open the system browser, unsupported schemes are rejected and foreign or absent origins cannot invoke native actions.
- [ ] 5.4 Adapt clipboard only where the native WebView needs it and configure menus to preserve terminal shortcuts; verify selected text copies, paste and Chinese composition work, Ctrl+C without selection reaches the terminal and copy failures are visible on both systems.
- [ ] 5.5 Integrate terminal fullscreen/window changes while retaining measured resize and binary WebSocket streaming; verify fullscreen entry/exit, rapid resize, mouse/trackpad scroll and continuous styled/Unicode output in Linux Wayland/X11 and macOS WebViews.

## 6. Complete quit and failure lifecycle

- [x] 6.1 Add one idempotent controller state machine for close, Quit/Cmd+Q, signal, startup failure and backend failure; verify overlapping events perform cleanup once with bounded contexts and do not depend on native `Run()` returning.
- [ ] 6.2 Connect user-initiated quit to editor dirty-state confirmation and preference flush; verify cancel preserves drafts/attachments, confirmed discard proceeds and the only window closing exits the application on both platforms.
- [x] 6.3 Verify partial-start failure, normal quit and signal cleanup release HTTP/file/SSH/registry resources using injected failures and isolated integration fixtures; ensure diagnostics do not contain launch or bearer credentials.
- [x] 6.4 Verify quit/relaunch preserves local and remote tmux sessions using existing disposable-server/SSH fixtures; any new tmux-starting package must install `testutil.ParentGuard`, use `testutil.SocketDir`, scrub child TMUX/TMUX_PANE, create private TMUX_TMPDIR, name an explicit socket and clean up through the same helper. Run outside tmux and record survival/reattachment results without ad hoc tmux commands or unrelated server cleanup.

## 7. Build packages and document operation

- [x] 7.1 Add a native Ubuntu 24.04 amd64 desktop build/package job with declared GTK4/WebKitGTK runtime dependencies, DEB launcher/icon and embedded assets; verify clean installation and launch without Go/Node or a separately running backend.
- [ ] 7.2 Add native macOS arm64/amd64 build jobs and architecture-specific `.app` ZIP artifacts with a documented deployment target; verify bundle architecture/assets, launch from Finder with a minimal PATH and missing-tmux diagnostics.
- [ ] 7.3 Add optional credential-driven macOS signing/notarization and explicit unsigned-development labeling; verify unsigned CI remains usable without signing secrets and signed release claims are gated by signature/notarization checks.
- [x] 7.4 Preserve existing headless CI/release jobs and add desktop-tagged checks without requiring native libraries in ordinary tests; verify all four existing headless artifacts and their version metadata remain produced.
- [ ] 7.5 Update both READMEs and desktop build/verification documentation for launch modes, managed transport settings, config/state paths, shared-state conflicts, dependencies, quit/unsaved-edit behavior, signing status and rollback; verify examples against packaged behavior and list only actually validated support.

## 8. Validate the complete desktop workflow

- [ ] 8.1 Run existing frontend tests/build and appropriate Go tests/vet/lint plus desktop-tagged checks; record commands and results, observing repository tmux isolation rules and leaving unrelated sessions untouched.
- [ ] 8.2 Complete the real-WebView acceptance matrix for automatic auth/reload, local/SSH hosts, host trust repair, file edit/preview/save/download, Chinese input, clipboard, scrolling, resize/fullscreen, sustained output/reconnect, preferences, dirty quit and tmux survival; record each tested OS/architecture/display combination and keep unavailable combinations unchecked.
- [ ] 8.3 Install packaged artifacts on the target systems and verify launcher startup, dependency reporting, bounded exit, next-launch state-lock availability and regression-free headless operation; mark desktop release readiness only when the required matrix and packaging evidence are complete.
