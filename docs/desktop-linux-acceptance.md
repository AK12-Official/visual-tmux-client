# Linux desktop acceptance

This record supplements [desktop verification](desktop-verification.md). macOS
interactive acceptance remains deferred by the user. Linux acceptance is still
in progress; passing a native fixture does not complete the full target matrix.

## Verified in the current Linux continuation

Environment: Ubuntu 24.04 amd64, GTK 4.14.5, WebKitGTK 2.52.6,
Go 1.26.10, authenticated private Xvfb/Openbox X11 display and a separate
headless Weston 13 Wayland compositor. Each run uses private HOME/XDG state
and a new session bus, with inherited D-Bus starter addresses removed. WebKit and browser sandboxes remain enabled.

| Behavior | X11 | Wayland |
| --- | --- | --- |
| Automatic authentication and both reload paths | Passed in existing native spike | Passed in existing native spike |
| Single-instance focus and signal cleanup | Passed in native test suite | Existing controller coverage; new physical acceptance pending |
| Continuous ANSI/Chinese output through the actual HTTP/WebSocket/frontend | Passed with injected terminal process | Passed with injected terminal process |
| Ctrl+C without selection reaches terminal as `0x03` | Passed with native XTest input | Pending |
| Concurrent first-use clipboard calls, exact Unicode/multiline bytes and Ctrl+Shift+V paste | Passed | Pending |
| Native drag selection and Ctrl+C copy without sending an interrupt | Passed | Pending |
| Visible copy failure with clipboard HTTP failure injected in the real page | Passed | Pending |
| WebKit input-method context commits Chinese text | Passed | Pending |
| OS fullscreen entry/exit, frontend layout and measured resize | Passed | Pending |
| Rapid window resize reaches changed final terminal dimensions | Passed | Pending |
| Native mouse-wheel scroll changes visible terminal output in both directions | Passed | Pending |
| Trusted HTTP/HTTPS links dispatch through real `xdg-open` | Passed | Pending |
| Synthetic link refuses browser launch; unsupported scheme does not execute | Passed | Existing native navigation guard evidence |
| Native GTK download overwrite cancel preserves old destination | Passed | Passed |
| Separate native replacement confirmation writes exact source bytes | Passed | Passed |
| Markdown preview renders Chinese heading | Passed | Passed |
| Edit and save Unicode text through actual file UI to disk | Passed | Passed |
| Cancel dirty quit preserves editor draft and original streaming attachment | Passed | Passed |
| Confirm discard exits, closes owned attachment and retains last saved bytes | Passed | Passed |

The workspace fixture injects a terminal process and local session inventory;
it starts no shell, tmux server or SSH connection. Its bytes pass through the
production binary WebSocket transport and terminal UI. Existing guarded Go
integration tests separately cover real local/SSH tmux survival and reattachment;
they are not substituted for an entire real-host GUI acceptance session.

The browser fixture registers its handler only in private MIME configuration,
starts real Chrome with a temporary profile and waits for its owned processes.
The HTTP target confirms a browser request without Authorization. HTTPS proves
dispatch, not successful TLS rendering: its disposable self-signed target is
expected to fail certificate validation. No certificate or sandbox bypass is used.
The owned workspace stays mounted at its original origin throughout.

Clipboard manager calls, including concurrent first use, now serialize on the
GTK main thread. Wails already dispatches actual writes; this also protects its
manager's lazy initialization before that dispatch. Copy failures use a message
applicable to both desktop and browser environments. The DEB declares `xdg-utils`,
required by the pinned Wails Linux browser opener. A rebuilt DEB installed in the
disposable Ubuntu runtime container and automatically installed that dependency.
Container installation does not establish GUI launch under a different compositor.

## Reproduce

Build embedded assets, then compile the native test executable:

