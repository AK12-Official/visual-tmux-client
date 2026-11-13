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
| Single-instance focus and signal cleanup | Passed in native test suite | Passed in native race suite |
| Continuous ANSI/Chinese output through the actual HTTP/WebSocket/frontend | Passed with injected terminal process | Passed with injected terminal process |
| Ctrl+C without selection reaches terminal as `0x03` | Passed with native XTest input | Passed with private virtual seat |
| Concurrent first-use clipboard calls, exact Unicode/multiline bytes and Ctrl+Shift+V paste | Passed | Passed with private virtual seat |
| Native drag selection and Ctrl+C copy without sending an interrupt | Passed | Passed with private virtual seat |
| Visible copy failure with clipboard HTTP failure injected in the real page | Passed | Passed with private virtual seat |
| Real IBus/libpinyin composition and exactly-once Chinese commit | Passed | Passed with private virtual seat |
| OS fullscreen entry/exit, frontend layout and measured resize | Passed | Passed with private virtual seat |
| Rapid window resize reaches changed final terminal dimensions | Passed | Passed with private virtual seat |
| Native mouse-wheel scroll changes visible terminal output in both directions | Passed | Passed with private virtual seat |
| Trusted HTTP/HTTPS links dispatch through real `xdg-open` | Passed | Passed with private virtual seat |
| Synthetic link refuses browser launch; unsupported scheme does not execute | Passed | Passed |
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
The owned workspace stays mounted at its original origin throughout. Browser
startup has a separate bounded budget for cold CI runners. Exit code, elapsed
time, timeout status and bounded failure diagnostics are retained, so a handler
that starts and exits without reaching the HTTP target still fails acceptance.

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
enabled only on private displays: XTest on X11 and the test-only Weston 13
virtual seat on Wayland. CI runs both native test suites.

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

- Physical trackpad behavior and compositor-managed input-method integration.
  Private virtual-seat input and the GTK IBus module do not establish these cases.
- Other desktop-portal implementations beyond the verified private GTK backend.
- Final regression and installed-package verification for the completed real-host GUI workflow.
- Installed-package acceptance in an actual Wayland desktop and the remaining
  complete packaging/launcher acceptance matrix.

OpenSpec remains 27/35 because its eight open tasks combine these outstanding
Linux checks with deferred macOS checks. The passes above are retained as
per-platform evidence; no combined task is checked off prematurely.

## Real IME and Wayland native input continuation

Both complete native suites pass with Go's race detector, including single-instance
focus, signal cleanup and workspace interaction. Evidence:
`/tmp/vtc-x11-full-ime-race-fixed.log` and
`/tmp/vtc-wayland-full-ime-race-fixed.log`. A race between repeated-launch
Show/Focus and Hide was found and fixed by scheduling window operations on the
UI thread.

A private IBus daemon with its configuration service and libpinyin 1.15.7 receives
actual `zhongwen` + Space key events. The real page observes compositionstart
and compositionend, and the terminal receives exactly `中文` once on both X11
and Wayland. No user input-method configuration is changed. This verifies the
GTK IBus module path, not every desktop's compositor-managed IM integration.

The Weston 13 desktop shell uses an isolated test module and virtual seat for
keyboard, trusted pointer clicks/drag, discrete wheel and continuous finger-axis
scrolling. Both scroll directions change visible terminal output. Pointer
coordinates are checked against trusted page events. This is not physical
trackpad evidence. The module is test-only and is not shipped in the application.

Reproduce with a race-enabled native test executable and installed verification
dependencies (`weston`, `libweston-13-dev`, IBus/libpinyin, GTK4 IBus module):

```sh
python3 scripts/probe-desktop-x11.py --ime /tmp/vtc-linux-input-race.test -test.v
python3 scripts/probe-desktop-wayland.py /tmp/vtc-linux-input-race.test --test-suite --test-input --ime
```

Private extracted installations can use the prefix flags above. The module
targets Weston's version 13 backend ABI; its declarations follow Ubuntu's Weston
13.0.0 source, whose upstream tarball SHA256 is
`52ff1d4aa2394a2e416c85a338b627ce97fa71d43eb762fd4aaf145d36fc795a`.

