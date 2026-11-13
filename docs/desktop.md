# Desktop development and operation

Desktop mode hosts the existing Vue interface in a Wails v3.0.0 window, with the existing Go HTTP/WebSocket backend in the same process. It is an experimental entry. The [verification record](desktop-verification.md) separates native builds, automated real-WebView probes, manual interaction and package acceptance; it is the authority for what has actually passed.

## Native build

The pinned compatibility toolchain is Go 1.26.10, Node 24 and CGO enabled. Build on the target OS and architecture. The frontend remains embedded; Go/Node are build dependencies only.

Ubuntu 24.04 amd64 build dependencies:

```sh
sudo apt-get install pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
npm --prefix hub/web ci
npm --prefix hub/web run build
cd hub
GOTOOLCHAIN=go1.26.10 CGO_ENABLED=1 go build -tags desktop -o ../visual-tmux-desktop ./cmd/visual-tmux-desktop
```

On macOS arm64 or amd64, install Xcode Command Line Tools and build the frontend as above. Use a consistent deployment target for native objects and the final linker:

```sh
cd hub
MACOSX_DEPLOYMENT_TARGET=12.0 GOTOOLCHAIN=go1.26.10 CGO_ENABLED=1 \
  go build -tags desktop -ldflags '-extldflags=-mmacosx-version-min=12.0' \
  -o ../visual-tmux-desktop ./cmd/visual-tmux-desktop
```

Setting a deployment target is not proof of runtime compatibility with macOS 12. Actual minimum-OS execution remains in the acceptance matrix. Native imports are excluded unless `-tags desktop` is set; the original `visual-tmux-client` still builds with CGO disabled.

## Launch and configuration

```sh
./visual-tmux-desktop
./visual-tmux-desktop --config /absolute/path/to/visual-tmux-client.yaml
./visual-tmux-desktop --help
./visual-tmux-desktop --version
```

Help/version initialize no GUI, listener or state. Desktop mode accepts `--config`, including the existing `VISUAL_TMUX_CLIENT_CONFIG` environment selection. An explicit relative file is resolved from the invoking directory, and paths inside YAML remain relative to that file. Without explicit selection, the implicit file is:

