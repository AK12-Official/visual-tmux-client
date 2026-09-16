## 1. tmux pane listing

- [x] 1.1 Add a pane list format and parser to `hub/internal/tmux` (`panes.go` or alongside `parse.go`): the `-F` string for `list-panes -a` carrying session name, window index, pane index, window-active, pane-active, pane-dead, window name, pane title, and current command, separated by `\x1f`. Verify: unit test parses a fixture including a CJK title, a title containing spaces, and a title containing `|`.
- [x] 1.2 Make the parser **drop** any record that does not split into exactly the expected field count. Verify: unit test with a record containing an embedded `\x1f` asserts the record is dropped rather than mis-attributed, and that the remaining records still parse.
- [x] 1.3 Add a `ListPanes(ctx)` method to the tmux client using the existing `Exec` idiom. Verify: `TestListPanesReportsEveryPane` drives a real tmux server, asserts one record per pane across a split window and that the panes reduce to a summary; `TestListPanesReportsWhenTmuxIsUnavailable` asserts a resolution failure is surfaced as an error rather than an empty list. (The package has no fake-exec seam — `Exec` is not injectable and every existing test drives real tmux — so the argument vector is verified end-to-end through tmux's own parsing rather than by asserting argv.)

## 2. Pane summary in the session service

- [x] 2.1 Implement representative-pane selection: skip exited panes, then prefer active-pane-of-active-window, then active pane, then pane of active window, then any live pane; group panes by session name. Verify: table-driven unit test covering each priority level and the all-exited case.
- [x] 2.2 Expose it through an **optional** capability interface that `session.Service` type-asserts against its backend, following the existing `fastGetter`/`fastChecker` idiom in `service.go`. Verify: a test backend that does not implement it yields sessions with absent summaries and no error; the existing fakes still compile unchanged.
- [x] 2.3 Confirm the existing `Backend` interface still has exactly its four methods and that no existing test fake required modification. Verify: `go build ./...` and `go test ./internal/session/` pass.

## 3. Listing integration and degradation

- [x] 3.1 Extend the session `Service.List` path to merge pane summaries into the returned sessions, leaving the summary absent where no representative pane exists. Verify: unit test asserts a session with no live pane is still returned with an absent summary.
- [x] 3.2 Ensure a failing pane query does **not** fail the session listing: the list is returned with summaries absent. Verify: unit test with a pane lister returning an error asserts a successful listing with absent summaries.
- [x] 3.3 Add the optional summary field to the session list DTO in `hub/internal/transport/http/dto.go` following existing conventions, keeping all existing fields and shapes unchanged. Verify: an httptest asserts a session without a summary serialises without the field and one with a summary serialises all three components; the existing session-list tests still pass unmodified.

## 4. Frontend subtitle

- [x] 4.1 Add the summary field to the session type in `hub/web/src/api.ts` as optional. Verify: `npm test` passes and an existing api test still passes unmodified.
- [x] 4.2 Add a DOM-free `formatPaneSubtitle` module implementing the precedence `windowName[*]: title` → `windowName` → `command` → nothing, marking the window name when the window is active. Verify: `npm test` covers all four branches, the active-window asterisk, an all-empty summary, and absent summary.
- [x] 4.3 Render the subtitle in `SessionList.vue` under the session name, omitting the element entirely when there is no subtitle. The pre-existing `row-meta` line (window count and attached state) was removed during review. Verify: manual check that a row with no summary collapses to the session name alone with no empty line, that no row shows a window count or attached state, and that a row's name and action buttons are unchanged.

## 5. Styling and accessibility

- [x] 5.1 Style the subtitle as small, muted text that ellipsises to a single line so row height stays uniform. Verify: manual check with a very long pane title that the row height does not change and the title does not overlap the action buttons.
- [x] 5.2 Confirm the row's accessible semantics match what the row actually shows. Verify: the row's `aria-label` is the session name plus its subtitle when one is rendered (`rowLabel` in `sessionSubtitle.ts`, with its own tests), and the window count and attached state are announced nowhere, because a later revision removed them from the row. A screen-reader user is told what sighted users see, and nothing they cannot. (An intermediate revision kept the label byte-identical to before the change and left the subtitle unannounced; the follow-up commit below reversed that once `row-meta` was removed.)

## 6. Verification

- [x] 6.1 Run `make lint` and confirm zero issues on the new Go files, including `lll`, `funlen`, `gocyclo`, and `mnd`. Verify: command exits 0.
- [x] 6.2 Run `make test` and confirm all Go and frontend tests pass. Verify: command exits 0.
- [x] 6.3 Confirm tmux test isolation is intact: any test touching tmux goes through `testTmuxEnv` + `t.TempDir()`, and `TestTerminalTestsPreserveParentTmux` passes. Verify: `go test ./...` exits 0 with the parent tmux server unaffected.
- [x] 6.4 Verify end to end in a browser against real sessions: a session running a plain shell, one running a full-screen program that sets its pane title, one with a split where a non-first pane is active, and one whose panes have all exited. Verify: each shows the expected subtitle per the precedence, the exited one shows no subtitle, and the session list still renders when the pane query is unavailable.

## Status notes (2026-09-15)

Marked complete and verified:

- 1.1–3.3 — verified by `go test ./internal/session/ ./internal/tmux/ ./internal/transport/http/`, plus a live run of the built hub against an isolated tmux server.
- 4.1–4.2 — verified by `npm test` (37 passing, 10 of them new).
- 6.1 — `golangci-lint` 2.13.2 (the pinned version): 0 issues.
- 6.2, 6.3 — `make test` now exits 0 in full, including the previously-failing tmux suites. This required fixing the blocker described below.

Confirmed by the operator in a browser (2026-09-15), using the environment and steps in `review.md`:

- 4.3 — a row with no subtitle collapses to the session name alone, and no row shows a window count or attached state.
- 5.1 — a long pane title ellipsises to one line; row height stays uniform and the text does not reach the action buttons.
- 6.4 — `split-demo` reports the **active** pane (`tail*: …`), not the inactive `sleep` pane, and `ended` shows no subtitle at all.

All 18 tasks are complete.

### Changes from the first review round

Review of the built UI produced four follow-ups, all applied:

- The ended-session status was a centred card; it is now anchored top-left like terminal output, in `TerminalView.vue` (not part of this change's tasks — recorded here because it was raised during review).
- The row's `row-meta` line (`1 window · attached`) was removed, and the "Session list view" delta updated: a row now carries only its name and subtitle.
- With `row-meta` gone, the subtitle *is* folded into the row's `aria-label` (`rowLabel`), so the label says what the row says and no longer announces a window count that is displayed nowhere. This reverses the intermediate decision recorded in 5.2.
- The record above and the spec deltas in this change were brought back in line with the shipped code, which a later review found they had drifted from (the 200-rune field cap and the `aria-label` both changed after they were written).

### Decided not to change: the tmux dead-pane message

Review asked why a session whose pane has exited shows its status **centred**. Investigated: that text is drawn by **tmux**, not by this client — it is `remain-on-exit-format`, whose default is `Pane is dead (#{pane_dead_status}, #{t:pane_dead_time})`. It sits at the exact vertical centre (row 30 of a 57-row pane), and the option controls the text only, never the placement.

Deliberately left alone. tmux centres it so it does not read as the program's own last output, and this client's whole premise is tmux-in-a-browser, so a tmux user seeing tmux's own convention is correct rather than a defect. Moving it would mean a hub change, a frontend change, a new overlay competing with the existing "connection ended" overlay, and a second source of truth for "this is over" — a poor trade for one line of text in a narrow case. If the product ever targets people who do not know tmux, the right fix is plain-language wording, not repositioning.

### Blocker fixed: tmux sockets exceeded the platform path limit

Every tmux-backed test failed on macOS with `File name too long`: `t.TempDir()` nests under `/var/folders/...`, so `TMUX_TMPDIR/tmux-<uid>/<socket>` overflowed the ~104-byte `sun_path` limit and tmux could not bind at all. Predated this change; passed on Linux CI.

Fixed by adding `hub/internal/testutil.SocketDir(t)`, which returns a short directory under `/tmp`, and using it everywhere a socket directory is needed: `newTestClient` in `hub/internal/tmux`, `testTmuxEnv` callers in `hub/cmd/visual-tmux-client` (`tmux_isolation_test.go`, `smoke_test.go`, `e2e_test.go`). Non-socket temp dirs (process working directory, config files) still use `t.TempDir()`. Socket names were shortened too, since the limit counts the whole path. Documented in `CONTRIBUTING.md`.
