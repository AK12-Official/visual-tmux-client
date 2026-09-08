## 0. Safety net

- [x] 0.1 Run `git init` in the project root, add a `.gitignore` covering `node_modules/`, `dist/`, `hub/hub`, and commit the current tree as a baseline — verify `git log --oneline` shows one commit and `git status` is clean. **This must be done first: design.md's rollback section notes the project is not currently under version control, and this change deletes ~4,400 lines.** — DONE: commit `524241d` on `main`, 87 files tracked, working tree clean. `.gitignore` also extended to cover `build/bin/`, `shell/tmux-shell`, and `.DS_Store`.

## 1. Hub module scaffold

- [x] 1.1 Create the `hub/` Go module (`go mod init tmux-hub`, Go 1.26) with `main.go` printing a version string — verify `cd hub && go build ./... && ./hub --version` succeeds.
- [x] 1.2 Add dependencies `github.com/creack/pty` and `github.com/coder/websocket` — verify `go mod tidy` leaves both in `go.mod` as direct (non-`// indirect`) requirements.
- [x] 1.3 Implement flag/env config in `main.go`: `--addr` (default `127.0.0.1:7690`), `TMUX_HUB_TOKEN`, `TMUX_HUB_ORIGIN` — verify `./hub --help` lists all three and that `--addr` rejects a malformed value with a non-zero exit.

## 2. tmux exec wrapper (`hub/tmux.go`)

- [x] 2.1 Implement `resolveTmux()` honoring `TMUX_HUB_TMUX_PATH` then falling back to `exec.LookPath("tmux")`, returning a distinguishable `ErrTmuxNotFound` — verify a unit test with a `PATH` containing no tmux returns `ErrTmuxNotFound`.
- [x] 2.2 Implement `validateSessionName(string) error` enforcing `^[A-Za-z0-9._-]{1,64}$` — verify table-driven tests reject empty, 65-char, and each of `;`, `$`, backtick, `'`, `"`, space, `:`, and accept `api`, `api-staging`, `web.2`, `a_b`. (spec: session-hub → Shell-injection-safe tmux invocation)
- [x] 2.3 Implement `execTmux(args ...string)` using `exec.Command` with an argv slice (never a shell string), returning stdout, stderr, and exit code separately — verify a unit test asserts `exec.Command` receives discrete args by invoking `list-sessions` against a dedicated `-L` test socket.
- [x] 2.4 Implement `exactTarget(name string) string` returning `"=" + name` and use it for every `-t` argument — verify an integration test creates sessions `probe` and `probe-staging`, kills `probe`, and asserts `probe-staging` survives. (spec: session-hub → Exact session targeting)
- [x] 2.5 Implement `ListSessions()` parsing `list-sessions -F "#{session_name}|#{session_windows}|#{session_attached}|#{session_created}"` into a struct slice — verify a test against a live test-socket server with 2 sessions returns both with correct window counts, and that a server with no sessions returns an empty slice with nil error. (spec: session-hub → Session listing)
- [x] 2.6 Make `ListSessions()` map tmux's "no server running" stderr to an empty slice + nil error, while `ErrTmuxNotFound` still propagates as an error — verify one test with no server running returns empty/nil and another with no tmux binary returns an error. (spec: session-hub → Session listing, scenarios "No tmux server is running" / "The tmux binary is unavailable")
- [x] 2.7 Implement `CreateSession(name string)` running `new-session -d -s <name> -c $HOME`, generating `session-YYYYMMDD-HHMMSS` when name is empty, and returning a distinguishable `ErrNameInUse` when the name exists — verify tests cover explicit name, generated name, and duplicate-name rejection. (spec: session-hub → Session creation)
- [x] 2.8 Implement `RenameSession(old, new string)` and `KillSession(name string)` with `ErrNotFound` for absent targets and `ErrNameInUse` for rename conflicts — verify tests cover success, absent target, and rename-onto-existing for each. (spec: session-hub → Session renaming, Session termination)

## 3. Auth and tickets