```sh
cd hub/web
npm ci
npm run build
cd ..
GOTOOLCHAIN=go1.26.10 go test -tags desktop -c -o /tmp/vtc-linux-workspace.test ./cmd/visual-tmux-desktop
cd ..
python3 scripts/probe-desktop-x11.py --log /tmp/vtc-x11-all-native.log /tmp/vtc-linux-workspace.test -test.v
python3 scripts/probe-desktop-wayland.py /tmp/vtc-linux-workspace.test --test-workspace --log /tmp/vtc-wayland-workspace.log
```

X11 verification needs Xvfb, xauth, Openbox, libXtst, D-Bus and Google Chrome.
These are verification tools, not additional desktop runtime dependencies.
`--prefix /path/to/usr` supports a private extracted Xvfb/Openbox installation;
Wayland's `--weston-prefix /path/to/usr` supports private Weston 13 libraries.
The X11 runner allocates its display number, uses an Xauthority cookie, disables
TCP listening and stops only its owned processes. Native input automation is
enabled only on that private X11 display. CI runs this same X11 test suite.

The GTK chooser automation checks the selected path before accepting. It waits
for asynchronous folder navigation before setting the filename, so a wrong
default directory cannot count as successful download acceptance. All workspace
test source/destination files and the child working directory are temporary.
Production download paths still come exclusively from the native chooser.

## Regression results

The private X11 suite passed all three native tests (focus, signal cleanup,
workspace); the workspace case completed in 8.23 seconds. The final Weston
workspace case and both existing full auth/dialog/download/reload/flush probes
also passed. Logs were retained at `/tmp/vtc-x11-all-native-final.log`,
`/tmp/vtc-wayland-workspace-final.log`, `/tmp/vtc-x11-spike-final.log` and
`/tmp/vtc-wayland-spike-final.log` for this local run. CI preserves its X11 log
as an artifact. A subsequent X11 run under the Go race detector also passed
eight simultaneous first-use clipboard requests and the full workspace flow
(`/tmp/vtc-x11-clipboard-race.log`, 9.55 seconds). The Wayland workspace passed
under the race detector after inherited D-Bus starter addresses were removed
(`/tmp/vtc-wayland-workspace-race.log`).

The continuation also passed 34 frontend test files and the frontend build,
ordinary Go tests, the complete desktop-tagged Go suite, desktop-tagged vet/lint,
`go test -race ./internal/desktop/...`, an independent CGO-disabled headless
build, actionlint and strict OpenSpec validation. Native UI cases run on their
private displays separately from the display-free tagged Go suite. No tmux
isolation helper was changed.

Remote [desktop compatibility run 43268738195](https://github.com/AK12-Official/visual-tmux-client/actions/runs/43268738195)
passed all three native targets for `fe748d6`; ordinary
[CI run 43268738216](https://github.com/AK12-Official/visual-tmux-client/actions/runs/43268738216)
also passed, including the four existing headless packages. These results do not
complete the deferred macOS interactive checks.

## Remaining Linux evidence

- Real IBus/libpinyin composition and candidate selection. A private daemon was
  attempted, but its client/engine initialization failed and a later direct-bus
  experiment timed out. This is unverified, distinct from the passing WebKit
  Unicode commit check. No user input-method setting was changed.
- Wayland keyboard/clipboard/fullscreen/scroll integration and physical trackpad
  behavior. Headless Weston and XTest evidence do not establish these cases.
- Desktop-portal save chooser behavior; current save/overwrite probes explicitly
  use GTK's in-process chooser with `GDK_DEBUG=no-portals`.
- An entire GUI session against real local and SSH hosts, including host-key
  trust repair, reconnect and remote file operations. Existing backend tests and
  the injected native workspace exercise different portions of this path.
- Installed-package acceptance in an actual Wayland desktop and the remaining
  complete packaging/launcher acceptance matrix.

OpenSpec remains 27/35 because its eight open tasks combine these outstanding
Linux checks with deferred macOS checks. The passes above are retained as
per-platform evidence; no combined task is checked off prematurely.
