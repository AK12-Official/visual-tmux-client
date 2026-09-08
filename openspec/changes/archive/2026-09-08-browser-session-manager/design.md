## Context

See proposal.md — Why for the defect analysis motivating this change. This document covers how to build the replacement.

**Verified environment facts** (probed on the development machine, 2026-09-07):

- `tmux 3.7b`, `go 1.26.3 darwin/arm64`.
- `window-size` defaults to `latest` in tmux 3.7b, so multi-client size handling already behaves correctly by default. Setting it explicitly is defensive for older tmux, not a fix.
- `default-size` is `80x24` — this is what an unsized pty inherits, and is the concrete reason the current product renders incorrectly.
- `list-sessions -F "#{session_name}|#{session_windows}|#{session_attached}|#{session_created}"` returns `probe|1|0|1788785508` — a single pipe-delimited line per session, creation time as a Unix timestamp.
- Exact targeting is confirmed: with sessions `probe` and `probe-staging` present, `kill-session -t "=probe"` removed only `probe`.

**What survives from the existing codebase**: `engine/ringbuffer/` only, and with a changed contract (see the pre-ready staging decision). Everything else listed in proposal.md — Impact is deleted. There is no incremental path from the control-mode engine; treat this as a rewrite that reuses one small utility.

**Constraint carried forward**: tmux owns session lifetime. The hub must be freely restartable without disturbing running sessions, which rules out the hub holding any authoritative state about sessions.

## Goals / Non-Goals

**Goals:**

- One process, one static binary, embedded frontend assets, no runtime installation step beyond copying the binary.
- Terminal output correctness by construction: the hub never interprets terminal bytes, so there is no code path in which it can corrupt them.
- A host-addressing seam present from the start, so multi-host can be added later without breaking client code.