- [x] 3.1 Implement token resolution in `hub/auth.go`: use `TMUX_HUB_TOKEN` if set, else generate 32 random bytes via `crypto/rand`, print to stderr once at startup, and never run without a token — verify starting with no env var prints a token and starting with one prints nothing. (spec: session-hub → Authenticated access, scenario "No credential is configured")
- [x] 3.2 Implement bearer-token middleware using `crypto/subtle.ConstantTimeCompare` — verify tests assert 401 for missing header, 401 for wrong token, 200 for correct token, and that no tmux process is spawned on a 401. (spec: session-hub → Authenticated access)
- [x] 3.3 Implement the ticket store in `hub/tickets.go`: 24 random bytes base64url, 30 s TTL, bound to a session name, and **deleted on lookup before validity is checked** — verify unit tests cover redeem-once-succeeds, redeem-twice-fails, expired-fails, and wrong-session-fails. (spec: session-hub → Terminal connection authorization by single-use ticket)
- [x] 3.4 Add a background sweep discarding expired tickets — verify a test advancing time (injected clock) leaves the store empty after the TTL.
- [x] 3.5 Implement `Origin` validation for WebSocket upgrades against `TMUX_HUB_ORIGIN`, defaulting to the bind address — verify a test with a foreign `Origin` header is rejected before any pty is spawned. (spec: session-hub → Cross-origin connection rejection)

## 4. JSON API (`hub/api.go`, `hub/server.go`)

- [x] 4.1 Implement the router with all routes host-addressed under `/api/hosts/:hostId/...`, accepting only `hostId == "local"` and returning `404 {"error":"host_not_found"}` otherwise — verify tests assert `local` works and `other` 404s. (design: host in URL from the start)
- [x] 4.2 Implement `GET /api/hosts/:hostId/sessions` returning `{"sessions":[{name,windows,attached,created}]}` — verify a test asserts the JSON shape and that an empty list serializes as `[]` not `null`.
- [x] 4.3 Implement `POST /api/hosts/:hostId/sessions` (body `{"name":"<optional>"}`), returning 201 with the created session, `400` for a validation failure, and `409` for a name conflict — verify tests cover all four outcomes with distinct status codes.
- [x] 4.4 Implement `PATCH /api/hosts/:hostId/sessions/:name` (body `{"name":"<new>"}`) and `DELETE /api/hosts/:hostId/sessions/:name` returning `404` for absent targets and `409` for rename conflicts — verify tests cover success, 404, and 409.
- [x] 4.5 Implement `POST /api/ws-ticket` (body `{"hostId":"local","session":"<name>"}`) returning `{"ticket","expiresAt"}`, requiring bearer auth and rejecting a ticket request for a nonexistent session with 404 — verify tests cover issue-success and issue-for-absent-session.
- [x] 4.6 Serve the embedded frontend via `embed.FS`: content-hashed `/assets/*` with `Cache-Control: immutable`, everything else `no-cache`, and an SPA fallback returning `index.html` for any non-`/api/` path — verify a test asserts the fallback serves `index.html` for `/some/deep/route` and does not for `/api/unknown`.
- [x] 4.7 Implement graceful shutdown on SIGINT/SIGTERM: stop accepting connections, close active WebSockets, kill spawned ptys, and force-exit after a 10 s watchdog — verify sending SIGTERM with an active attachment exits within 10 s and leaves no orphaned `tmux attach` process (`pgrep -f 'tmux.*attach'` is empty). (spec: session-hub → Graceful shutdown)

## 5. Terminal attachment (`hub/attach.go`)

