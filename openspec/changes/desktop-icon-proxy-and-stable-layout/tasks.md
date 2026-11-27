# Tasks

Repository rules apply throughout: tmux-backed tests run outside tmux and only through the existing isolation helpers (`testutil.SocketDir`, `testutil.ParentGuard`, the disposable SSH fixture); do not add ad hoc tmux commands or weaken helpers.

## 1. SOCKS5 transport and the remote dial path

- [x] 1.1 Implement an in-tree SOCKS5 `CONNECT` client without authentication in `hub/internal/host`: host names sent as domain names, IP literals sent as addresses, one dial budget covering proxy connect, negotiation and connect reply, context cancellation, typed errors for the four failure classes in design decision 3; verify unit tests against an in-process fake SOCKS5 server cover success for a domain and an IP target, every mapped reply, an authentication method being selected, the proxy closing without a reply, a negotiation timeout and cancellation, and pass with `cd hub && go test ./internal/host/...`
- [x] 1.2 Add the shared remote dial function with an explicit proxy route and make `dialAndHandshake` and `ProbeEndpoint` use it, with the proxy address read from host settings when each connection is opened; verify all existing connection, trust and probe tests pass unchanged with no proxy set, and new tests using the fake proxy show the terminal, SFTP and reconnect dials go through it when one is set
- [x] 1.3 Prove a configured proxy is never bypassed: add tests where the proxy is unreachable and the target address has a listener that records connections, and assert the listener receives none and the attempt fails within `dial_timeout`; add a test that reconnection after the proxy stops still targets the proxy only
- [x] 1.4 Prove server key verification is unchanged through a proxy: add tests using the disposable SSH fixture reached through the fake proxy for an unconfirmed key (trust required, no credentials sent) and a changed key (identity mismatch); verify they pass alongside the existing trust tests

## 2. Proxy setting storage and API

- [x] 2.1 Store the proxy address under a key in the existing `metadata` table with a revision-checked update in one transaction and return it from `GetSettings`; add store tests for an empty default, persistence across close and reopen, a stale revision being refused, absent-means-unchanged versus explicit clear for both `default_host_id` and the proxy address; verify with `cd hub && go test ./internal/host/...`
- [x] 2.2 Extend `GET/PUT /api/host-settings` with `proxyAddress` and normalize and validate it as described in design decision 5, rejecting other values with `invalid_proxy_address`; add router tests for each accepted form, each rejected form, a PUT without `proxyAddress` leaving it unchanged, a PUT without `defaultHostId` leaving the default host unchanged, and unauthenticated calls being refused; verify with `cd hub && go test ./internal/transport/...`
- [x] 2.3 Map the four proxy error codes to the wire codes in design decision 3 with HTTP 503 and the proxy address in the diagnostic; add handler tests for each code and that diagnostics contain no credential material; verify with `cd hub && go test ./internal/transport/...`

## 3. Settings dialog and proxy reporting in the workspace

- [x] 3.1 Extend the frontend host-settings client with `proxyAddress` and a partial update that sends only the fields being changed; add tests in `api.test.ts` for reading the field and for request bodies that omit untouched fields; verify with `npm --prefix hub/web test`
- [x] 3.2 Add `SettingsModal.vue` following the host-manager dialog pattern (title label, focus trap, Escape and close button), opened by a Settings button placed before the Hosts button in the top-bar actions, containing the proxy address field with a visible label, an accessible description, an inline error, the resulting connection path, and Save and Cancel; add DOM tests for direct-versus-proxy path text, validation messages, the revision-conflict path keeping the typed value, and focus and Escape behavior; verify with `npm --prefix hub/web test`
- [x] 3.3 Add English and Simplified Chinese strings for the dialog, the Settings button, the proxy address errors and the four proxy wire codes, with messages that name the proxy address and say whether the problem is the proxy or the target host; verify the i18n parity and matching tests pass
- [x] 3.4 Add `connectionPath` to the per-host capability report for SSH hosts and omit it for the local host, with backend tests; show it on the host card and map the proxy wire codes to their localized messages wherever host errors are displayed (host manager, session list, terminal states); add DOM tests for both languages; verify with `cd hub && go test ./internal/host/... ./internal/transport/...` and `npm --prefix hub/web test`
- [x] 3.5 Confirm the frontend still type-checks and builds with `npm --prefix hub/web run build`

## 4. Application icon

- [x] 4.1 Author the master artwork on the 1024-unit Apple icon grid and the simplified small-size variant under `packaging/desktop/icon/`, and add `scripts/generate-app-icon.sh` that renders the Linux scalable icon and a ten-representation `.icns` (32 pixels and below from the small variant); commit the generated `visual-tmux-client.svg` and `visual-tmux-client.icns`; verify the script runs with `rsvg-convert` and `iconutil` available, and `iconutil -c iconset` on the result lists all ten expected pixel sizes
- [ ] 4.2 Update `scripts/package-desktop.sh` to copy the `.icns` into `Resources`, add `CFBundleIconFile`, take the bundle version from the same value as the archive name, and fail when the icon is missing; the Linux branch installs the generated SVG; verify by building a macOS package from a native binary and checking `plutil -lint`, the extracted `CFBundleIconFile` value matching a file in `Resources`, `codesign --verify --strict`, and agreement between the archive name and the plist version, and by listing the icon inside a built DEB
  - Implemented. Verified locally on macOS: `plutil -lint`, `CFBundleIconFile` resolving to a sealed resource, `codesign --verify --strict`, archive name and plist version equal, and the script failing when the `.icns` is missing. The DEB listing cannot be produced on macOS (no `dpkg-deb`); the Ubuntu CI job now checks the installed icon and the DEB listing.
