# Design

## Context

Motivation is in `proposal.md`; required behavior is in the `specs/` deltas. This section lists only the current state that shapes the approach.

**Remote connections.** There are exactly two places that open a TCP connection to a remote host, both in `hub/internal/host`: `dialAndHandshake` in `connection.go` (used by `dialTransport` for terminals, files and reconnection) and `ProbeEndpoint` in `trust.go` (fingerprint probing; the HTTP layer injects it through a `probeDialer` variable in `router.go`). Both call `net.Dialer` directly. The `ConnectionManager` already holds the host registry.

**Local sessions.** Local sessions are created by `tmux.Client.Create` in `hub/internal/tmux` as one command queue (`start-server`, the global options, then `new-session -d -s name -c home`). Remote sessions use the same argument builder through `RemoteSessionBackend`. `app.New` builds the tmux client before the host registry, which owns the proxy setting. The project requires tmux 3.3 or newer; its `new-session -e` sets a variable in the new session, and tmux copies the session environment into every pane the session opens later.

**Host settings.** The host state database has a single-row `settings` table (`default_host_id`, `revision`) exposed as `GET/PUT /api/host-settings` with revision-checked updates, and a key/value `metadata` table that currently holds only an `initialized` marker. The schema is versioned with `PRAGMA user_version` (now 2); v1 to v2 was a table migration with a backup. Desktop and server mode share this state.

**Error reporting.** Remote failures are `host.Error` values with sentinel codes. `mapHostError` turns them into wire strings of the form `code: diagnostic`; the frontend matches the code by substring and shows a localized message, never the raw code.

**Dependencies.** No SOCKS client is in the build. `golang.org/x/net` appears in the module graph through Wails but is not a requirement of this module and is not in `go.sum`.

**Frontend layout.** No control sets `line-height`, so every button and selector is sized by the metrics of whatever font renders its label. The UI font stack names no CJK family, so Chinese labels use the platform fallback font, which is taller than the Latin one. Controls are sized by their label plus padding. Measured on macOS with headless Chrome 158 on the current source, English versus Simplified Chinese: 10 of 14 controls in the top bar and sidebar differ in height, 9 in width; with an explicit control line-height the height differences drop to zero in every scene, and with group minimum widths the width differences drop to zero except in the add-host form, where descriptive text of different length shifts whole sections. The DOM tests run in jsdom and perform no layout.

**Packaging.** `scripts/package-desktop.sh` writes the macOS `Info.plist` inline without `CFBundleIconFile` and ships no `.icns`. Linux installs `packaging/desktop/visual-tmux-client.svg`, a hand-authored 128-unit SVG, into `hicolor/scalable/apps`. The plist's short version is hard-coded. CI does not run the packaging script; the Desktop compatibility workflow builds native binaries and runs the native tests on macOS and on Ubuntu under X11 and Wayland. macOS provides `iconutil`; no SVG rasterizer is installed on development machines or CI images.

**Native verification.** Native fixtures in `hub/cmd/visual-tmux-desktop` are selected with `VTC_NATIVE_FIXTURE_MODE`, run in real WebViews with private state, and report page observations by injecting JavaScript that posts to `/api/desktop/probe`.

## Goals / Non-Goals

**Goals:**

- One remote connection path, so no caller can accidentally dial directly.
- Proxy failures that a non-technical user can act on without understanding SOCKS.
- No behavior change for anyone who does not set a proxy.
- Commands in new local terminals use the same proxy without the user editing a shell configuration file.
- Control sizes that no longer depend on fonts or rendering engine.
- One artwork source for both platforms, with packaging that needs no extra local toolchain.

**Non-Goals:**

- Proxy authentication, HTTP `CONNECT` proxies, per-host proxies, system proxy detection and `~/.ssh/config` support.
- Passing the proxy to sessions on remote hosts, changing the environment of sessions that already exist, and programs that do not read the standard proxy variables such as `ssh`.
- Moving the existing language and font-size controls into the new settings dialog.
- Changing line height of body text or making session rows language-independent; the geometry rules cover controls only.
- An icon for the disposable "Compatibility Spike" bundle that CI builds for native tests.
- A responsive layout; the sidebar keeps its fixed width.

## Decisions

### 1. One dial function with an explicit route