The GTK desktop-portal backend also passes native save and preliminary replacement
confirmation on both X11 and Wayland. The app's separate replacement cancellation
preserves the old destination, and approval writes exact source bytes. The
automation uses AT-SPI on the private session bus, restricts access to the GTK
portal backend PID, and restricts destinations to the test fixture directory.
No user accessibility configuration is changed. The GTK backend is selected in
private portal configuration; this does not validate every GNOME/KDE portal.

Portal reproduction adds `--portal` to either command above. It requires
`xdg-desktop-portal`, `xdg-desktop-portal-gtk`, AT-SPI and Python GI bindings.
Verified logs: `/tmp/vtc-portal-fifth.log` and
`/tmp/vtc-wayland-portal.log`. The earlier in-process chooser tests remain
independent evidence and can be reproduced by omitting `--portal`.

The complete X11 race suite additionally passes direct portal chooser cancellation
with no destination created (`/tmp/vtc-x11-portal-cancel.log`). A repeated complete
Wayland suite exposed a Weston compositor SIGSEGV during consecutive GTK portal
dialogs (`/tmp/vtc-wayland-portal-cancel-repeat.log`); that combination remains
incomplete pending diagnosis despite the earlier single-workspace pass.

GDB confirms the compositor exits inside `desktop-shell.so` while calling
`weston_desktop_surface_foreach_child`, not inside the application. The trace is
retained in `/tmp/vtc-wayland-portal-gdb.log`. A 500 ms path-completion delay did
not resolve it and is not counted as a fix. CI currently gates the complete
Wayland native suite with the in-process GTK chooser and the complete X11 suite
with the GTK portal. Repeated Wayland portal acceptance remains open; the
optional `--portal` switch preserves the reproducer.

## GNOME Wayland portal verification

A separate private GNOME Shell 46.0 / Mutter 46.2 headless desktop passes
consecutive downloads through `org.freedesktop.impl.portal.desktop.gnome`,
including preliminary replacement, the application's replacement cancel/approve,
and direct chooser cancellation with no file created. Markdown preview, Unicode
editor save and dirty-quit cancel/discard also pass. Reproduce:

```sh
python3 scripts/probe-desktop-gnome.py /tmp/vtc-portal-cancel.test --log /tmp/vtc-gnome-portal-final.log
```

The binary is compiled with `go test -race -tags desktop -c`. The runner uses
private HOME/XDG paths, its own D-Bus and Wayland socket, memory GSettings and
only stops its owned GNOME process. Existing displays and the user's settings
are not used. It requests software rendering; Mutter selects a GBM device
without mode setting on this machine. GNOME/portal dependencies are verification
tools, not application runtime requirements.

This runner does not inject native input: its input/clipboard/fullscreen matrix
is still distinct from the passing Weston/X11 input suites. `--test-suite`
additionally exposes a GNOME-specific remaining case: the second CLI launch
shows the window but does not obtain focus under Mutter's activation policy
(`/tmp/vtc-gnome-full.log`). Signal cleanup and workspace pass in that run.
Real desktop-launch activation and forwarding its activation token require
further implementation/verification; this focus case is not marked complete.
The Weston shell crash is not reproduced in the GNOME portal workspace.