**Non-Goals** (design-level, beyond proposal.md's scope exclusions):

- No terminal emulation, screen-diffing, or output parsing anywhere in the hub. Any future feature requiring interpreted screen state must be argued separately.
- No persistent storage of any kind — no database, no state file. All state is either tmux's or in-memory and disposable.
- No abstraction layer for "future remote hosts" beyond the URL/API shape. Do not build a `Bridge` interface with one implementation on the theory that a second is coming; introduce it when the second arrives.

## Decisions

### Decision: Go for the hub, Vue 3 for the frontend

**What**: The hub is a single Go module producing one static binary with the built frontend embedded via `embed.FS`. The frontend stays Vue 3 + Vite + xterm.js, reusing the existing `shell/frontend` toolchain and its dependency versions.

**Why**: The user expressed no language preference, so this is decided on merits. Go gives a single statically-linked binary with no runtime dependency — decisive for the eventual multi-host phase, where an agent must be copied to arbitrary machines; the reference implementation's Node agent requires installing Node 18+ on every host. Go's `os/exec` with argv slices makes the shell-injection-safe invocation requirement structural rather than a discipline. Keeping Vue 3 avoids a rewrite with no user-visible benefit: the value in the reference implementation is its protocol and xterm.js integration, not React.

**Alternatives considered**: TypeScript/Node for both sides, to mirror the reference implementation and share types between server and client — rejected because the single-binary property is worth more than type sharing at this scale, and `node-pty` is a native addon that complicates distribution. Rewriting the frontend in React to match the reference — rejected as effort with no user-visible return.

### Decision: pty + `tmux attach-session` for streaming; one-shot `exec` for everything else

**What**: Two distinct mechanisms, never mixed:

1. **Metadata and mutation** — `os/exec` with an argv slice, capturing stdout: `list-sessions`, `new-session -d`, `rename-session`, `kill-session`, `has-session`.
2. **Live terminal** — `pty.Start` on `tmux attach-session -t =<name>`, streaming raw bytes both ways.

**Why**: This is the decision that fixes the product. tmux composes its own panes, split borders, status line, and copy-mode into the pty's byte stream, so the hub carries bytes rather than reconstructing a screen. Correct rendering of colors, full-screen applications, and layout follows automatically, because the hub has no representation of the screen that could be wrong. Sizing collapses to one `TIOCSWINSZ` call, after which tmux reflows everything itself.

**Alternatives considered**: Keep control mode (`tmux -CC`) and fix its defects individually — rejected: adding `capture-pane -e`, escape-aware buffering, flow control, and per-pane size negotiation reconstructs a terminal emulator inside the hub, and the per-pane size negotiation problem has no correct solution at all, since tmux sizes panes from the layout it maintains for its own clients. Control mode is the right tool for a native client that implements its own pane rendering (iTerm2's integration), not for streaming to a browser terminal.

### Decision: WebSocket with JSON control frames and binary data frames

**What**: One WebSocket per attached terminal. Control messages are JSON **text** frames; terminal bytes are **binary** frames in both directions. The frame's WebSocket opcode discriminates the two — no envelope, no base64.

Client → server:

| Frame | Payload |
|---|---|
| binary | raw input bytes, written to the pty verbatim |
| text | `{"type":"resize","cols":<int>,"rows":<int>}` |
| text | `{"type":"ping"}` |

Server → client:

| Frame | Payload |
|---|---|
| binary | raw output bytes, written to xterm verbatim |
| text | `{"type":"ready","session":"<name>","cols":<int>,"rows":<int>}` |
| text | `{"type":"exit","code":<int\|null>}` |
| text | `{"type":"error","message":"<string>","retryable":<bool>}` |
| text | `{"type":"pong"}` |

**Why**: Terminal I/O is binary. Base64-encoding it inside JSON — as the reference implementation does on both of its hops — costs 33% bandwidth plus encode/decode on every frame in both directions, for no benefit. WebSocket already carries a text/binary distinction, so using it removes the need for an envelope on the hot path. The split also makes the byte-faithfulness requirement structural: the data path has no field that could be transformed. `retryable` on errors exists so the client can implement the "do not retry a refused attachment" requirement without pattern-matching message strings.

**Alternatives considered**: All-JSON with base64 payloads, matching the reference implementation — rejected for the overhead above; the compatibility argument does not apply since we share no clients with it. Two separate sockets for control and data — rejected: doubles connection state and creates ordering hazards between a resize and the output that follows it.

### Decision: Size negotiation is client-measured, debounced, and applied with a forced repaint

**What**:

- The client derives `cols`/`rows` from `FitAddon.proposeDimensions()` — measured from the rendered DOM, never assumed — and sends them as query parameters on the WebSocket URL for the initial size, then as `resize` frames on change.
- The client debounces resize reporting at ~100 ms trailing edge, so a window drag produces few frames and the final one matches the final size.
- The hub validates `1..1000` per dimension, rejecting out-of-range values while retaining the last valid size.
- The hub calls `pty.Setsize` on the master fd, then issues `refresh-client -t <client> -C <cols>x<rows>` as a belt-and-braces measure for the attached client.
- On attach, the hub explicitly sets `window-size latest` (a no-op on tmux ≥3.1 where it is already the default, defensive for older versions).
- **Forced repaint**: if the size being applied equals the size tmux already has, `TIOCSWINSZ` produces no `SIGWINCH` and tmux does not redraw, leaving the user with a blank terminal until the next output. The hub therefore applies `rows-1` and then `rows` in quick succession to guarantee a repaint. Because tmux is doing the drawing, this reliably repaints the whole screen — the hub does not need any scrollback of its own for the initial view.

**Why**: This directly satisfies the sizing requirements and is the second half of the correctness fix. Measuring rather than assuming is what makes wrapping correct; the forced repaint is what makes attach feel instant instead of blank. The dummy-resize trick and `window-size latest` are both borrowed from the reference implementation, which arrived at them for the same reasons.

**Alternatives considered**: Have the hub pick a size and let the browser scale the result — rejected: produces either letterboxing or unreadable text, and wastes the user's viewport. Send `resize` on every resize event without debouncing — rejected: a window drag would issue hundreds of `TIOCSWINSZ` calls and tmux reflows on each.

### Decision: Auth is a server token plus single-use, target-bound WebSocket tickets

**What**:

- A single shared secret. Read from `VISUAL_TMUX_CLIENT_TOKEN`; if unset at startup, the hub generates 32 random bytes, prints the token to stderr once, and uses it for that run. It never starts unauthenticated.
- JSON API calls authenticate with `Authorization: Bearer <token>`, compared using `crypto/subtle.ConstantTimeCompare`.
- Terminal attachment does **not** accept the token. The client calls `POST /api/ws-ticket` with `{"session":"<name>"}` and receives an opaque ticket: 24 random bytes, base64url, **30-second TTL, single-use, bound to that session name**. The ticket is passed as `?ticket=` on the WebSocket URL and consumed on redemption — deleted from the store before validity is even checked, so a replay cannot succeed regardless of outcome.
- `Origin` is validated on WebSocket upgrade against the configured public origin, defaulting to the bind address.
- The hub binds `127.0.0.1` by default.
- The pty child's environment is scrubbed of `TMUX`, `TMUX_PANE`, and `VISUAL_TMUX_CLIENT_TOKEN` before spawn.

**Why**: The product moves from an embedded webview with no listening socket to an HTTP server, so unauthenticated access would mean local-network remote code execution. Tickets exist specifically because browsers cannot set headers on a WebSocket handshake: without them the long-lived token would have to travel in the URL, where it lands in browser history, server logs, and proxy logs. A 30-second single-use target-bound ticket has almost no value if leaked. Scrubbing `TMUX` also prevents the child from thinking it is nested inside a tmux session.

**Alternatives considered**: Signed JWTs as the primary credential, as the reference implementation uses — rejected as unnecessary without multi-user identity; a shared token plus tickets is strictly less machinery for the same protection at this scope. Passing the token as a WebSocket subprotocol — rejected as a misuse of the field that some proxies mangle.

### Decision: Backpressure by pausing pty reads, gated on WebSocket buffer depth

**What**: The output goroutine checks the WebSocket's outbound buffer depth before each read. Above 1 MB it stops reading the pty; it resumes below 128 KB, polling at 100 ms. Because it stops reading, the kernel's pty buffer fills and tmux itself blocks on write — backpressure propagates to the producer rather than accumulating in the hub.

**Why**: Without this, `cat` on a large file grows hub memory without bound. Letting the pty buffer fill is the correct mechanism: it is exactly how a real terminal applies backpressure, and it needs no queue of our own.

**Alternatives considered**: An unbounded output queue — rejected, that is the memory-growth bug. Dropping output when behind — rejected outright: this is precisely the silent-drop defect in `eventbus/bus.go:140-144` that this change exists to eliminate. Terminal output cannot be dropped, because a lost escape sequence corrupts all subsequent rendering.

### Decision: `ringbuffer` is repurposed for pre-ready staging, with whole-frame eviction

**What**: `engine/ringbuffer/` is kept but its contract changes. It becomes a bounded FIFO of **output chunks** (not a flat byte array) staging output produced between pty spawn and the client's `ready`, capped at 2 MB. On overflow it evicts whole oldest chunks.

The byte-boundary truncation in the current implementation (`buffer.go:39-48`) **must be removed**: slicing a byte stream at an arbitrary offset can cut an escape sequence or a multi-byte character in half, which is the corruption described in proposal.md. Chunk-granular eviction cannot do that.

**Why**: There is a real window between spawning the pty and the client being ready in which tmux's initial repaint arrives, and dropping it would leave a blank screen. Bounding it prevents a client that never becomes ready from growing memory without limit. Evicting whole chunks loses a contiguous prefix of history — acceptable and self-correcting, since tmux repaints — whereas cutting mid-sequence corrupts everything after it.

**Alternatives considered**: Delete `ringbuffer` and spawn the pty only after `ready` — rejected: it adds a round-trip to first paint and still needs a buffer for output racing the transition. Keep byte-granular truncation — rejected, it is the defect.

### Decision: Session list is polled, not pushed

**What**: The client polls `GET /api/sessions` every 5 seconds while the list view is visible, and re-fetches immediately after any successful mutation. No WebSocket, no SSE, no `%sessions-changed` subscription.

**Why**: Satisfies "reflects change without user action" at a fraction of the complexity of a push channel. The data is a handful of rows; the reference implementation reaches the same conclusion (5 s polling, no refresh button). A 5-second staleness window on a session list is imperceptible, and mutations refresh immediately anyway.

**Alternatives considered**: A dedicated events WebSocket — rejected: needs its own auth, reconnection, and lifecycle for a list of a few rows. Control mode's `%sessions-changed` — not available, control mode is gone.

### Decision: Host is in the URL from the start, with one hard-coded value

**What**: Every route is host-addressed — `GET /api/hosts/:hostId/sessions`, `WS /ws/:hostId/:session` — but `:hostId` accepts only `local` in this change; anything else is `404 host_not_found`.

**Why**: The API shape is the expensive thing to change later, because it is baked into every client call site. Reserving the segment now costs one path parameter and makes the multi-host phase additive. This is deliberately *only* the URL shape: no bridge interface, no registry, no indirection behind it. Those arrive with the second host, not before.

**Alternatives considered**: Omit the host segment and version the API later — rejected, a mechanical but repo-wide edit that is free to avoid now. Build the full local/remote bridge abstraction now — rejected as speculative generality; one implementation behind an interface is harder to read with no present benefit.

### Decision: Structure as a fresh `hub/` module; delete `engine/` and `shell/`

**What**:

```
hub/
  main.go              flag/env parsing, signal handling, graceful shutdown
  server.go            router, middleware, static asset serving, embed.FS
  auth.go              token comparison, Origin check
  tickets.go           in-memory ticket store with TTL + single-use semantics
  api.go               JSON handlers for session list/create/rename/kill
  tmux.go              exec wrapper: argv construction, name validation, output parsing
  attach.go            pty spawn, WebSocket pump, resize, backpressure
  ringbuffer/          moved from engine/, contract per the staging decision
  web/                 Vue 3 + Vite frontend (moved from shell/frontend)
```

`engine/` and `shell/` are deleted outright. There is no compatibility shim and no deprecation period; nothing external depends on them.

**Why**: The remaining code shares no abstractions with the old engine, so restructuring it in place would mean editing every file and inheriting a package layout designed around control mode. A clean module is smaller and more readable. `hub/web/` sits inside the module so `embed.FS` can reach the build output.

**Alternatives considered**: Keep `engine/` as the module root and rewrite its packages — rejected, the layout (`tmuxcm`, `tmuxconn`, `eventbus`, `domain`) is entirely control-mode-shaped and every package is deleted. Keep the Wails shell alongside the hub during transition — rejected, it depends on the deleted engine, so it cannot build.

### Decision: xterm.js addons — `fit` and `unicode11`; no WebGL

**What**: Load `@xterm/addon-fit` (measurement for size negotiation) and `@xterm/addon-unicode11` (correct CJK/emoji width). Do **not** add `@xterm/addon-webgl` or the canvas renderer; keep xterm's default DOM renderer.

**Why**: `unicode11` matters because incorrect wide-character width computation causes cursor drift — a rendering defect in the same family as the ones being fixed. The DOM renderer is retained deliberately: it falls back to system fonts for CJK glyphs, whereas the WebGL renderer requires glyphs present in the loaded atlas and renders missing ones as blanks or tofu. The reference implementation makes this exact choice for this exact reason. Search and serialize addons are not needed by any requirement in scope.

**Alternatives considered**: WebGL for throughput — rejected, it trades a correctness property (CJK fallback) for performance the product does not currently need.

## Risks / Trade-offs

- [Risk] **Losing per-pane addressing is irreversible within this architecture.** Any future feature needing to read or write one specific tmux pane — per-pane capture, sending a command to a named pane, a pane-level status API — cannot be built on the attach stream, because the hub cannot see inside it. → Mitigation: such features are served by *additional* one-shot `exec` calls (`capture-pane -p -e -t <pane>`, `send-keys -t <pane>`) alongside the stream, which is how the reference implementation exposes its pane API. The attach stream stays uninterpreted; the exec path answers pane questions. This is a real constraint, not a hidden one.
- [Risk] **The forced dummy resize (`rows-1` then `rows`) is empirical.** It could visibly flicker, or could stop working on a future tmux. → Mitigation: implement it as one clearly-commented function with the reasoning recorded, and include a manual verification step for attaching to a session with static content (e.g. a finished `ls`) where a missing repaint is immediately obvious. If flicker appears, `refresh-client -C` alone may suffice on modern tmux.
- [Risk] **Two clients attached at different sizes: the smaller one sees a cropped or padded view.** `window-size latest` makes tmux follow the most recently active client, which is the best available behavior but not perfect for either party. → Mitigation: accept it; it matches native tmux behavior with two differently-sized terminals, so it will not surprise users. Do not attempt to paper over it.
- [Risk] **A shared token has no per-user identity**, so all callers are equally privileged and the token cannot be revoked for one party. → Mitigation: acceptable for a single-user, loopback-bound tool. Note explicitly that multi-host and sharing phases must revisit this; do not build sharing on top of a shared token.
- [Trade-off] **Polling costs a request every 5 seconds per open client.** Negligible for a local tool; would need reconsideration only at a scale this design does not target.
- [Trade-off] **Deleting ~4,400 lines including ~1,500 lines of tests discards genuine investment**, notably the control-mode parser and its test suite. → Those tests validate a component whose output has no consumer. Keeping it would mean maintaining a tmux protocol parser for nothing.
- [Risk] **The hub cannot detect that its pty's tmux client was detached externally** (e.g. another client runs `detach-client -t`). The pty closes, which the hub sees as EOF and reports as `exit`. → Mitigation: correct behavior as-is; the client shows the session ended and the user can reattach from the list. Worth an explicit verification step because it is easy to mistake for a bug.

## Migration Plan

No data migration: the hub stores nothing, and tmux sessions are untouched by the transition.

Sequenced so the tree always builds:

1. Create `hub/` with the tmux exec wrapper and JSON API. Verify with `curl`, no frontend.
2. Add pty attach and the WebSocket pump. Verify with a WebSocket CLI client, no frontend.
3. Move `shell/frontend` to `hub/web/`, strip Wails bindings, retarget to the HTTP API. Verify in a browser.
4. Move `engine/ringbuffer` to `hub/ringbuffer` and change eviction to whole-chunk.
5. Delete `engine/` and `shell/`.

Rollback: this is a pre-1.0 project with no users and, notably, **no git repository** (`git status` fails in the project root). There is no revert path once files are deleted. Steps 1–4 must be complete and verified before step 5 runs, and step 5 should not run until the browser client is confirmed working. Initializing git before starting this work is strongly recommended and is listed as the first task.

## Open Questions

- Should the generated token be persisted to a file (e.g. `~/.visual-tmux-client/token`) so it survives restarts, or printed fresh each run? Printing is fine for development; persistence is a small addition that does not affect the specs, the transport, or the task breakdown, so it can be settled during implementation.
- Whether to expose `default-terminal`/`TERM` as configuration. `xterm-256color` is correct for every case in scope; a truecolor-related need would be additive.
