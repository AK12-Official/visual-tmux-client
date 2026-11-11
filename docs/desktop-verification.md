# Desktop implementation verification

This is an execution record for `add-desktop-application`, not a claim of released desktop support. A checkbox in the implementation plan is completed only when all of its specified evidence exists. Native builds, actual WebView behavior and package installation are separate checks.

## Target matrix

| Target | Native build | Real WebView integration | Installed package | Status |
| --- | --- | --- | --- | --- |
| Ubuntu 24.04 amd64, Wayland | Pending | Pending | Pending | Not validated |
| Ubuntu 24.04 amd64, X11 | Passed: tagged spike compiled | Automatic auth/workspace/reload passed via HTTP action; remaining GUI checks pending | Pending | Compatibility gate not passed |
| macOS arm64, documented minimum 12+ | Pending; no macOS execution environment available | Pending | Pending | Not validated |
| macOS amd64, documented minimum 12+ | Pending; no macOS execution environment available | Pending | Pending | Not validated |

Build evidence on Linux does not establish macOS support. X11 evidence does not establish Wayland behavior. Minimum-version support requires compatible SDK/deployment settings and actual runtime evidence, not just compilation on a newer system.

## Inspected environment

- Host: Ubuntu 24.04.3 LTS, Linux x86_64.
- Go: `go1.27.3 linux/amd64`.
- Node/npm: `v24.12.0` / `11.20.0`.
- C compiler: GCC 13.3.0.
- GTK4 runtime: `libgtk-4-1` 4.14.5+ds-0ubuntu0.6.
- WebKitGTK runtime: `libwebkitgtk-6.0-4` 2.52.6-0ubuntu0.24.04.1.
- Wails: `github.com/wailsapp/wails/v3@v3.0.0` pinned in `hub/go.mod`; native Linux spike compiled with Go 1.27.3 and subsequently Go 1.26.10 (the compatibility CI toolchain).
- Initially missing `pkg-config`, `libgtk-4-dev`, `libwebkitgtk-6.0-dev` are now installed by the user; `pkg-config --modversion gtk4 webkitgtk-6.0` returns 4.14.5 / 2.52.6. No installed `wails3` or macOS `xcrun` executable was found; the spike uses the Go build directly.
- No tmux commands or tmux-backed tests were run for this environment inspection.
- Current desktop session reports X11 with `DISPLAY` available and no `WAYLAND_DISPLAY`; no Wayland execution evidence is available.

## Command evidence

Commands below were run from the repository root unless a directory is stated. Dependencies or display prerequisites must be fulfilled before native build/run commands can provide evidence.

| Command | Result |
| --- | --- |
| `uname -sm` | `Linux x86_64` |
| `cat /etc/os-release` | Ubuntu 24.04.3 LTS |
| `go version` | Go 1.27.3 linux/amd64 |
| `gcc --version` | GCC 13.3.0 |
| `command -v pkg-config` | Not found |
| `dpkg-query -W` for GTK4/WebKitGTK runtime/development packages | Runtime libraries installed; development packages absent |
| `go list -m -json github.com/wailsapp/wails/v3@v3.0.0` in `hub/` | Version resolved; Go language minimum 1.26.0; no native build claimed |
| `sudo apt-get install --yes pkg-config libgtk-4-dev libwebkitgtk-6.0-dev` | Initial agent invocation required an interactive password; user subsequently installed dependencies |
| `pkg-config --modversion gtk4 webkitgtk-6.0` | 4.14.5 / 2.52.6; Linux build prerequisites now available |
| `go build -tags desktop -o /tmp/vtc-desktop-spike ./cmd/visual-tmux-desktop` in `hub/` | Passed, native Linux/amd64 binary |
| `go test ./internal/desktop/spike` in `hub/` | Passed: protected session route uses synthetic backend; mutations and process creation refused |
| `go test -race ./internal/desktop/spike` in `hub/` | Passed |
| `go vet ./internal/desktop/spike` and `go vet -tags desktop ./cmd/visual-tmux-desktop` in `hub/` | Passed |
| `CGO_ENABLED=0 go build -o /tmp/vtc-headless-spike-check ./cmd/visual-tmux-client` in `hub/` | Passed; resulting `--version` prints `dev` |
| `go list ./cmd/...` in `hub/` | Only existing headless command listed; native entry is excluded without desktop tag |
| Initial raw-message version of `/tmp/vtc-desktop-spike --spike --probe` in X11 | Vue mount reports received; missing frame provenance correctly rejected; historical failure resolved below |

### Historical raw-bridge finding (resolved by approved HTTP design)

Task 1.2 has Linux native build evidence, but its required macOS evidence remains pending and its checkbox remains open. Task 1.3 originally exposed the raw-bridge limitation below. The user approved switching native actions to authenticated HTTP, and the Linux workspace/auth/reload probe now passes; the complete cross-platform/dialog gate remains open.

