## Context

See proposal.md for motivation and scope. Observed integration points:

- `hub/internal/app/app.go` owns HTTP/WS, terminal manager, file roots, registry and SSH pool. `Start` already accepts `127.0.0.1:0` and exposes the bound address; `Shutdown` closes attachments before HTTP, files and host resources. `app_test.go` covers this using fake backends.
- `hub/cmd/visual-tmux-client/main.go` loads configuration relative to the working directory, prints the credential banner and owns signal handling. Those semantics remain specific to the headless command.
- `hub/web/embed.go` embeds the Vite output. `api.ts` uses relative URLs and a bearer header; `terminal.ts` derives its WebSocket URL from the page origin. Serving the whole UI from the existing backend preserves same-origin behavior.
- Preferences currently use `storage.ts`, including language, font size, sidebar state, recent host and host-scoped ordering/pins. `i18n/index.ts` reads storage at module initialization, before `bootstrapApp`. Token storage currently uses the same localStorage wrappers.
- File download currently fetches an authenticated Blob and clicks an anchor. Clipboard and fullscreen use browser APIs. These are runtime compatibility boundaries, not reasons to rewrite the UI.
- CI packages headless binaries using `CGO_ENABLED=0` on Linux for four OS/architecture pairs. It has no native desktop build or GUI test job.
- Existing specs describe the server/browser entry. Its startup banner, config precedence, login and browser fullscreen remain intact. The new capability specifies the additive desktop entry; shared service guarantees still apply.
- No Wails prototype has run. The inspected Ubuntu 24.04 machine has GTK4/WebKitGTK runtime libraries but lacks `pkg-config` and development headers; no macOS execution environment has been demonstrated.

## Goals / Non-Goals

**Goals:**

- Keep terminal bytes on the existing binary WebSocket path, preserving size negotiation, bounded buffering and reconnect behavior.
- Keep one backend in the desktop process with deterministic ownership, failure handling and shutdown.
- Make native integration a narrow adapter so the ordinary browser bundle remains usable.
- Establish evidence from real WebViews before describing the desktop release as supported.

**Non-Goals:**

- Native widget rewrite, HTTP/WS removal, remote agent, Windows, cloud sync, automatic updates, system tray or autostart.
- Bundling/installing tmux, changing session lifetime, migrating browser-profile data automatically or changing host credential encryption.
- Replacing the existing headless packages or silently attaching the desktop to an independently running server.

## Decisions

### 1. Wails v3 as a window host with an early compatibility gate

Use Wails v3 with an exact version pinned after a small compatibility spike; v3.0.0 is the inspected baseline. Its `WebviewWindowOptions.URL` supports loading the loopback page; `Options.OnShutdown` provides cleanup. Keep framework bindings out of session, host, file-source and terminal operations.

Use a `desktop` build tag on the native entry/adapter and keep testable orchestration free of native imports. Ordinary headless `go test ./...` and CGO-disabled builds must not acquire GTK/Xcode requirements. Keep one Go module unless dependency inspection shows an actual need for another; no duplicate backend implementation.

Alternatives: Electron simplifies Chromium consistency but introduces a bundled browser and Go subprocess ownership; Wails v2 requires a different asset/runtime integration. Neither is an automatic fallback. If Wails cannot meet the gate, stop and revise this design before committing to another container.

The spike must establish real external-URL rendering, reload/bootstrap, source byte downloads and authenticated same-origin HTTP invocation of narrow native actions on both systems. Documentation alone does not pass the gate. It can use fake backend services first, avoiding incidental tmux mutations.

### 2. Add a desktop composition root and retain the existing transport

Add `hub/cmd/visual-tmux-desktop` with thin native wiring and `hub/internal/desktop` orchestration. Reuse `app.New`, embedded `web.FS()`, host registry and shutdown. Add optional desktop-only HTTP handlers through an explicit app option, absent in ordinary server mode.

```text
Desktop main (native UI thread)
  |
  +-- Desktop controller --> Existing App --> tmux / SSH / SFTP
  |                              ^
  +-- WebView --> loopback HTTP/WS+
  |
  +-- Narrow native actions: save dialog, clipboard, window integration
```

Startup order: parse informational flags; acquire desktop instance ownership; resolve config; generate a per-process bearer/bootstrap code; initialize durable state; start the loopback listener; verify readiness; create/load the window. Observe the server error channel throughout the run. On partial failure, close all resources already acquired exactly once and show a native startup error instead of a blank window. Do not emit the headless credential banner in desktop logs.

One desktop instance per OS-user application profile owns one main window; reopening focuses the running window. Keep the registry's existing single-owner lock authoritative: conflict with the headless command is an actionable error, not permission to reuse its token or kill it.

### 3. Launch-specific configuration without changing server precedence

The desktop entry reads explicit `--config` / existing config environment selection when present; otherwise it looks for `visual-tmux-client.yaml` in the OS user configuration directory for this application, never an arbitrary desktop working directory. Reuse strict YAML parsing, config-relative path resolution and non-launch operational settings.

