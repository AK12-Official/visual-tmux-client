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

The user reported passing the macOS 12 runtime check. See the [macOS acceptance record](desktop-macos-acceptance.md) for the reported scope. Native imports are excluded unless `-tags desktop` is set; the original `visual-tmux-client` still builds with CGO disabled.

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

## Proxy for remote hosts

Where direct SSH connections are blocked but a proxy app such as Clash can reach the hosts, open **Settings** in the top bar (before **Hosts**), enter the proxy address and save. Desktop and browser mode share the setting, because it is stored with the host state rather than among the desktop preferences.

- **What to enter.** The address and port the proxy app listens on, as `host:port`, for example `127.0.0.1:7890`. A leading `socks5://` is accepted and removed. Anything else, including a username or password or an `http://` address, is refused with an explanation under the field. Leave the field empty to connect directly.
- **What uses it.** Every new connection to every remote host: checking the server key when a host is added or repaired, terminals, file operations and reconnection. The local tmux server is never contacted through it. Terminals that are already open keep their connection; the proxy applies when a connection is next opened, so a changed address takes effect on reconnection. The host card in the host manager shows whether each host connects directly or through the proxy.
- **Commands in a local terminal.** Each local terminal session created while a proxy is set receives the standard variables that tell commands about a proxy: `http_proxy`, `https_proxy` and `all_proxy`, in lower and in upper case, naming the proxy as `socks5h://host:port`, plus `no_proxy` and `NO_PROXY` listing `localhost`, `127.0.0.1`, `::1` and `.local`. Programs that read them, such as `git` over HTTPS and `curl`, then connect through the proxy, and windows opened in such a session inherit them. Programs that do not read these variables, or that accept only an HTTP proxy address, are not covered: `ssh` and `git` over SSH keep going direct. Nothing is added to sessions on remote hosts. A session keeps the variables it was created with, so open a new one after changing the address. Variables set by your own shell configuration, such as a line in `~/.zshrc`, are applied afterwards and take precedence; the settings dialog says so.
- **What is not supported.** A proxy that asks for a username or password, HTTP proxies, a different proxy per host, reading the system proxy settings, and `~/.ssh/config` `ProxyJump` or `ProxyCommand` entries. The setting is not a secret and is stored next to the default host.
- **When it fails.** The session list stops retrying until you choose Retry, and the message says which side to look at. An open terminal keeps trying to reconnect with longer gaps; the notice above it names the proxy:

| The message says | What to do |
| --- | --- |
| The proxy cannot be reached | Start the proxy app and check the address in Settings. |
| The proxy asks for a username or password | Use a proxy address that needs no sign-in. |
| The proxy refused to connect to the host | Check the proxy app's rules: this host is probably set to go direct, which is blocked. |
| The host did not answer through the proxy | Check the host's address and port; the proxy is working. |

- **Rollback.** A previous build ignores the stored address and connects directly, as it always did.

## Closing and recovery

Closing the only window and Application → Quit / Cmd+Q / Ctrl+Q use the same confirmation path. Cancel preserves editor state and terminal attachments. Confirmed quit waits up to five seconds for queued preferences; failed or stalled persistence keeps the application open with a visible error. Signals and backend failures use bounded resource cleanup. Tmux sessions remain owned by tmux rather than by the GUI process; abrupt process termination cannot recover unsaved editor buffers.

On startup failure, check the native diagnostic window, explicit configuration paths and whether another process owns the state. On backend failure, the owned listener and registry are released before reporting the failure. Restart the application after addressing the cause. The original server command remains available for rollback: close the desktop, then start `visual-tmux-client --config <the-same-explicit-config>`. Keep the host database and matching master key together; see the main README's backup instructions.

## Development packages and verification

After building a native binary from the repository root:

```sh
DESKTOP_BINARY=./visual-tmux-desktop sh scripts/package-desktop.sh
```

