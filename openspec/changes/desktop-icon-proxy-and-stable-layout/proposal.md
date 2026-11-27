# Proposal

## Why

Three follow-ups recorded in `docs/TODO.md` keep the desktop application from being usable for the people it targets. The macOS package ships without an application icon, so Dock, Finder and Launchpad show a generic one while Linux already shows the project's artwork. Remote hosts cannot be reached on networks where direct SSH is blocked and only a local proxy client such as Clash provides a path, because every remote connection is dialed directly. The proxy setting alone does not help with what users then do inside the application: `git push` typed in a local terminal still goes direct, because terminal programs use only a proxy they are told about. Switching the interface language changes the size of controls: measured on the current source, 10 of 14 controls in the top bar and sidebar change height and 9 change width between English and Simplified Chinese, so the interface jumps under the pointer.

All three were reported from real use. None touches the tmux engine or the terminal protocol, so they are bundled into one change.

## What Changes

- **Application icon on macOS.** The macOS package carries an application icon generated from the same artwork Linux already ships, rendered on the Apple icon grid at every required size, with legible small-size variants. Linux keeps its launcher icon, regenerated from the same source so both platforms share one artwork.
- **Proxy setting for remote connections.** A new field in the application's settings accepts a SOCKS5 proxy address (`host:port`). When it is set, every connection to a remote host is established through that proxy: host probing, fingerprint confirmation and trust repair, terminals, SFTP file operations and reconnection. Host names are handed to the proxy for resolution so the proxy's own routing rules apply consistently. A configured proxy that cannot be reached is reported as such and is never silently bypassed with a direct connection. The setting persists across restarts. Connections to the local tmux server do not use the proxy.
- **Proxy for commands in local terminals.** With a proxy set, every local terminal session the application creates receives the standard proxy environment variables naming it, so commands such as `git` over HTTPS and `curl` run inside connect through the proxy. Local addresses are excluded. Sessions that already exist and sessions on remote hosts are left as they are, and the settings dialog says that variables set in the user's own shell configuration take precedence.
- **Unchanged trust and diagnostics.** Server key verification stays end-to-end and is not relaxed by the proxy. Connection problems name their cause in plain language: the proxy is not reachable, the proxy refused the connection, or the target host did not answer through the proxy.
- **Language-independent control geometry.** Buttons, selectors and inputs keep the same height and width in every supported language. Controls in one group share a minimum width sized for the longest supported label, stay on one line and center their text. Descriptive text such as error messages and help content continues to wrap naturally.
- **Geometry verification in a real WebView.** Control geometry under both languages is checked in the real desktop WebView on macOS and Linux, because the existing DOM tests do not perform layout.

Out of scope for this change: reading `~/.ssh/config` (`ProxyJump` / `ProxyCommand`), automatic detection of system proxy settings, per-host proxy overrides and proxy authentication, a switch that turns the terminal variables off, and making `ssh` or `git` over SSH use the proxy. The first four remain possible follow-ups.

## Capabilities

### New Capabilities

- `ssh-proxy`: the user-configured SOCKS5 proxy through which the application establishes every connection to remote hosts and which new local terminal sessions carry for the commands run in them, including where the setting lives, how it persists, what it applies to and how proxy-related failures are reported.

### Modified Capabilities

- `host-management`: remote connection failures caused by the proxy are reported as distinct host errors, and host status names whether a proxy or a direct connection was used, without disclosing secrets. Server key trust requirements are unchanged.
- `desktop-application`: release packages carry the application icon that macOS and Linux desktop shells display, with one shared artwork.
- `web-internationalization`: controls keep language-independent height and width, and keep their position when the interface language changes.

## Impact

- **Backend:** the two remote dial sites in `hub/internal/host` (`connection.go`, `trust.go`) are unified behind one connection path; host status and error reporting gain proxy-related causes; a settings operation is added for the proxy address; local session creation passes the proxy to `tmux new-session -e`.
- **Frontend:** a settings surface for the proxy address; shared control sizing rules affecting `App.vue`, `SessionList.vue`, `HostManagerModal.vue`, `HelpModal.vue` and the file manager overlay.
- **Packaging:** `scripts/package-desktop.sh` and `packaging/desktop/`, plus an icon generation step.
- **Dependencies:** a SOCKS5 client, either `golang.org/x/net/proxy` or a small in-tree implementation; decided in design.
- **Verification:** the native desktop probes gain a bilingual geometry check on macOS and Linux; isolation rules for tmux-backed tests are unchanged.
- **Documentation:** the three `docs/TODO.md` entries are retired on completion; desktop operation and verification docs describe the proxy setting and the icon.
- **Not affected:** the tmux engine, the terminal protocol, sessions that already exist and the existing headless release artifacts.