For commit `55872bed71d3d57a7316259397037468a828c748`, both
[desktop compatibility CI](https://github.com/AK12-Official/visual-tmux-client/actions/runs/43396670113)
and [ordinary CI](https://github.com/AK12-Official/visual-tmux-client/actions/runs/43396670197)
passed, including native Linux/Mac build/package checks and the existing four
headless packages. This does not complete deferred Mac interactive acceptance.

### GNOME activation-token fix

The complete GNOME suite now passes under Go's race detector
(`/tmp/vtc-gnome-focus-ack.log`): repeated launch focuses the first window,
signal cleanup passes and the portal/file/dirty-quit workspace passes. The
second process forwards `XDG_ACTIVATION_TOKEN` through Wails single-instance
data; GTK retains the startup identifier until the hidden window maps again.

The test creates an owned GTK launch button, activates it with Mutter's private
remote-desktop virtual keyboard and obtains GTK's startup notification token.
The launcher remains alive until the recipient records actual visibility and
focus. Thus launcher exit and automatic focus fallback cannot satisfy the test.
A bare CLI launch without a valid desktop activation token remains subject to
the compositor's focus-stealing policy. Reproduce with the current compiled
native test executable and `probe-desktop-gnome.py --test-suite`.

Desktop-tagged golangci-lint reports zero issues for these changes. The GNOME
input matrix beyond this launcher interaction remains distinct from the full
X11/Weston virtual-input tests.

### Real-host GUI fixture preparation

The desktop command's normal TestMain now installs `testutil.ParentGuard`.
`RunWithParentGuard` factors the same implementation for native fixture event
loops which must run on the original main thread; it retains all stand-in,
socket snapshot, session comparison and owned cleanup checks. Existing testutil
race tests pass (3.507 s), desktop-tagged command tests pass (6.094 s), and
combined desktop/testutil lint reports zero issues. No real-host GUI acceptance
is claimed by this preparation. That remains an outstanding Linux item.

### Real local and SSH GUI continuation

The X11 race-enabled native fixture now passes two consecutive desktop starts
against real isolated local tmux and disposable SSH-backed tmux sessions
(`/tmp/vtc-real-hosts-diagnostic.log`, 4.24 s). The real frontend selects each
host/session and verifies visible terminal output with ANSI/Chinese source
bytes. Each desktop quits normally; the second start renders both original
sessions again. Host registration/trust is preseeded by the fixture: this does
not establish GUI enrollment or trust-repair acceptance, which remain pending.

The first real local run exposed concurrent initial-dimension reads versus
resize writes in Attachment.Start. Initial dimensions are now copied under the
attachment mutex before input pumping starts. The local native race test and
existing terminal race tests pass. A restart fixture initially timed out;
stage-report requests now preserve diagnostic boundaries and the latest run
passes, but broader display and reconnect/file/trust workflows remain open.

Real SSH GUI file preview/edit/save and reconnect now pass in both desktop
rounds under the race detector (`/tmp/vtc-real-files-confirm.log`, 6.70 s).
The fixture opens the remote README preview, edits draft.txt in CodeMirror,
answers the real native save confirmation and checks exact source-file bytes
from the parent fixture. Each round saves different Unicode content. It then
drops SSH connections, observes a non-connected UI state and requires a larger
remote output counter after reconnect. The second desktop start reuses both
original sessions. Earlier file-save timeouts were caused by the unhandled
native confirmation and are superseded by this passing run. GUI host enrollment,
trust repair and real-host native download remain pending.

Real remote binary download now passes in both X11 desktop rounds under the
race detector (`/tmp/vtc-real-download.log`, 8.45 s). The actual file UI opens
a private binary SFTP fixture, invokes the native save chooser, and verifies
exact bytes including NUL, 0xff and Chinese text at the selected destination.
Each round uses a distinct destination. The same run retains remote preview,
Unicode save, SSH reconnect and original-session survival coverage. The portal
variant of this real-host download is implemented but not yet verified.

The complete real-host workflow also passes through the GTK portal on X11
(`/tmp/vtc-real-download-portal.log`, 10.89 s) and GNOME Wayland's portal
(`/tmp/vtc-real-hosts-gnome.log`, 13.65 s), both with Go's race detector. Each
run includes two desktop starts, original local/SSH session reattachment,
remote Markdown preview, different Unicode edits saved per round, binary
byte-equal native download, and SSH drop/reconnect with advancing output.
Desktop/testutil/terminal lint reports zero issues. GUI enrollment and trust
repair remain separate pending checks.

GUI SSH enrollment and initial trust confirmation now pass on native X11 with
Go's race detector (`/tmp/vtc-real-enroll-ready.log`, 8.51 s). The parent no
longer pre-enrolls the host: the real page opens host management, fills the
form, probes the disposable server, compares the displayed fingerprint with
the fixture's exact public-key fingerprint, confirms trust, and saves. The
second desktop start reuses that UI-created registry entry. Both rounds still
pass terminal/file/download/reconnect checks. Probe automation waits for Vue's
actual enabled button state. Desktop lint reports zero issues. Host-key
replacement/trust repair remains distinct and unverified by initial enrollment.

GUI trust repair now passes on native X11 under the race detector
(`/tmp/vtc-real-trust-repair.log`, 8.80 s). On the same disposable SSH address,
the parent rotates the actual server key before the second desktop start.
The UI first refuses the connection (`host_unavailable`), then probes the
existing host, compares the displayed new fingerprint with the rotated key,
confirms trust and saves. The original remote session and full file/download/
reconnect workflow subsequently pass. This supersedes earlier initial-trust-only
limitations for this environment. The fixture's rotation itself is covered by
a race-enabled test proving old fingerprints fail and the new fingerprint
succeeds. Desktop/testutil lint reports zero issues.