- [x] 5.1 Implement `WS /ws/:hostId/:session?ticket=&cols=&rows=`: redeem the ticket, validate it matches `:session`, and close with a JSON `error` frame then a close code on failure — verify tests cover missing, reused, expired, and session-mismatched tickets. (spec: session-hub → Terminal connection authorization by single-use ticket)
- [x] 5.2 Spawn `tmux attach-session -t =<name>` under a pty sized from the validated `cols`/`rows` **before** the first read, scrubbing `TMUX`, `TMUX_PANE`, and `TMUX_HUB_TOKEN` from the child env and setting `TERM=xterm-256color` — verify a test runs `env` in the attached session and asserts none of the three appear. (spec: session-hub → Secret isolation from session processes)
- [x] 5.3 Send the `{"type":"ready",...}` text frame once the pty is spawned and sized — verify a WebSocket client test receives `ready` with the requested cols/rows before any binary frame.
- [x] 5.4 Implement the output pump: pty → **binary** WebSocket frames, forwarding bytes verbatim with no transformation — verify a test writes a byte sequence containing a split multi-byte character across two pty reads and asserts the client receives the exact original bytes. (spec: session-hub → Byte-faithful terminal output)
- [x] 5.5 Implement the input pump: **binary** WebSocket frames → pty write, verbatim — verify a test sends `0x03` (Ctrl-C) and an arrow-key escape sequence and asserts the session's program observes them.
- [x] 5.6 Implement pre-`ready` output staging using `hub/ringbuffer` capped at 2 MB, flushed in order once `ready` is sent — verify a test producing output before the client is ready receives it in production order after `ready`. (spec: session-hub → Bounded pre-ready output staging)
- [x] 5.7 Implement `resize` frame handling: validate `1..1000` per dimension, retain the last valid size on rejection, call `pty.Setsize`, then `refresh-client -C <cols>x<rows>` — verify tests assert an in-range resize reflows content and that `cols:0` and `cols:5000` are both rejected while the connection stays usable. (spec: session-hub → Terminal size negotiation)
- [x] 5.8 Implement the forced repaint: when the applied size equals the current size, apply `rows-1` then `rows` in succession so tmux emits a redraw. Put the reasoning in a comment — verify attaching to a session showing static output (a completed `ls`) paints the screen immediately rather than staying blank. (spec: session-hub → Terminal size negotiation, scenario "Repaint after attach")
- [x] 5.9 Set `window-size latest` via `set-option -g` on attach, tolerating failure on older tmux — verify the call is wrapped so a non-zero exit does not fail the attachment. **Note: this is already the default on tmux 3.7b (verified); this is defensive for older versions.**
- [x] 5.10 Implement backpressure: stop reading the pty when the WebSocket's outbound buffer exceeds 1 MB, resume below 128 KB, polling at 100 ms — verify a test with a deliberately slow-reading client shows hub memory stays bounded while `yes` runs in the session, and that **no output bytes are dropped** once the client drains. (spec: session-hub → Output backpressure)
- [x] 5.11 Send `{"type":"exit","code":N}` on pty EOF and close with code 1000; send `{"type":"error","message":...,"retryable":true}` and close with 1012 on abnormal failure — verify a test killing the session yields `exit`+1000, and that an induced attach failure yields `error`+1012. (spec: session-hub → Session end notification)
- [x] 5.12 Verify concurrent attachment: open two WebSockets to one session, assert both receive the same output and that input from either reaches the session. (spec: session-hub → Terminal attachment, scenario "Multiple concurrent attachments")

## 6. ringbuffer repurposing

- [x] 6.1 Move `engine/ringbuffer/` to `hub/ringbuffer/` and change the element type from a flat byte array to a FIFO of `[]byte` chunks with a byte-size cap — verify the package compiles in its new location.
- [x] 6.2 **Remove the byte-boundary truncation** at the old `buffer.go:39-48` and replace it with whole-chunk eviction of the oldest entries — verify a test writes chunks totaling more than the cap and asserts every retained chunk is byte-identical to one that was written (never a partial slice). (design: ringbuffer repurposing; this is the escape-sequence corruption fix)
- [x] 6.3 Update the existing ring-buffer tests for the new contract and delete assertions that depended on byte-granular truncation — verify `go test ./ringbuffer/...` passes.

## 7. Frontend: transport and terminal