The script produces an Ubuntu amd64 DEB with GTK4/WebKitGTK runtime dependencies, launcher and icon, or an architecture-specific macOS `.app` ZIP with its icon. It stops when an icon file is missing. The DEB includes a narrowly attached AppArmor profile for `/usr/bin/visual-tmux-desktop`, allowing WebKit to create its renderer sandbox under Ubuntu 24.04 namespace restrictions. Install/remove hooks load/unload only that profile when AppArmor is enabled. The macOS development archive is labeled `development-unsigned`: it uses ad-hoc signing with no Developer ID or notarization. These are development artifacts, not a desktop release. CI publishes them as workflow artifacts and preserves existing headless releases.

### Application icon

Both packages show the same artwork. The sources are `packaging/desktop/icon/app-icon.svg` (master, drawn on the 1024-unit Apple icon grid) and `app-icon-small.svg` (a simplified drawing used at 32 pixels and below). Their outputs are committed and packaging only copies them:

| File | Used by |
| --- | --- |
| `packaging/desktop/visual-tmux-client.svg` | Linux: `hicolor/scalable/apps` launcher icon |
| `packaging/desktop/visual-tmux-client.icns` | macOS: `CFBundleIconFile`, ten representations from 16 to 1024 pixels |

After editing the artwork, regenerate and commit the results from the repository root:

```sh
sh scripts/generate-app-icon.sh
```

The script needs `rsvg-convert` (Homebrew: `brew install librsvg`; Ubuntu: `librsvg2-bin`). The `.icns` is produced only on macOS, which also provides `iconutil`; elsewhere the script writes the Linux icon and stops. Set `ICON_OUTPUT_DIR` to write elsewhere, as CI does when it regenerates the icon and checks that the committed `.icns` still has all ten representations at the expected sizes. Byte equality is not required, because rasterizer versions differ. The package version in the archive name, `CFBundleShortVersionString` and `CFBundleVersion` all come from `DESKTOP_VERSION`.

On Ubuntu with restricted namespaces, a source binary at another path needs a matching AppArmor profile. The DEB installation configures the packaged executable's path. Source build success alone does not verify sandbox startup from an arbitrary location.

Optional signing is available only through an explicitly selected `notarize` input on a manual Desktop compatibility workflow run on `main`. Configure repository secrets `MACOS_CERTIFICATE_P12_BASE64`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY` (a Developer ID Application identity), `MACOS_NOTARY_API_KEY_P8_BASE64`, `MACOS_NOTARY_KEY_ID` and `MACOS_NOTARY_ISSUER_ID`. Pull-request and ordinary unsigned builds do not receive these secrets. The signing script uses a temporary private keychain, signs with hardened runtime and timestamp, requires an explicit Accepted notarization result, staples and validates the ticket, and checks Gatekeeper before producing a `developer-id-notarized` archive. Failed checks produce no signed artifact. Actual Developer ID/notarization execution remains pending because no signing credentials were supplied; GUI acceptance is documented separately.

The explicit disposable probe never uses saved hosts or real tmux sessions:

```sh
./visual-tmux-desktop --spike --probe
```

It uses temporary state, synthetic sessions and native WebViews. Automated native-dialog probes operate only on their own window. Follow the verification record for current probe coverage. Manual IME, clipboard, window, file and package checks are documented in the [acceptance checklist](desktop-manual-checklist.md). Run tmux-backed Go tests outside tmux and only through the repository's guarded disposable fixtures.

Package startup can be verified separately with `--spike --probe-startup`. On macOS this probe covers packaged startup and workspace reload. Chooser and interactive input coverage comes from the [manual acceptance record](desktop-macos-acceptance.md).

For an isolated headless Wayland integration check on Ubuntu 24.04 with Weston 13 available:

```sh
python3 scripts/probe-desktop-wayland.py ./visual-tmux-desktop
```

The script creates private HOME/config/cache/runtime directories, starts its own compositor and D-Bus session, runs the complete disposable probe, then stops only its own compositor. `--weston-prefix /path/to/extracted/usr` supports privately extracted Weston packages. It records a log under `/tmp` and excludes physical keyboard/IME/scroll/trackpad acceptance.