- [ ] 4.3 Add a CI step to the macOS Desktop compatibility job that installs an SVG rasterizer, regenerates the icon into a temporary directory, and checks that the committed `.icns` contains all ten representations at the expected pixel sizes; include the icon sources and script in the workflow's path filters; verify on a workflow run
  - Implemented for the macOS job (installs `librsvg`, regenerates into `RUNNER_TEMP`, checks ten representations and pixel sizes; the same commands passed locally). Awaiting a workflow run.
- [x] 4.4 Document the icon sources, the regeneration command and the packaging expectations in `docs/desktop.md`, and add Dock, Finder and Launchpad (macOS) and application menu and taskbar (Ubuntu) icon checks to `docs/desktop-manual-checklist.md`; verify the documented regeneration command runs as written and record a visual check at 16 and 32 pixels

## 5. Language-independent control geometry

- [x] 5.1 Add height tokens for three control sizes and minimum-width tokens per control group to `style.css`, with shared control rules (inline flex centering, `line-height: 1`, no wrapping, explicit height for selectors and inputs), and apply them to the top bar, session list, host manager, help dialog and file manager controls; verify `npm --prefix hub/web test` and `npm --prefix hub/web run build` pass and no descriptive text has a fixed height
- [x] 5.2 Choose token values by measuring both languages in the real WebView on macOS so that every label fits on one line and controls in one group are equally wide; verify the geometry check from task 6.1 passes on macOS for the sidebar, the host manager list and the add-host form

## 6. Geometry verification in the real WebView

- [x] 6.1 Add a native geometry fixture to `hub/cmd/visual-tmux-desktop` that runs with private state and synthetic sessions, injects a script that switches language through the real language selector, measures every visible button, selector and input in the three scenes keyed by DOM position, and posts both snapshots through the existing probe endpoint; make the test fail on any height or width difference and on any label overflow, and skip without a display like the other native tests; verify with the macOS native test run
- [ ] 6.2 Run the geometry fixture in the Ubuntu X11 and Wayland CI jobs through the existing native test entry points and confirm all three desktop jobs are green
  - The fixture is wired into the macOS, Ubuntu X11 and Wayland jobs through the existing native test entry point and passes on macOS arm64. Awaiting a workflow run for the Ubuntu jobs.
- [x] 6.3 Record the geometry check and its coverage in `docs/desktop-verification.md`, and add a language-switch check in both directions to `docs/desktop-manual-checklist.md`; verify the documented commands run as written

## 7. Integration checks

- [x] 7.1 Add an end-to-end Go test that drives the authenticated API against the disposable SSH fixture through the fake proxy: probe, confirm the fingerprint, attach, run a file operation and reconnect; then stop the fake proxy and assert the next attempt reports the proxy as unreachable while local host operations still succeed; verify with `cd hub && go test ./...`
- [x] 7.2 Run the complete validation used by CI: `npm --prefix hub/web test`, `npm --prefix hub/web run build`, `cd hub && go test -race ./...`, `cd hub && go vet ./...` and `make lint`; verify all pass with tests run outside tmux
- [x] 7.3 Describe the proxy setting, its scope and the plain-language errors in `docs/desktop.md`, and note them in `README.md` and `README.zh-CN.md`; remove the three retired entries (desktop network and proxy support, interface internationalization layout, application icon) from `docs/TODO.md`, keeping the desktop performance entry; verify the documentation matches the shipped behavior and `openspec validate desktop-icon-proxy-and-stable-layout --strict` passes

## 8. Proxy address in new local terminal sessions

- [x] 8.1 Return the proxy environment variables for an address in `hub/internal/host`: `http_proxy`, `https_proxy` and `all_proxy` in both cases with `socks5h://host:port`, and `no_proxy` and `NO_PROXY` excluding `localhost`, `127.0.0.1`, `::1` and `.local`; none for an empty address; add unit tests; verify with `cd hub && go test ./internal/host/...`
- [x] 8.2 Let the local tmux client add variables to each session it creates with `new-session -e`, through a function it calls at creation time, failing the creation when that call fails; keep remote session creation free of them; add tests on a disposable tmux server that a new session and the windows opened in it carry the variables, that a session created earlier or after the address is cleared does not, and that the remote argument builder passes none; verify with `cd hub && go test ./internal/tmux/... ./internal/host/...`
- [x] 8.3 Install the function in `app.New` from host settings, and extend the desktop end-to-end proxy test: after the proxy is saved through the API, a local session created through the API carries the variables, the session created before keeps none, and a session created after the proxy is cleared carries none; verify with `cd hub && go test ./internal/app/... ./internal/desktop/...`
- [x] 8.4 Rewrite the proxy field's explanation to say that new local terminal sessions use the proxy, and show, whenever an address is entered, a hint that variables set in the shell's own configuration take precedence; add English and Simplified Chinese strings and DOM tests for both; verify with `npm --prefix hub/web test` and `npm --prefix hub/web run build`
- [x] 8.5 Describe the behavior, what it does and does not cover, and a manual check in `docs/desktop.md`, `README.md`, `README.zh-CN.md` and `docs/desktop-manual-checklist.md`; verify the documented check runs as written

## Workflow follow-up

- Archive the change once the review requirements of the repository are satisfied.
- Mention the proxy setting and the application icon in the next release notes.