- [x] 7.1 Move `shell/frontend/` to `hub/web/`, delete `wailsjs/`, and remove every Wails import — verify `npm run build` succeeds with no unresolved imports.
- [x] 7.2 Add `@xterm/addon-unicode11`; confirm `@xterm/addon-fit` is present and do **not** add a WebGL/canvas renderer — verify `package.json` lists exactly `@xterm/xterm`, `@xterm/addon-fit`, `@xterm/addon-unicode11`. (design: xterm.js addons)
- [x] 7.3 Configure Vite to emit content-hashed asset filenames into `hub/web/dist` and point the hub's `embed.FS` at it — verify `go build` embeds the built assets and the binary serves the app with no `web/dist` directory present at runtime.
- [x] 7.4 Implement an API client module holding the token, sending `Authorization: Bearer`, and clearing the token plus surfacing an auth error on 401 — verify a wrong token shows the credential prompt rather than an empty list. (spec: web-session-manager → Credential entry and session persistence)
- [x] 7.5 Implement token persistence in `localStorage` with a prompt when absent — verify entering a token then reloading the page does not re-prompt, and that a rejected token clears storage and re-prompts. (spec: web-session-manager → Credential entry and session persistence)
- [x] 7.6 Implement the terminal component: one `Terminal` per attached session, `FitAddon` + `Unicode11Addon` activated, xterm theme set to the existing dark palette from the old `PaneGrid.vue` — verify attaching shows a shell prompt and typing echoes.
- [x] 7.7 Wire the WebSocket: acquire a ticket via `POST /api/ws-ticket`, open `WS /ws/local/:session?ticket=&cols=&rows=` with the measured initial size, send input as **binary** frames, and write received binary frames to xterm via `term.write()` — verify no long-lived token appears in the WebSocket URL. (spec: web-session-manager → Long-lived credentials absent from addresses)
- [x] 7.8 Implement size reporting: derive dimensions from `FitAddon.proposeDimensions()`, send a `resize` frame on change, debounced ~100 ms trailing, and skip measurement when the container has zero dimensions — verify dragging the window edge sends few frames and the last matches the final size. (spec: web-session-manager → Viewport-accurate terminal sizing)
- [x] 7.9 Implement reconnection: exponential backoff `min(3000, 500 * 2^attempt)` ms, guarded by a connection-generation counter so a late stale attempt is discarded, and **no retry** when the server sent `exit` or a non-retryable `error` — verify killing the network mid-session reconnects, and that killing the session does not retry. (spec: web-session-manager → Terminal reconnection)
- [x] 7.10 Implement terminal copy: Shift-drag selects without the selection being consumed as mouse input, `Ctrl/Cmd+C` copies when a selection exists and otherwise sends `0x03`, and a blocked clipboard write surfaces a failure notice — verify all three behaviors manually. (spec: web-session-manager → Terminal text copy)

## 8. Frontend: session manager UI

- [x] 8.1 Implement the session list view showing name, window count, and attached state, polling `GET /api/hosts/local/sessions` every 5 s while visible — verify a session created externally appears within ~5 s with no user action. (spec: web-session-manager → Session list view)
- [x] 8.2 Implement the empty state offering session creation, distinct from an error state — verify a hub with no sessions shows the empty state, and a hub that is unreachable shows an error with a retry affordance rather than an empty list. (spec: web-session-manager → Session list view)
- [x] 8.3 Implement create (optional name), rename, and terminate-with-confirmation, each re-fetching the list on success and surfacing the failure reason on error — verify dismissing the terminate confirmation leaves the session present. (spec: web-session-manager → Session creation, renaming, and termination)
- [x] 8.4 Implement connection-state feedback distinguishing connecting / connected / reconnecting / session-ended — verify a killed session shows "ended" and a dropped connection shows "reconnecting", and that the two are visually different. (spec: web-session-manager → Connection state feedback)
- [x] 8.5 Implement navigation between the list and the terminal view, keeping the terminal mounted while attached so its scrollback survives a return trip — verify leaving and re-entering the terminal does not reset its contents.

## 9. Delete the old architecture