Desktop transport settings are fixed: `127.0.0.1:0`, a fresh generated bearer and default same-origin WS policy. Ignore configured/env `server.addr`, `auth.token` and `websocket.origin` for desktop operation and report their names as desktop-managed settings without logging their values. Do not accept `--addr` on the desktop entry. Document this specialization and keep the original command unchanged. Help/version must not initialize a GUI, database or listener.

Retain existing state/master-key defaults and explicit paths so host enrollment is shared sequentially between launch modes; do not create parallel copies of credentials. Keep invalid config/state failures fail-closed.

Resolve explicit tmux configuration first. Otherwise use PATH, then desktop-only macOS executable checks at `/opt/homebrew/bin/tmux` and `/usr/local/bin/tmux`; never invoke a login shell. Missing tmux leaves remote hosts and host repair usable and yields an actionable local capability error, matching the existing per-operation failure model. Do not change tmux socket selection or set test isolation variables in product behavior.

### 4. Short-lived launch handoff and document bootstrap

Generate a high-entropy, one-use launch code valid for at most 30 seconds. Put only that code in the initial URL fragment, remove it using `history.replaceState` before loading the application, and redeem it with JSON POST to a desktop-only bootstrap endpoint. Validate exact bound origin/Host, request size and content type; respond `no-store`. The bearer never goes in a URL, command-line argument, child environment or log. Invalid, expired and reused codes disclose nothing. Keep bootstrap distinct from terminal tickets.

The response contains the bearer and allowlisted preference snapshot. Hold the bearer in memory with desktop-only sessionStorage for reload recovery; remove stale ordinary localStorage token values and do not include bearer data in durable preferences. A new process generates new credentials. Reload reads the document-session credential, authenticates and fetches preferences; a rejected credential produces a desktop initialization error with a native reload/reopen path, not a demand to copy a token. A native-controlled reload can mint a fresh launch code when recovery is needed. Code-bearing URLs must be redacted from framework diagnostics.

An ordinary browser opening the bare desktop URL receives no launch credential and cannot authenticate itself. Backend session/host/file routes retain their bearer checks; terminal attachments retain existing host/revision/session-bound tickets and origin validation. No token is added to public client configuration or public static HTML.

Split the frontend entry into minimal platform detection/bootstrap and deferred application imports. On desktop, complete authentication and preference hydration before importing modules such as `i18n/index.ts`, loading runtime configuration, or mounting Vue. On ordinary web entry, retain existing initialization/login flow. A desktop marker alone is not authorization.

### 5. Persist an allowlisted preference snapshot, independent of origin

Add a versioned `desktop-preferences.json` under the effective private state directory. Keep it separate from `hosts.db` and `master.key`. Store only existing UI keys: language, font size, sidebar state, recent host, and validated per-host ordering/pins. Bound total size (1 MiB) and key/string/collection sizes; reject unknown keys including auth and file drafts. Use private permissions and atomic same-directory replacement. Serialize updates to avoid lost state.

Install a synchronous in-memory preference adapter before importing Vue/i18n modules, seeded from the authenticated snapshot. Existing synchronous reads remain synchronous. Writes update memory and enqueue versioned, serialized authenticated snapshot persistence; an acknowledged save is durable. Flush pending changes before confirmed application quit; expose failures rather than reporting persistence success. Corrupt preference files are preserved for diagnosis, reported, and fall back to defaults without overwriting until a user makes a new preference change. Persistence errors do not redirect host selection or discard editor drafts.

Use browser localStorage unchanged outside desktop mode. No attempt to access an unrelated browser profile. Existing font clamping, locale validation, host-scoped ordering and removed-host behavior remain authoritative.

### 6. Adapt only browser-dependent actions

Add one frontend platform interface for save-download, copy text and fullscreen. Prefer proven browser/WebView behavior where it satisfies the tests. Use desktop-only JSON POST endpoints carrying the existing Bearer credential for native actions. Validate the exact bound Host and Origin before dispatch, reject absent/foreign/opaque origins, cap request bodies and expose no CORS permission. Go dispatches allowed actions to the native UI API; do not use Wails RawMessageHandler or generated runtime bindings as an authorization boundary. Deny off-origin navigation in the window; open explicitly activated HTTP/HTTPS links in the system browser, rejecting unsupported schemes. Do not expose arbitrary file writes, shell execution or generic unrestricted Go method invocation to page content.

Desktop downloads use an explicit save dialog for consistent semantics. Source reads must still pass the existing authenticated, host/revision-aware file service, `files.enabled`, root/account bounds and file-size limits. Bind the action to the host and connection revision captured when initiated; cancelling creates no file, overwrite requires confirmation, failed writes leave no partial replacement and are visible. Stream to a temporary destination and atomically finish rather than serializing large files as base64 bridge events. Native save permission authorizes only the selected destination and does not widen source access. The compatibility spike exercises the same authenticated HTTP dispatch used in production, including rejected foreign and missing origins. Native callbacks receive validated typed inputs and no arbitrary destination path from the page. Wails raw messages are ignored. This change was approved after Linux v3.0.0 was observed to supply only the top-level URL and no IsMainFrame/TopOrigin provenance.