The Linux spike reports:

```text
spike: mount report: reloaded=false origin_exact=false origin_root_url=true main_frame=false top_origin_present=false
spike: mount report: reloaded=true origin_exact=false origin_root_url=true main_frame=false top_origin_present=false
spike: FAIL native load/reload probe timed out
```

The mount report is diagnostic evidence only; the strict handler does not accept it as an authorized native action. No raw message payload or credential is logged. The trailing slash explains `origin_root_url`, but normalization alone cannot supply missing frame provenance.

In Wails v3.0.0, `pkg/application/linux_cgo.go:sendMessageToBackend` obtains the current top-level WebView URI and sets only `OriginInfo.Origin`. `IsMainFrame` and `TopOrigin` are not populated. `pkg/application/linux_child_frame_guard.go` documents this limitation and that non-reserved child-frame messages still reach `RawMessageHandler`. Thus relaxing the frame check would not establish the planned sender-origin guarantee. Reference: https://github.com/wailsapp/wails/blob/v3.0.0/v3/pkg/application/linux_child_frame_guard.go

Approved design adjustment, now implemented: route the narrow native actions through desktop-only authenticated HTTP endpoints with exact bound Origin/Host validation and existing bearer credentials, then call native dialog/clipboard/window APIs on the Go side. Retain Wails as window host and retain terminal WebSocket transport. The raw handler is no longer used. Design/tasks were updated with user approval. macOS build/runtime evidence still requires an actual macOS environment.

## Compatibility gate (tasks 1.2 and 1.3)

Only the evidence explicitly recorded below is complete; a mount observation is not an authorization or full application acceptance result.

| Check | Ubuntu Wayland | Ubuntu X11 | macOS arm64 | macOS amd64 |
| --- | --- | --- | --- | --- |
| Native desktop-tagged build | Not run under Wayland | Passed | Pending | Pending |
| Existing embedded Vue UI loads from dynamic loopback URL | Pending | Passed, authenticated synthetic workspace | Pending | Pending |
| Reload and initialization occur exactly once | Pending | Authenticated reload passed; frontend initialization/write tests passed | Pending | Pending |
| HTTP native action boundary | Pending | Owned-origin action passed in WebView; foreign/missing origin and credentials refused by HTTP tests | Pending | Pending |
| Save chooser, cancellation and byte-equal destination | Pending | Pending | Pending | Pending |
| CGO-disabled original headless build remains independent | Same Linux build, display-independent | Passed | Pending | Pending |

The current command requires explicit `--spike`; without it, it refuses to start. It uses a newly created temporary host-state directory, disables file access and injects a read-only synthetic session backend and refusing process factory. It never uses the user's saved hosts, generates no real terminal process and performs no tmux cleanup. The temporary state is removed through bounded shutdown. Automatic login, reload recovery, preference storage/hydration and the testable backend controller are implemented. Native save-dialog/clipboard wiring and streaming download code compile and have transport tests; actual chooser interaction is still pending. Production entry, quit confirmation/flush, single-instance behavior, platform packaging and complete runtime acceptance remain unfinished.

Use fake backend/session/process services for the initial container spike, a private temporary host-state directory and private file fixtures. Never connect the spike to the developer's real tmux or host database. Follow repository isolation helpers for later tmux tests; clearing `TMUX` alone is not isolation.

## Current implementation and verification

Completed task checkboxes are tracked in `openspec/changes/add-desktop-application/tasks.md`; unfinished multi-platform or lifecycle tasks remain open even where Linux has partial evidence.

- Desktop configuration uses stable user-config discovery, preserves explicit config-relative paths and manages transport independently of headless mode. Tests cover malformed/explicit files, override precedence, working-directory independence, secret-safe errors and informational flags at the loader boundary.
- The controller generates a fresh bearer, owns app startup/readiness/shutdown, wraps only desktop HTTP handlers and releases the registry lock on failure. Fake-backend tests exercise listener/readiness/window failures and concurrent cleanup without starting tmux.
- The HTTP exchange uses a <=30-second one-use fragment code, strict Origin/Host and bounded JSON POST. Tests cover foreign/missing/opaque origins, invalid credentials, expiry, superseded/replayed/concurrent code redemption, body limits and absent desktop bootstrap in ordinary server mode.
- Preferences are bounded, allowlisted, versioned and atomically replaced with private permissions. Tests cover restart, interrupted staging, invalid/corrupt files, excluded token/draft keys, failed writes and revision conflicts. Frontend tests cover hydration before i18n import, serialized updates, document-session recovery and visible persistence failure.
- Desktop file save uses a native chooser callback, captured source host/revision and a staged destination. Source access still goes through the existing authenticated file route, now honoring an optional expected connection revision for downloads. Tests cover byte equality, cancellation, limits, failed transfer, old-destination preservation, disabled access and stale source refusal. Native chooser/overwrite interaction has not been validated live.

