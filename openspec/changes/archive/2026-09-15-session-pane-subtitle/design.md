## Context

See `proposal.md` — Why. Constraints that shape this approach:

- The hub already polls with one `list-sessions` invocation per `web.session_poll_interval` (default 2s). `hub/internal/tmux/parse.go` splits session records from the **right** because session names may themselves contain the separator — an existing acknowledgement that tmux fields are free-form.
- `session.Session` (`hub/internal/session/model.go:4`) is `{Name, Windows, Attached, Created}`; there is no pane data anywhere in the hub.
- `session.Backend` is deliberately minimal (`List`/`Create`/`Rename`/`Kill`). `session/service.go:50-56` establishes an **optional-capability idiom**: unexported interfaces (`fastGetter`, `fastChecker`) are type-asserted against the backend so an implementation can add capability without widening the contract.
- `SessionList.vue` already renders a meta line under the name (`row-meta`, "N windows · attached/detached"), so the row is already two-line.
- tmux has **no session-level equivalent** of `pane_current_command`; the data is inherently pane-level.

## Goals / Non-Goals

**Goals:**
- A user can tell what each session is running without attaching to it.
- The subtitle degrades to nothing, never to a wrong value — a mis-parsed title is worse than a missing one.
- No regression to the session list when pane data is unavailable.

**Non-Goals:**
- Changing the terminal header, which keeps showing the session name and connection state. (An earlier framing described this as "moving" a process name from the header to the list; the header has no process name and the name it does show is load-bearing when the sidebar is collapsed.)
- Per-pane subtitles or a pane list in the sidebar. The reference does this in its pane toolbar; not here.
- Pushing subtitle updates over the WebSocket instead of the existing poll. Not worth a protocol change for a 2-second-stale string.
- Detecting *which agent* is running (the reference has a whole `agent-nexus` layer that identifies `claude`/`codex`/`cursor` from the command and title). Out of scope.

## Decisions

### 1. A second tmux query, not an extension of the first

`list-sessions` cannot supply pane fields — tmux has no session-scoped current command — so the hub adds one `list-panes -a` invocation per poll. `-a` covers every session in a **single** exec rather than one query per session, so the cost is one extra process per poll regardless of session count.

*Alternatives considered:* one `list-panes -t =<name>` per session — N processes per poll, and it scales with exactly the dimension (many sessions) that makes the subtitle most valuable. Reading `/proc` or `ps` instead of asking tmux — rejected: it would reimplement tmux's own notion of the active pane and would not see the pane title at all.

### 2. `\x1f` as the field separator, with a strict field-count check

The pane format carries three free-form fields (window name, pane title, current command), so the existing right-split trick does not generalise to them. The hub uses ASCII Unit Separator (`\x1f`) as the field separator and then **verifies the field count**; a record that does not split into exactly the expected number of fields is **dropped**, not guessed at.

*Alternatives considered:* `strings.SplitN` so the last field absorbs extra separators — only protects one of the three free-form fields. A printable separator like `|` — session names, window names, and OSC titles routinely contain it. Dropping malformed records is the key choice: it converts an unparseable pane into a missing subtitle, and the spec explicitly permits a missing subtitle.

### 3. Best-pane selection mirrors the reference

Skip panes that have exited; then prefer, in order: active pane of the active window, any active pane, any pane of the active window, any remaining live pane. This is the reference's `panePriority` ordering, and it answers the question the subtitle is asked most often — "what is this session showing me right now?"

*Alternatives considered:* the first pane in index order — arbitrary, and picks a background pane in a split session. Showing a count of panes instead — answers a different question, and the window count already covers it.

### 4. Optional capability on the backend, not a widened interface

Pane summaries are obtained through an optional interface that `session.Service` type-asserts against its backend, following `service.go:50-56`. A backend that does not implement it yields sessions with absent summaries and no error. This keeps `Backend` at four methods and keeps existing test fakes valid.

*Alternatives considered:* adding `ListPanes` to `Backend` — would force every implementation and every existing fake to change for a feature that is allowed to be absent.

### 5. Subtitle composition is a pure function on the client

The precedence — `windowName[*]: title` → `windowName` → `command` → nothing — lives in a DOM-free module with unit tests. The test environment has no DOM, so anything worth testing has to be pure; this is the same split the file-manager change uses for its preview dispatch.

*Alternatives considered:* composing the string on the hub. Rejected: it is presentation, it would need the asterisk/formatting rules on the server, and the client already receives all three fields and can render them differently later without a server change.

### 6. The subtitle is an additional line, not a replacement

`row-meta` ("N windows · attached/detached") stays as it is; the subtitle is a new line above it. The subtitle carries no state and no punctuation of its own beyond the active-window asterisk, so it reads as annotation rather than as another control.

*Alternatives considered:* folding the command into the existing meta line — that line already packs two facts, and appending a free-form command of arbitrary length risks wrapping the row and crowding the rename control.

## Risks / Trade-offs

- **[Risk] A pane whose title contains `\x1f` produces a malformed record.** → *Mitigation:* the field-count check drops it, yielding no subtitle. The failure mode is a missing subtitle, never a wrong string.
- **[Risk] The extra tmux invocation per poll is visible on a machine with a very large pane count.** → *Mitigation:* it is one process, not one per session. If profiling shows cost, the query can be reduced to sessions that are actually listed (currently every session) or moved behind the existing optional-capability seam without touching the specs.
- **[Risk] A subtitle that changes every poll causes visual churn** for sessions running a command that rewrites its title constantly (some prompts do). → *Mitigation:* the subtitle is text-only with no transition or animation, so changes are as calm as the poll interval; shortening the poll is deliberately not part of this change.
- **[Trade-off] Subtitle content is only as fresh as the poll.** → *Accepted;* it is ambient context, not a live indicator, and the activity dot already covers "is this session producing output".
- **[Trade-off] Adding a line to each row makes the sidebar list taller,** so fewer sessions fit without scrolling. → *Accepted;* it is one small-text line, and the sidebar is collapsible.

## Migration Plan

Additive and compatible. The new session field is optional on the wire, so an older client ignores it and a newer client tolerates its absence. No persisted state, no protocol change, and no change to session lifecycle semantics. Rolling back is reverting the release.

## Open Questions

- Whether the subtitle should be truncated with an ellipsis or allowed to wrap when a pane title is very long. Leaning toward a single-line ellipsis so row height stays uniform; it does not affect the specs or the task breakdown, only the CSS.
