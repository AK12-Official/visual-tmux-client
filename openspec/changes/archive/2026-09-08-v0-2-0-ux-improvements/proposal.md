# Proposal: v0.2.0 UX Improvements

## Why

v0.1.0 shipped the browser session manager, and daily use has since surfaced one usability defect trio plus a batch of missing affordances (tracked in the author's problems list):

- **A detached or ended terminal can never be re-attached from the UI.** Three causes stack: the hub's `exit` frame permanently sets `ended` in `terminal.ts` (correct for a killed session, wrong for a plain `Ctrl-b d` detach, which leaves the session alive); re-clicking the already-selected row changes nothing; and `KeepAlive` caches the dead `TerminalView` instance, so even switching away and back restores the corpse rather than re-attaching.
- **Auto-generated session names are not actually unique.** `generateSessionName` (`hub/tmux.go:220`) documents "appending a random suffix if the base collides" but appends nothing — two empty-name creates in the same second return `ErrNameInUse`, violating the session-hub requirement "the hub SHALL generate a unique one".
- **A token supplied via `VISUAL_TMUX_CLIENT_TOKEN` is invisible.** `resolveToken` prints only tokens it generates itself, so `VISUAL_TMUX_CLIENT_TOKEN="$(openssl rand -base64 32)" ./visual-tmux-client` starts a hub whose credential exists only inside that child process — the user cannot type it into the browser.
- **Session names reject all non-ASCII input** (`^[A-Za-z0-9._-]{1,64}$` in `hub/tmux.go:28`), although tmux 3.7b itself accepts CJK and even spaces (verified empirically on an isolated `-L` socket) and the whole argv/URL/JSON path is already UTF-8-clean.

Beyond those fixes, the UI lacks what sustained terminal work needs: notifications that coexist and expire instead of one gray banner, one-click session creation, batch kill, awareness of which background session just produced output, terminal chrome (font size, fullscreen, per-panel close), manual ordering and pinning, a collapsible sidebar, and any in-app help. This change delivers the fixes and those affordances together as **v0.2.0** (minor version: new compatible functionality, per the project's release workflow).

## What Changes

- **Fix: re-attachable terminals.** When a terminal view ends, an overlay distinguishes "detached — session still alive" (offers Reconnect, which rebuilds the terminal session) from "session gone" (states the session ended). Dead `TerminalView` instances no longer stay cached behind `KeepAlive`.
- **Fix: genuinely unique auto names.** Empty-name creation retries with a `-1`, `-2`, … suffix on collision instead of failing.
- **Fix: visible token.** The hub prints its effective access token and listen address at startup, whether the token was generated or taken from the environment.
- **Unicode session names.** Validation relaxes to: valid UTF-8, no control characters, no `:` or `.` (tmux target-syntax ambiguity and older-tmux compatibility), no leading/trailing whitespace, at most 64 runes. CJK names work end to end; the error message becomes human-readable.
- **Toast notifications.** The single-notice banner is replaced by a stack of toasts: multiple messages coexist (a new one pushes the others up), each auto-expires, and levels — error / warning / info — are visually distinct.
- **One-click creation.** Session creation loses its name input: a single button creates a session with an auto-assigned default name; renaming remains the way to change it.
- **Batch management.** The session list supports a multi-select mode with bulk kill.
- **Activity awareness.** A session card whose terminal produced output recently gets a highlighted border (decays after ~2 s of silence); the selected terminal signals activity distinctly — a breathing dot in the terminal top bar plus a `●` prefix on the browser tab title, so output is noticeable even when the tab is backgrounded. This rides on the existing behavior that `KeepAlive` keeps background attachments streaming.
- **Terminal top bar.** The terminal panel gains a header: session title and connection state on the left; font size −/+ (px steps, persisted), fullscreen, and close (returns to the placeholder, does not kill the session) on the right.
- **Collapsible sidebar.** The session list can be collapsed to a narrow rail and restored; state persists in localStorage.
- **Session ordering.** Two sort modes — default (today's behavior) and manual (drag reorder, pin to top) — with the choice and the manual order persisted in localStorage.
- **Taller session cards.** Cards become a two-line layout (name / metadata) with more breathing room.
- **In-app help.** A help control opens a modal rendering an embedded tmux guide (Chinese, adapted from the author's desktop guide, plus a short "using this app" section). The guide ships inside the binary and needs no network access.

Explicitly **out of scope** for this change (deferred, not rejected):

- Multi-host agent, share links, file transfer, mobile input, themes — the deferred list from the previous change.
- Server-side persistence of ordering/pins — localStorage only this release; a hub-side preferences API can follow if the per-browser limitation hurts.
- An English edition of the embedded guide.
- Batch operations beyond kill (no bulk rename or detach).
- Auto-reconnect of ended terminals without an explicit user action.

## Capabilities

### New Capabilities

- `web-notifications`: leveled, stacked, auto-expiring toast notifications for action results and terminal events.
- `in-app-help`: an embedded tmux guide and brief app usage notes, reachable from a help control.

### Modified Capabilities

- `session-hub`: the session-creation name-validation requirement's permitted set is redefined (Unicode-safe rule above; the renaming requirement inherits it); a new requirement covers startup token disclosure.
- `web-session-manager`: creation flow becomes one-click with an auto name; reconnection requirements distinguish detached from ended and require a reconnect affordance; the session-list requirement extends to batch termination, ordering/pinning, activity indication, and sidebar collapse; a new terminal-chrome requirement covers font size, fullscreen, and close.

## Impact

- **Go** (`hub/`): `tmux.go` (name validation, auto-name uniqueness), `auth.go` + `main.go` (startup banner), tests in `tmux_test.go` / `auth_test.go`. No new Go dependencies; no API contract changes — every existing endpoint keeps its shape, and name validation only widens acceptance.
- **Web** (`hub/web/src/`): `App.vue` restructured (toast integration, sidebar collapse, top bar); `SessionList.vue` redesigned (quick create, multi-select, drag/pin ordering, taller cards, activity borders); `TerminalView.vue` + `terminal.ts` (reconnect overlay, activity hook, font size, fullscreen); new `ToastStack.vue` and `HelpModal.vue`; one new npm dependency (`marked`) for guide rendering.
- **Docs/assets**: tmux guide added at `hub/web/public/tmux-guide.zh-CN.md` (vite copies it into the embedded dist); `README.md` and `README.zh-CN.md` updated for name rules, token banner, and the new features; `Makefile` `VERSION` default bumped with the release.
- **Version**: v0.2.0.