Clipboard adaptation must preserve terminal Ctrl+C with no selection and platform copy/paste shortcuts; avoid menu accelerators consuming terminal input. Fullscreen must fill the terminal area and refit on entry/exit, with native integration only as needed. Fullscreen/window changes must also work under Linux Wayland and X11.

### 7. One shutdown state machine, including unsaved edits

Use `starting -> running -> confirming -> stopping -> stopped`, with startup failure entering cleanup. First release defaults to closing the only window quitting the application on both systems; provide macOS Quit/Cmd+Q and Linux Quit as equivalent user paths. This deliberate cross-platform default avoids an invisible backend. No tray/background mode is introduced.

A user close/quit checks the shared editor dirty state. Cancel returns to the same running workspace; explicit discard continues. No disk draft recovery is added. On confirmed quit, flush preferences, cancel work and invoke the existing attachment/HTTP shutdown budgets using a fresh context; make teardown idempotent across native quit, signal and backend-error paths. Ensure file roots, registry and SSH resources close even after window construction failure. Do not rely on code after native `Run()` returning.

OS termination signals use bounded cleanup rather than waiting indefinitely for UI confirmation; abrupt process death cannot promise unsaved-buffer recovery. Backend failure is a visible application error and tears down its owned resources; never silently reconnect to a different process. Shutdown must never kill tmux sessions or unrelated servers.

### 8. Native build and release evidence

Initial acceptance targets: Ubuntu 24.04 amd64, Linux Wayland and X11 sessions; macOS arm64 and amd64, with a deployment target of macOS 12+ where supported by the selected SDK/toolchain. Treat these as proposed release baselines, not evidence that every combination already works. Build and validate on matching native runners, preserving existing four headless artifacts and their validation.

Provide an Ubuntu amd64 DEB with native dependencies and desktop launcher/icon, and separate macOS architecture `.app` ZIP artifacts. Avoid promising a universal Linux binary/AppImage before dependency validation. The app embeds web assets; users need neither Node nor Go nor a separately started backend. tmux remains an external requirement for local hosts.

macOS signing/notarization runs only when publishing credentials are configured. Unsigned development artifacts are clearly labeled; signed/public release claims require signature/notarization verification. Native Linux build needs GTK4/WebKitGTK development packages; runtime package metadata declares corresponding runtime dependencies. Pin the desktop toolchain and record CGO/native dependencies without changing the headless build's runtime contract.

## Risks / Trade-offs

- [WebKit differs from Chromium, especially input method, clipboard and downloads] -> Gate release on real terminal/editor tests in each native WebView; jsdom is insufficient.
- [External URL loading does not prove Wails bridge availability] -> Use authenticated HTTP native-action endpoints; the Linux raw bridge does not provide sender-frame provenance. Verify this path per platform before claiming support.
- [Random ports invalidate browser storage origin] -> Hydrate the port-independent preference adapter before any module-level storage read.
- [Two launch modes share an exclusive registry] -> Focus an existing desktop instance; explicitly report other owners and retain the store lock.
- [Native dependencies complicate builds] -> Separate desktop build tags/jobs and publish a narrow tested Linux baseline.
- [Asynchronous preference writes or quit races lose changes] -> Serialize durable writes, surface errors and flush in the shared quit state machine.
- [No macOS validation available in the current session] -> Keep both macOS runtime checks and packaging evidence unchecked until performed on actual runners/devices.
- [Native quit and signals race] -> Own cancellation/cleanup once, keep UI callbacks nonblocking and enforce existing shutdown budgets.

## Migration Plan

1. Run the Wails compatibility gate with the existing embedded UI and fake services before dependent native integrations on each available platform. Shared transport-independent work can proceed with HTTP boundary tests while unavailable platform evidence stays pending; no platform acceptance is waived. Record results and any required implementation adjustments.
2. Add desktop orchestration/handlers and frontend platform bootstrap; retain headless behavior and existing data formats.
3. Add preference persistence and native actions, then lifecycle handling and full regression/GUI validation.
4. Add separate native CI/packages and usage/dependency/config documentation. Publish desktop artifacts only for combinations with completed acceptance evidence.
5. Rollback by using the unchanged headless command. Desktop preferences are additive and removable independently; host database and key retain their existing schema and backup requirements. No destructive state migration is planned.

## References

- Wails inspected baseline: https://github.com/wailsapp/wails/tree/v3.0.0/v3
- Window URL/options: https://github.com/wailsapp/wails/blob/v3.0.0/v3/pkg/application/webview_window_options.go
- Lifecycle/instance options: https://github.com/wailsapp/wails/blob/v3.0.0/v3/pkg/application/application_options.go
- Native dependencies/platform guidance: https://v3.wails.io/getting-started/installation/
- Linux packaging: https://wails.io/guides/build/linux/
- macOS packaging: https://wails.io/guides/build/macos/

These establish API feasibility, not runtime acceptance. Concrete versions and execution results belong in the implementation verification record.
