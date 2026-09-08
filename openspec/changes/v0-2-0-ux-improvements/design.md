# Design: v0.2.0 UX Improvements

## Context

See proposal.md for motivation. The relevant current-state facts that shape this design:

- `App.vue` owns one global `notice` ref — a single message slot with no expiry and no leveling. Every error path (create/rename/kill, terminal notices) funnels through it.
- `TerminalView` is rendered inside `KeepAlive` with `:key="selected"`; switching sessions **deactivates** (not unmounts) the previous view, so its `TerminalSession` keeps its WebSocket open and keeps receiving output in the background. `terminal.ts` sets `ended = true` on the hub's `exit` frame, which permanently suppresses reconnection — correct for a killed session, wrong for a detach, and there is no code path that ever builds a fresh attachment afterwards.
- Name validation lives in `hub/tmux.go` (`validateSessionName`, ASCII-only regexp). The argv/URL/JSON path is already UTF-8-clean end to end; tmux 3.7b itself accepts CJK and spaces (verified on an isolated `-L` socket).
- `hub/server.go` already `go:embed`s `hub/web/dist`; vite copies `hub/web/public/` verbatim into `dist` — an embedded help document needs no new server route.
- The frontend has no state-management library and no test runner; components communicate via props/events. Any new shared state should follow that scale, not introduce Pinia.

## Goals / Non-Goals

**Goals:**

- Every user-visible failure or event flows through one toast pipeline with levels and lifetimes.
- A terminal that has ended can be re-attached with one explicit action; detaching is clearly distinguished from the session being gone.
- All new client-side preferences (font size, sidebar state, ordering) survive reloads via localStorage with a single `vtc:` key namespace.
- No breaking API changes; the hub's JSON/WebSocket contracts keep their shape.

**Non-Goals** (beyond proposal-level out-of-scope):

- No state-management library, no component framework patterns beyond props/events + one small composable.
- No redesign of the auth screen or theming work; new UI elements reuse the existing CSS custom properties (`--th-*`).
- No server-side preferences API.

## Decisions

### D1. Re-attach: overlay in `TerminalView`, reconnect by rebuilding `TerminalSession`

The `ended` flag in `terminal.ts` keeps its current meaning (suppress **automatic** reconnect) but stops being terminal for the view. `TerminalView` gains an overlay shown when its connection state is `ended`:

- If the session still appears in the polled session list → the overlay says the session is detached and offers **Reconnect**, which disposes the old `TerminalSession` and constructs a new one inside the same component instance. No `KeepAlive` eviction is needed — the component instance is reused, only the terminal session object is rebuilt.
- If the session no longer exists → the overlay states the session ended and offers no reconnect.

Session liveness comes from the list `App.vue` already polls every 5 s (passed down as a prop), not from a new endpoint. *Alternative considered:* making the hub's `exit` frame carry a reason (detach vs kill). Rejected — the attach process exit code does not reliably distinguish them across tmux versions, and the list already knows the truth.

### D2. Toasts: a module-level reactive store, not prop drilling

A new `src/toasts.ts` exposes `notify(level, message)` plus a `useToasts()` composable over a module-scoped `ref<Toast[]>`. `ToastStack.vue` renders the stack (newest at the bottom, pushing earlier ones up, per the spec). Levels: `error` (8 s), `warning` (5 s), `info` (3 s); stack cap 5, dropping oldest. Each toast self-expires via its own `setTimeout`; manual dismiss clears it.

This is a store rather than props because emitters live at three depths (`App.vue` handlers, `TerminalView`, and `terminal.ts`, which is not a component at all). A tiny composable keeps the app's no-Pinia character while avoiding threading a callback through every layer. *Alternative considered:* an event bus. Rejected — same wiring cost, less type safety.

### D3. Unicode names: code-point walk in Go, not a bigger regexp

`validateSessionName` becomes a single pass over the string's runes checking, in order: non-empty; `utf8.ValidString` (guaranteed for Go strings from JSON, checked anyway for defense); no `unicode.IsControl` rune; no `:` or `.`; no leading/trailing whitespace; ≤ 64 runes. The returned error names the first violated constraint (the spec's "Name is rejected" scenario requires naming the constraint, and the current `must match ^[A-Za-z0-9...]$` message is the complaint that started item #5's example).

*Alternative considered:* a Unicode-property regexp (`^[\p{L}\p{N}...]+$`). Rejected — it either over-restricts (which characters are "letters enough"?) or degenerates into "almost everything", and it cannot produce a specific violation message.

Auto-name collisions: `CreateSession("")` retries the generated name with a `-1`…`-9` suffix (bounded, then a real error), instead of the current single-shot name that collides when two creates land in the same second.

### D4. Token banner: printed in `main.run()`, always

`resolveToken` stops printing; it returns the token and its source (`environment` or `generated`). `run()` prints one banner before serving:

```
visual-tmux-client: token: <token> (source: environment)
visual-tmux-client: open http://127.0.0.1:7690
```