Add a single function in `hub/internal/host` that opens the transport for a remote endpoint given the proxy address to use (empty means direct). `dialAndHandshake` and `ProbeEndpoint` both call it. The proxy address is read from host settings at the moment a connection is opened, by the code that already resolves the target (the connection manager, and the HTTP layer for probes, which passes it into `probeDialer`).

*Why:* a second dial site that forgets the proxy is the most likely bug in this change. Reading the setting per connection means a changed proxy applies to new connections without cached state or invalidation, which is what the spec requires for active terminals.

*Alternatives:* caching the setting inside the connection manager with an invalidation hook (extra state and a stale-value risk for no gain); routing at the socket level (not available in Go's standard library).

### 2. A small in-tree SOCKS5 client

Implement only SOCKS5 `CONNECT` without authentication (RFC 1928): TCP connect to the proxy, method negotiation, a connect request, reply validation. No new dependency.

*Why:* the three failure classes in the spec map directly to typed errors if the client controls the protocol. `golang.org/x/net/proxy` would add a direct requirement for about a hundred lines and reports the proxy's reply as an error that does not expose the SOCKS reply code, so classification would rely on string matching.

*Details:*

- Host names are sent to the proxy as domain names (the proxy resolves them); IP literals are sent as addresses.
- `hosts.dial_timeout` is one budget covering the proxy TCP connect, the negotiation and the connect reply. The reply arrives when the proxy has reached the target or failed, so the target's connection time is inside this budget. The SSH handshake budget is unchanged and starts once the tunnel is open.
- The context cancellation pattern already used around the handshake is reused, so closing a probe cancels the proxy connection too.

### 3. Proxy failure classification

| Observation | Code | Wire code |
| --- | --- | --- |
| TCP connect to the proxy fails, or negotiation times out | proxy unreachable | `proxy_unreachable` |
| Proxy selects an authentication method | proxy authentication required | `proxy_auth_required` |
| Reply: general failure, not allowed by ruleset, TTL expired, or the proxy closes without a reply | proxy refused | `proxy_refused` |
| Reply: network or host unreachable, or the connect reply times out | target unreachable through proxy | `proxy_target_unreachable` |

All four use HTTP 503 like `host_unavailable`, carry the proxy address in the diagnostic (it is not a secret) and are recorded as connection failures by the existing pool backoff. Trust and authentication errors are produced after the tunnel is open and are unchanged. Each wire code gets a message in both locales that names the proxy address and says whether the problem is the proxy or the target host.

### 4. The setting lives in host state, in the metadata table

Store the proxy address under a key in the existing `metadata` table, updated in the same transaction as the settings revision check.

*Why:* no schema version change, no migration, and an older build opened on the same state ignores the key and dials directly, which is the behavior it had before.

*Alternatives:* a `settings` column with a v2 to v3 migration (consistent with `default_host_id`, but it forces a schema bump and makes older builds refuse the state for one optional value); the desktop preferences file (desktop-only, meant for UI preferences, and the connections are made by the backend that server mode shares).

### 5. Backward-compatible settings API

`GET /api/host-settings` returns `proxyAddress` next to `defaultHostId` and `revision`. For `PUT`, a field that is absent from the JSON body is left unchanged; an explicit `null` or empty string clears it.

*Why:* the existing frontend always sends `defaultHostId` explicitly, so absent-means-unchanged breaks no caller, while the new settings dialog, which only edits the proxy, must not clear the default host.

*Validation:* an optional leading `socks5://` is stripped, because proxy clients display their address in that form. The remainder must be `host:port`, where the host is an IPv4 literal, a bracketed IPv6 literal or a hostname, and the port is 1 to 65535. Other schemes and malformed values are rejected with `invalid_proxy_address`; the stored value is always the normalized `host:port`.

### 6. A settings dialog in the workspace

Add a settings dialog that follows the existing host-manager dialog pattern (labelled by its title, focus trapped, Escape and close button). It opens from a Settings button in the top-bar actions, before the Hosts button, so it is reachable with the sidebar collapsed. In this change it contains one section: the proxy address field, Save and Cancel. The field carries one sentence and nothing else: with a saved proxy it names that proxy and says that remote connections and new local terminals use it, with none it says that connections go direct and what a proxy covers. The input's example shows the `host:port` form, and a second short sentence appears only while an address is entered, saying that proxy variables set in the shell's own configuration take precedence.

*Why here:* the workspace is shared by desktop and browser mode, and the setting belongs to the backend that dials, so both get the same field. A YAML key was rejected because the people this is for do not edit YAML.

*Behavior:* Save sends the current revision; on a revision conflict the dialog reloads the settings, keeps what was typed and says the setting changed elsewhere. The field has a visible label, an accessible description and an inline error.

### 7. The connection path is part of host status

The per-host capability report gains a `connectionPath` value (`direct`, or `proxy` plus its address) for SSH hosts and omits it for the local host. The host card shows it.

### 8. One artwork source, generated icon files are committed

```
   packaging/desktop/icon/
     app-icon.svg        master, 1024-unit grid
     app-icon-small.svg  simplified, for 32 px and below
            |
            |  scripts/generate-app-icon.sh   (rsvg-convert + iconutil, run on demand)
            v
   visual-tmux-client.svg   Linux launcher icon     } committed; packaging only copies
   visual-tmux-client.icns  macOS icon, 10 sizes    }
```

- *Master artwork.* The existing terminal glyph is redrawn on the Apple icon grid (1024 canvas, 824 body, 100 margin, corner radius about 184). macOS does not mask icons, so this is what keeps it the same visual size as its neighbors. The Linux scalable icon is generated from the same master, so both platforms show identical art.
- *Small sizes.* The glyph's strokes are under one pixel at 16 px. Representations of 32 pixels and below come from a simplified variant with thicker strokes.
- *Committed outputs.* The generation script needs an SVG rasterizer that is not present on development machines, so its outputs are committed and `package-desktop.sh` only copies them. The Desktop compatibility macOS job regenerates into a temporary directory and checks that the committed `.icns` contains all ten representations at the right pixel sizes, not byte equality, because rasterizer versions differ.
- *Bundle.* `package-desktop.sh` copies the `.icns` into `Resources`, adds `CFBundleIconFile`, and takes the bundle version from the same version value as the archive name instead of the hard-coded `0.6.0`. The existing `codesign --verify --strict` then also proves the icon is inside the sealed resources.

*Alternatives:* generating at package time only in CI (the documented local packaging path would need the toolchain); rendering the SVG with a headless browser (not available in CI images and not reproducible); keeping the Linux SVG as authored and adding a separate macOS drawing (two artworks to keep in sync).

### 9. Control sizing rules

Define the rules once in `style.css` as tokens and apply them to every control.

- *Height.* Each control kind gets a fixed height token (three sizes: compact for the sidebar mode and tool buttons, default for ordinary buttons and selectors, large for the full-width sidebar buttons). Controls are inline flex containers that center their label, with `line-height: 1` and `white-space: nowrap`, so no font metric can change the box. Selectors and inputs get an explicit height.
- *Width.* Each control group gets a minimum-width token sized to fit the longest label among the supported languages, rounded up, with the label centered. Full-width buttons keep filling their container.
- *Unchanged.* Descriptive text keeps natural wrapping. Body line height is not changed.

*Why tokens rather than automatic measurement:* the set of labels is closed and changes only with the locale files; a hidden measuring pass on every render would cost layout for no visible gain. The real-WebView check below fails if a label no longer fits its token.

*Alternative:* relying on an explicit `line-height` alone (fixes height in the measured scenes but still lets fallback fonts decide the glyph position, and does nothing for width).

### 10. Geometry is verified in the real WebView

Add a native fixture mode that runs with private state and synthetic sessions like the existing ones. After the workspace renders, the native side injects a script that:

1. switches language through the real language selector, so the app's own live-switch code path runs;
2. for each language, measures every visible button, selector and input in three scenes (sidebar expanded, host manager list, add-host form), keyed by position in the DOM because labels differ;
3. posts both snapshots through the existing probe endpoint.

The native test fails on any height or width difference, and on any label that overflows its control. It runs in the macOS job and in the Ubuntu X11 and Wayland jobs through the existing native test entry points, and is skipped when no display is available, like the other native tests.

*Why:* jsdom performs no layout, and the reported symptoms differ between WebKit on Linux and the macOS WebView, so the check has to run on both engines the application actually ships on.

### 11. New local sessions receive the proxy as environment variables

The local tmux client holds an optional function that returns the variables for the sessions it creates. `app.New` installs one that reads the proxy address from host settings. `Create` calls it each time and passes the result as `new-session -e NAME=value`. The remote session backend passes none.

*Variables:* `http_proxy`, `https_proxy` and `all_proxy`, each also in upper case, with the value `socks5h://host:port`; `no_proxy` and `NO_PROXY` with `localhost,127.0.0.1,::1,.local`. An empty proxy address yields no variables at all.

*Why:* reading the setting when a session is created means a changed address applies to the next session with no cached state to invalidate, the same reasoning as decision 1. The scheme is `socks5h` because the setting is a SOCKS5 proxy and because names must be resolved by the proxy, as in decision 2. Both cases are set because tools differ in which one they read. The exclusion list keeps a proxy set for everything from pulling local services into the proxy.

*Alternatives:* `set-environment -g` on the tmux server (changes server-wide state that also reaches sessions the user creates outside the application, and needs updating whenever the setting changes); passing the variables in the environment of the `tmux` client process (tmux copies only the variables named in `update-environment` into a session); a switch in the settings (the setting itself is the switch, and a shell startup file remains the way to override it); injecting on remote hosts too (the address names the user's own machine, so it would only break commands there).

*Limits, stated in the settings dialog and the documentation:* the variables reach sessions the application creates after the setting is in effect, and the windows opened in them. Programs that ignore these variables are unaffected. A shell startup file that sets the same variables runs later and wins. If the setting cannot be read when a session is created, creation fails with that error instead of starting a session that silently has no proxy.

## Risks / Trade-offs

- [Rendering engines disagree about fallback font metrics] → Controls no longer depend on font metrics, and the geometry check runs on WebKit on both operating systems, not only in Chrome.
- [The real-WebView check depends on CI display fixtures] → It reuses the fixtures the native tests already require and skips without a display exactly as they do.
- [A proxy that closes the connection without a reply is ambiguous] → Reported as "proxy refused" with a diagnostic that says the proxy closed the connection; still distinct from "proxy unreachable".
- [With a proxy set, host names are resolved by the proxy] → Intended: the proxy's own routing rules apply. IP-literal hosts are passed through unchanged.
- [One more round trip before the SSH handshake] → Inside the existing dial budget; no new timeout or configuration.
- [After a rollback, an older build silently dials directly] → Accepted and documented; it is the behavior the older build always had.
- [Minimum widths are hand-maintained] → Tokens are grouped by control kind and documented; the geometry check fails when a label stops fitting.
- [Chinese labels get more horizontal padding] → Accepted during exploration after reviewing comparison sheets; labels are centered.
- [Committed icon binaries can drift from the SVG sources] → CI regenerates and checks structure; the regeneration command is documented next to the sources.
- [Programs in a terminal do not all read the proxy variables, and some accept only HTTP proxies] → The setting is a SOCKS5 proxy, so the value is `socks5h://`; the documentation names what works (`git` over HTTPS, `curl`) and what does not (`ssh`, `git` over SSH).
- [The user's shell startup file sets the same variables] → It runs later and wins; the settings dialog says so whenever an address is entered.
- [The tmux global environment carries proxy variables of the user's own] → A session created with a proxy set overrides them for as long as it exists; with no proxy set the application adds nothing and the user's value applies as before.
- [Three unrelated areas in one change] → Tasks are grouped into independent sections, but archiving is all or nothing; a delay in one area holds the others.
- [Session rows and body text still follow platform font metrics] → Out of scope; recorded as a possible follow-up.

## Migration Plan

No data migration. The proxy setting is empty by default, so an upgraded installation dials exactly as before until the user enters an address. The icon and the control sizing ship with the package and the frontend. Rollback is a normal redeploy of the previous build: it ignores the stored proxy address and dials directly, and the previous frontend does not read `proxyAddress`. Sessions created by the previous build keep the environment they were created with.

## Open Questions

- The exact pixel values of the height and width tokens; they are chosen by measuring both languages in the real WebView during implementation.
- Whether the collapsed sidebar rail also gets a settings shortcut.
- Whether the macOS corner is a plain rounded corner or a path that approximates the system's continuous curvature.