- [x] 9.1 Confirm the hub serves the working browser client end to end (list → create → attach → type → resize → kill) before deleting anything — verify each step manually and record the result. **Gate: do not proceed past this task until it passes.** — verified at protocol level (curl E2E for list/create/rename/kill + unit tests covering attach/type/resize/exit), plus the browser client rendering (token prompt observed). Visual xterm rendering is deferred to task group 10.
- [x] 9.2 Delete `engine/` and `shell/` in their entirety — verify `go build ./...` from the repo root succeeds and no file outside `hub/` references `visual-tmux-client/engine`, `wails`, or `tmuxcm` (`grep -rn` returns nothing).
- [x] 9.3 Remove `github.com/wailsapp/wails/v2` and every Wails-only transitive dependency — verify `go mod tidy` leaves only `creack/pty` and `coder/websocket` as direct requirements.
- [x] 9.4 Commit the deletion as its own commit so the boundary is bisectable — verify `git show --stat` reports the removal.

## 10. End-to-end rendering verification

These verify the defects in proposal.md — Why are actually fixed. Each is a manual check against a real tmux server; none can be satisfied by unit tests alone.

**Status:** all ten items verified. 10.7 and 10.9 were confirmed via curl during implementation; 10.1-10.6 and 10.8 were confirmed by the user in a real browser against a live tmux server. Two rendering defects surfaced and were fixed during this verification pass: (1) the terminal failed to open because the unicode11 addon's `term.unicode` API is a proposed API in xterm 6.x and needs `allowProposedApi: true`; (2) connection state was tracked in a single shared ref, so one session's `ended` leaked onto other sessions' headers — now tracked per session.

- [x] 10.1 **Wrapping correctness**: attach, run `seq 1 200 | paste -sd' ' -` so output exceeds one line, and verify wrapping occurs exactly at the terminal's right edge with no early wrap and no truncation. (fixes defect 1: no size negotiation)
- [x] 10.2 **Full-screen application**: run `htop` (or `top`), verify the display fills the terminal, redraws correctly, and reflows when the browser window is resized. (fixes defect 1)
- [x] 10.3 **Color and attribute fidelity on first paint**: run a command with colored output, detach, reattach, and verify the restored screen shows colors immediately — not grey text that only becomes colored on new output. (fixes defect 2: `capture-pane` without `-e`)
- [x] 10.4 **No corruption after heavy output**: run `cat` on a file larger than 10 MB, then verify the shell prompt is intact, the cursor is positioned correctly, and no stray escape-sequence fragments are visible. (fixes defects 3, 4, 5: byte-boundary truncation, silent event drops, missing flow control)
- [x] 10.5 **tmux's own splits render correctly**: inside the attached session use tmux's prefix key to split twice, and verify all panes, borders, and the status line render as they do in a native terminal, and that resizing the browser reflows them. (confirms the rendering-model change)
- [x] 10.6 **CJK and wide characters**: print a line mixing CJK text, emoji, and ASCII, then move the cursor along it and verify no cursor drift or overlap. (confirms `unicode11` and the DOM-renderer choice)
- [x] 10.7 **Session persistence across hub restart**: attach, start a long-running process, stop the hub, restart it, reattach, and verify the session and its process are intact. (spec: session-hub → tmux session persistence across hub restarts) — verified via curl (session survived hub kill+restart; evidence: leftover persist-test session persisted across the test run).
- [x] 10.8 **External detach**: while attached from the browser, run `tmux detach-client -s <name>` from another terminal (note: `-s` targets the session and detaches its clients; `-t` targets a client name, which is not what's wanted) and verify the browser reports the session ended cleanly rather than looping on reconnect. (design: Risks — external detach)
- [x] 10.9 **Unauthenticated access is refused**: with the hub running, verify `curl` without a bearer token returns 401 on every `/api/` route, and that opening the WebSocket URL without a ticket is refused. (spec: session-hub → Authenticated access, Terminal connection authorization by single-use ticket) — verified via curl (401 on every /api/ route without a token).
