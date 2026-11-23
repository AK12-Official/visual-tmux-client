## Why

Users should be able to launch Visual Tmux Client from the Linux or macOS desktop and use its existing interface without opening a browser or copying an access token. The existing Vue UI and Go HTTP/WebSocket backend already provide the required functionality; a desktop host can reuse them while owning startup, authentication and shutdown.

## What Changes

- Add a Wails v3 desktop entry that runs the existing Go backend in-process and loads the embedded frontend through a private loopback listener with a dynamically assigned port.
- Automatically authenticate the owned window while retaining bearer authentication, host-bound terminal tickets and origin checks for backend requests.
- Persist desktop UI preferences independently of the listener port, with credentials kept out of preference storage.
- Provide desktop-compatible download, clipboard, fullscreen and external-link behavior without replacing Vue, xterm.js, CodeMirror, HTTP or WebSocket.
- Define bounded application shutdown, unsaved-edit confirmation, startup diagnostics and predictable configuration/tmux discovery when launched from a desktop icon.
- Add separate Linux and macOS desktop build/package validation while preserving the existing headless command and its release artifacts.
- Gate desktop release readiness on real WebView validation on both platforms, including terminal input/output and tmux session survival.

## Capabilities

### New Capabilities

- `desktop-application`: Desktop launch, owned backend authentication, port-independent preferences, native integration, application lifecycle and Linux/macOS distribution.

### Modified Capabilities

None. Existing specifications continue to describe the existing server command and ordinary browser entry. The new desktop entry reuses their service and protocol guarantees, with its launch-specific configuration, credential handoff and desktop integration defined in the new capability. Existing browser login, startup credential disclosure and configuration precedence remain unchanged for the server command.

## Impact

- Add a desktop entry under `hub/cmd/visual-tmux-desktop` and testable orchestration under `hub/internal/desktop`; adapt `hub/internal/app` only where required for lifecycle and desktop-only handlers.
- Add a small frontend platform/bootstrap adapter around `hub/web/src/main.ts`, authentication, preference storage and browser-dependent actions; keep the existing embedded asset bundle and terminal stream.
- Add desktop-only authenticated preference/bootstrap endpoints and a narrow native action bridge; do not migrate session/file/host APIs to framework bindings.
- Add Wails/native build dependencies, isolated from the headless build through build tags or an equivalent explicit package boundary. Update CI and packaging for native Linux/macOS builds and document system WebView and tmux requirements.
- Reuse the existing host database, encryption key, single-owner lock, file limits and shutdown budgets. A running headless instance owning the same state remains an actionable startup conflict, not a second backend to silently connect to.
- No product code is changed by this proposal. Initial release baseline is Ubuntu 24.04 amd64 and macOS 12+ arm64/amd64, subject to actual supported runner/toolchain validation; broader Linux distributions/architectures are outside the initial desktop acceptance matrix.