| System | Implicit configuration |
| --- | --- |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/visual-tmux-client/visual-tmux-client.yaml` |
| macOS | `~/Library/Application Support/visual-tmux-client/visual-tmux-client.yaml` |

Desktop mode manages `server.addr`, `auth.token` and `websocket.origin`: it binds `127.0.0.1:0`, creates a new bearer per process and retains same-origin WebSocket checks. Configured/environment values for these three settings are ignored; their names, never their values, are reported as managed settings. `--addr` is unavailable. The owned window signs in through a short-lived single-use fragment exchange. A bare browser URL receives no credential.

The implicit state directory is the same OS user configuration directory for `visual-tmux-client`. Explicit `hosts.state_dir` and `hosts.master_key_file` retain their existing behavior. Desktop and headless modes share the host registry and encryption material sequentially. A second desktop launch for the same profile focuses the first window. A running headless owner remains a conflict: close that process normally, then retry; the desktop never stops it or borrows its credentials.

Explicit tmux configuration wins, followed by PATH. Desktop macOS discovery additionally checks `/opt/homebrew/bin/tmux` and `/usr/local/bin/tmux` without invoking a shell. Install tmux for local sessions. Missing local tmux does not prevent remote-host administration or remote session access; local operations report their existing capability error.

## Preferences and native actions

Language, font size, sidebar collapse, recent host and per-host session order/pins are saved to private `desktop-preferences.json` in the effective state directory. The file is versioned, bounded to 1 MiB and atomically replaced. It does not contain authentication tokens or editor drafts. Corrupt files are preserved and a visible warning uses defaults; write failures are visible and keep the workspace usable.

Normal reload recovers the document-session credential. Application → Reload workspace confirms possible draft loss and creates a fresh one-use launch code; use it when initialization reports stale credentials. HTTP/HTTPS links explicitly activated in the interface open the system browser. Unsupported schemes and off-origin WebView navigation are rejected. Native actions use authenticated same-origin HTTP, not Wails raw messages.

Downloads retain the source host and connection revision while a native chooser selects the local destination. Existing source authorization, roots, disabled-file setting and size limits remain authoritative. Cancellation writes nothing; overwrite requires confirmation, and incomplete transfers do not replace the previous destination.

## Closing and recovery

Closing the only window and Application → Quit / Cmd+Q / Ctrl+Q use the same confirmation path. Cancel preserves editor state and terminal attachments. Confirmed quit waits up to five seconds for queued preferences; failed or stalled persistence keeps the application open with a visible error. Signals and backend failures use bounded resource cleanup. Tmux sessions remain owned by tmux rather than by the GUI process; abrupt process termination cannot recover unsaved editor buffers.

On startup failure, check the native diagnostic window, explicit configuration paths and whether another process owns the state. On backend failure, the owned listener and registry are released before reporting the failure. Restart the application after addressing the cause. The original server command remains available for rollback: close the desktop, then start `visual-tmux-client --config <the-same-explicit-config>`. Keep the host database and matching master key together; see the main README's backup instructions.

## Development packages and verification

After building a native binary from the repository root:

```sh
DESKTOP_BINARY=./visual-tmux-desktop sh scripts/package-desktop.sh
```

The script produces an Ubuntu amd64 DEB with GTK4/WebKitGTK runtime dependencies, launcher and icon, or an architecture-specific macOS `.app` ZIP. The DEB includes a narrowly attached AppArmor profile for `/usr/bin/visual-tmux-desktop`, allowing WebKit to create its renderer sandbox under Ubuntu 24.04 namespace restrictions. Install/remove hooks load/unload only that profile when AppArmor is enabled. The macOS development archive is labeled `development-unsigned`: it uses ad-hoc signing with no Developer ID or notarization. These are development artifacts, not a desktop release. CI publishes them as workflow artifacts and preserves existing headless releases.

On Ubuntu with restricted namespaces, a source binary at another path needs a matching AppArmor profile. The DEB installation configures the packaged executable's path. Source build success alone does not verify sandbox startup from an arbitrary location.

Optional signing is available only through an explicitly selected `notarize` input on a manual Desktop compatibility workflow run on `main`. Configure repository secrets `MACOS_CERTIFICATE_P12_BASE64`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY` (a Developer ID Application identity), `MACOS_NOTARY_API_KEY_P8_BASE64`, `MACOS_NOTARY_KEY_ID` and `MACOS_NOTARY_ISSUER_ID`. Pull-request and ordinary unsigned builds do not receive these secrets. The signing script uses a temporary private keychain, signs with hardened runtime and timestamp, requires an explicit Accepted notarization result, staples and validates the ticket, and checks Gatekeeper before producing a `developer-id-notarized` archive. Failed checks produce no signed artifact. Actual Developer ID/notarization execution remains pending because no signing credentials were supplied; successful signing would not complete the separate GUI acceptance matrix.

The explicit disposable probe never uses saved hosts or real tmux sessions:

```sh
./visual-tmux-desktop --spike --probe
```

It uses temporary state, synthetic sessions and native WebViews. Automated native-dialog probes operate only on their own window. Follow the verification record for current probe coverage. Manual Chinese composition, clipboard shortcuts, terminal scrolling/resize/fullscreen, full file editing and package installation still need their recorded target-specific acceptance; an automated Unicode string round-trip does not prove IME support. Run tmux-backed Go tests outside tmux and only through the repository's guarded disposable fixtures.

Package startup can be verified separately with `--spike --probe-startup`. This explicitly excludes chooser/input acceptance. At the user's request, outstanding macOS interactive validation is deferred for testing on their Mac; see the [handoff checklist](desktop-macos-acceptance.md). CI runs the complete Linux probe and existing macOS build/package/startup regressions, records the macOS interactive deferral, and publishes clearly labeled development packages for review. Green CI does not complete the deferred integration gate or input acceptance.

For an isolated headless Wayland integration check on Ubuntu 24.04 with Weston 13 available:

```sh
python3 scripts/probe-desktop-wayland.py ./visual-tmux-desktop
```

The script creates private HOME/config/cache/runtime directories, starts its own compositor and D-Bus session, runs the complete disposable probe, then stops only its own compositor. `--weston-prefix /path/to/extracted/usr` supports privately extracted Weston packages. It records a log under `/tmp` and excludes physical keyboard/IME/scroll/trackpad acceptance.