Latest local commands/results:

| Command (Go commands in `hub/`, npm in `hub/web/`) | Result |
| --- | --- |
| `go test ./...` with Go 1.27.3 | Passed all packages, including existing isolated tmux regression tests |
| `GOTOOLCHAIN=go1.26.10 go test -race ./internal/desktop ./internal/config ./internal/app ./internal/desktop/spike ./internal/transport/http` | Passed |
| `npm test` | Passed all 31 test files, including new desktop bootstrap tests |
| `npm run build` | Passed type checking and Vite build |
| `GOTOOLCHAIN=go1.26.10 go build -tags desktop -o /tmp/vtc-desktop-spike ./cmd/visual-tmux-desktop` | Passed |
| `/tmp/vtc-desktop-spike --spike --probe` | Passed actual X11 WebView automatic auth, synthetic workspace mount, same-origin HTTP action and reload, then exited normally |
| `GOTOOLCHAIN=go1.26.10 go vet ./...` | Passed |
| `GOTOOLCHAIN=go1.26.10 golangci-lint run --config ../.golangci.yml ./...` | 0 issues |
| Same linter with `--build-tags desktop ./cmd/visual-tmux-desktop/...` | 0 issues |
| Compatibility build with `-ldflags '-X main.defaultSpike=true'`, then `--help` | Built; help confirms synthetic spike defaults to true without starting a GUI |
| `actionlint -shellcheck= .github/workflows/desktop-compatibility.yml` from repository root | Passed |
| Headless cross-builds with `GOTOOLCHAIN=go1.26.10 CGO_ENABLED=0` for Linux/macOS amd64/arm64 | All four binaries built; `go version -m` confirmed OS, architecture and disabled CGO, with no Wails runtime dependency |
| Linux headless `--version` after linking `main.version=desktop-compatibility-check` | Printed `desktop-compatibility-check` |
| `openspec validate add-desktop-application --strict` | Passed |

The installed linter's Go export-data reader could not consume the host Go 1.27.3 cache. Checks were rerun successfully with Go 1.26.10; this did not require weakening lint rules or changing the headless runtime contract. Native GUI probes use fake sessions only. Full Go regressions use the repository's existing disposable tmux test fixtures; green tests are not a claim of universal isolation.

Overall tracked progress is 11/35. The current implementation is deliberately still a disposable compatibility spike. Native user-quit confirmation/flush, normal desktop entry, single-instance focus, complete downloads/clipboard/fullscreen runtime acceptance, production packages and untested platforms remain open. The HTTP architecture change itself is approved and resolved; the remaining gate is execution evidence and unfinished integration, not another request to approve that architecture.

### Prepared remote compatibility workflow

`.github/workflows/desktop-compatibility.yml` adds native Ubuntu amd64 and macOS arm64/amd64 builds, transport-independent race tests, a headless build check, and development-only compatibility artifacts. macOS `.app` ZIPs opt into the synthetic spike at build time; they are ad-hoc signed, without Developer ID/notarization, and do not represent a desktop release. The existing release workflow is unchanged.

Runner labels were checked against GitHub's official runner inventory: https://github.com/actions/runner-images#available-images . The workflow definition has been statically validated locally; remote results must be recorded separately before claiming macOS build success. A pull request touching the listed paths or a manual dispatch gathers that evidence. Package build success alone will not pass input-method, minimum-OS, Wayland or native-dialog acceptance.

Remote compatibility execution: pending. This field will be updated with the pull request, workflow run and per-platform results after execution.

## End-to-end acceptance (tasks 8.2 and 8.3)

Pending for every matrix target:

- Automatic authentication and reload; invalid/expired/reused launch exchange refusal.
- Local and SSH host operations, trust repair and host/revision isolation.
- Chinese input composition, multiline paste, copy versus Ctrl+C, mouse/trackpad scroll.
- Window resize, fullscreen refit, continuous ANSI/Unicode output and reconnect without input replay.
- File edit, preview, save and download, including chooser cancellation and destination failures.
- Preference restoration after restart on a different port; visible persistence errors.
- Unsaved-edit quit cancellation and confirmed quit; bounded failure/signal cleanup.
- Local/remote tmux sessions survive; the state lock is available on next launch.
- Installation/desktop-launch from the actual package without Go/Node; declared dependencies and macOS signing status are accurate.

Record actual native test results here as implementation progresses. Leave unavailable platforms pending rather than substituting browser/jsdom or cross-compilation results.