The `open` line derives from `cfg.addr`, substituting `127.0.0.1` when the host is `0.0.0.0`, `::`, or empty so the hint stays clickable. Printing an environment-provided credential to stderr is an accepted trade-off for a loopback-by-default single-user tool (the spec's loopback requirement is unchanged).

### D5. Activity: an `onActivity` hook riding the existing background streams

`terminal.ts` gains an optional `onActivity` callback fired on binary-message receipt, throttled to at most once per 500 ms per session. `App.vue` keeps `lastActiveAt: Record<name, number>` and a per-session decay timer that clears the highlight ~2 s after the last output — no global ticking clock needed.

- Non-selected sessions: `SessionList` renders a colored card border for sessions with a live highlight flag (`#9`).
- The selected session: a breathing dot in the terminal header plus a `● ` prefix on `document.title`, cleared on decay (`#10`). The title is only ever prefixed/un-prefixed around the app's static base title, so it cannot clobber anything dynamic.

This works specifically because `KeepAlive` already keeps deactivated views streaming. That is also the known cost — see Risks.

### D6. Terminal header in `App.vue`, font size as state + prop

The header (session name, connection state, breathing dot; font −/+, fullscreen, close) lives in `App.vue`'s main region above the terminal, not inside `TerminalView`, because it must render from `selected` and survive terminal rebuilds (D1). Font size is `App.vue` state persisted as `vtc:font-size` (default 13, clamped 8–24, ±1 px steps), passed as a prop; `TerminalView` watches it, applies `term.options.fontSize`, and re-fits. Fullscreen calls `requestFullscreen()` on the terminal container; the existing `ResizeObserver` in `terminal.ts` already fires on the box change and re-negotiates size, so no extra fullscreen-change handler is required. Close sets `selected = null` (the view deactivates and keeps streaming — the session survives, and its activity can still be indicated).

### D7. Sidebar: collapse + footer help button

`collapsed` state persisted as `vtc:sidebar-collapsed`; expanded 260 px, collapsed a ~44 px icon rail (expand, create, help). The help button sits at the sidebar's bottom-left per the request. Width animates via CSS transition; the terminal re-fits through the same `ResizeObserver` path as D6.

### D8. Batch kill: an explicit select mode, sequential DELETEs

`SessionList` gains a select mode: a toggle makes each card show a checkbox, clicks toggle selection instead of navigation (navigation is suspended while the mode is on, removing ambiguity), and an action bar shows the count with a confirmed Kill. Kills issue one `DELETE` per session sequentially and report per-session failures as toasts. *Alternative considered:* a batch endpoint. Rejected for this release — N sequential calls at this scale need no server change, and per-session errors map cleanly onto the existing error contract.

### D9. Ordering: localStorage keys keyed by session name

`vtc:order` stores `{ mode, order: string[], pinned: string[] }`. Rendering in manual mode: pinned (in pinned order) → sessions in saved order → unseen sessions appended in default order. Renames rewrite the stored name in place; kills drop it. Drag reorder uses HTML5 drag-and-drop on the cards, enabled only in manual mode. The per-browser limitation is accepted (proposal out-of-scope).

### D10. Help: markdown in `hub/web/public/`, rendered by `marked`

The adapted Chinese guide lives at `hub/web/public/tmux-guide.zh-CN.md`; vite copies it into the embedded `dist`, so the hub serves it as a plain static file with no new route or embed directive. `HelpModal.vue` fetches it once (cache the rendered HTML), renders with `marked` into `v-html`, closes on Esc/backdrop, and is reachable from the sidebar footer and the collapsed rail.

`marked` becomes the only new npm dependency. Its output goes into `v-html` un-sanitized — the content is repo-controlled with no user input in the pipeline, so no sanitizer is added. *Alternative considered:* pre-rendering HTML at build time with a vite plugin. Rejected — more build machinery for no runtime gain; keeping markdown as the source of truth keeps the guide editable.

Guide adaptation (from the author's desktop copy): keep the fundamentals and cheat sheet, drop personal references, and add a "使用本程序" section covering token connection, create/rename, and what attach / detach / close mean — matching the in-app-help spec's content requirement.

### D11. Session cards: two-line layout

Cards become two rows (name, then meta + hover actions) with taller padding — CSS-only, folded into the `SessionList` redesign alongside D8/D9. Not spec-relevant (aesthetic).

## Risks / Trade-offs

- [Every session ever viewed keeps an xterm instance + WebSocket alive → memory grows with viewed sessions] → Accepted for this scale (tens of sessions); it is also what makes D5 possible. If it ever hurts, add an LRU cap on the `KeepAlive` cache — at the cost of losing activity indication for evicted sessions.
- [Dragging to reorder is poor on touch devices] → Manual sorting is desktop-first; mobile input remains deferred from the previous change.
- [`document.title` mutation can interact with browser extensions] → Prefix/un-prefix only around the static base title; worst case is cosmetic.
- [Unicode names may surprise external scripts that assumed ASCII session names] → The dangerous characters (`:`/`.`) stay forbidden; the rest is the user's own namespace.
- [Auto-name retry is still racy against concurrent external creates] → Bounded retries (10); tmux's own duplicate-session error is mapped to the retry path, so the race window is small and the failure is a clean `ErrNameInUse`.
- [Toast auto-expiry can hide an error the user wanted to re-read] → Errors get the longest lifetime (8 s), and any error state that matters beyond that (e.g. auth failure) still has its dedicated non-toast UI.

## Migration Plan

No persistent-format migration: the JSON API is unchanged, and all new client state is additive localStorage keys (`vtc:font-size`, `vtc:sidebar-collapsed`, `vtc:order`). Rollback is reverting the release; stale localStorage keys are inert. Release follows the project workflow: bump `hub/web/package.json` + `Makefile` `VERSION` to 0.2.0, update both READMEs, `make test`, package all four targets, tag `v0.2.0` after CI passes.
