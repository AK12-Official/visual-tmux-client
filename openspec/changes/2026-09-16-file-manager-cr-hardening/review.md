# Review

Independent review of this change. The reviewers did not write the code; each was given a disjoint
file set so they could not duplicate each other, and each was asked for a concrete failure
scenario per finding rather than a style opinion.

## Round 1

Four subagents, run concurrently:

| reviewer | scope | outcome |
| --- | --- | --- |
| Go service | `hub/internal/files/{service,model,guard,errors}.go` | 4 findings, 3 CONFIRMED (G1–G4 below) |
| Go transport and tmux | `hub/internal/transport/http/{files,dto}.go`, `hub/internal/tmux/panes.go` | 2 findings, 1 CONFIRMED |
| Frontend | `hub/web/src/**` | 6 findings, 6 CONFIRMED |
| Claims and tests | spec, change docs, READMEs, every test in the diff | 11 findings, 10 CONFIRMED |

Every finding was reproduced before it was acted on. Three are worth recording in detail, because
two of them are defects *introduced* by this change.

### The shrink-direction framing error — a regression this change introduced

`hub/internal/transport/http/files.go`

The fix for the review's finding #5 replaced `ServeContent`'s own measurement with a `SectionReader`
bounded by the authorized length. That closed the growth direction and opened the opposite one: a
file truncated after the check was served with `Content-Length` promising the authorized length and
a body carrying fewer bytes, because `ServeContent` takes the header from the section and copies
with an error it discards. The browser rejects such a response as a transfer failure. The old code
was self-consistent here (`Content-Length: 0`) and wrong only about `X-File-Size`.

Fixed by clamping the section to what the descriptor holds and reporting that length. Covered by
`TestReadRouteServesAShortenedFileAtItsNewLength`, which was confirmed to fail with the clamp
reverted.

### The name taken by a dangling link — an incomplete fix

`hub/internal/files/service.go`

The new free-name branch of `confirmUnchanged` asked with `os.Stat`, which follows symlinks: a name
taken by a link whose target is gone therefore read as free, and the write replaced the link — the
exact shape `Write`'s own pre-check refuses when a write *begins* against one. Fixed by asking with
`Lstat`. Covered by `TestAWriteRefusesANameTakenByADanglingLink`, confirmed to fail with `Stat`
restored.

### A listing that called itself truncated while showing everything

`hub/internal/files/service.go`

The staging-file filter ran after `readListing`, so a directory holding exactly `maxDirEntries`
entries plus one in-flight staging file reported `Truncated: true` while returning all of its
entries. Fixed by applying the skip inside the read, so the bound counts what the caller is shown.
Covered by `TestAWriteInFlightDoesNotMakeAListingLookTruncated`.

### Findings that dissolved on inspection

These were reported or suspected and did not survive being traced. They are listed because a review
that only reports hits is not telling you what it checked.

- `ExpectedMtime.Millis` being left zero when only the exact time is supplied: `matches` branches on
  `Nanos != nil` first, so the zero is never read.
- `textScan`'s carry logic: property-tested over 480,160 chunk/content combinations against the
  ground truth (`has NUL || !utf8.Valid`) at chunk sizes from 1 to 32768, with no mismatch in either
  direction.
- `readListing`'s arithmetic at limits 1–4, and `limit <= 0` being unreachable (config validation).
- Tab ids being reused, so `saveTickets` could be misattributed.
- `entry.mtime` from a listing reaching a save: only `info`/`image` tabs take it, and those are
  never dirty, so Save is disabled and no shortcut reaches it.
- The wire names agreeing across Go and TypeScript, the delta spec being byte-identical to the living
  spec, and the two READMEs being paragraph-for-paragraph equivalent.
- `{ immediate: true }` on `CodeEditor`'s activate watcher: reviewers judged it dead code rather than
  a bug, and it was removed because its comment described a case it could not cover.

## Round 2

Three subagents: closure verification of every round-1 finding, and an adversarial re-review of the
round-1 fixes split across Go and frontend. The fixes are where a review is most likely to find the
next defect, which round 1 had already demonstrated.

**Closure verification: 20 of 22 findings closed**, and each closure verified by reverting the fix in
a throwaway copy and confirming the named test fails:

| finding | verdict | settling evidence |
| --- | --- | --- |
| G1 dangling link read as a free name | CLOSED | revert `Lstat`→`Stat`: `TestAWriteRefusesANameTakenByADanglingLink` fails |
| G2 staging file counted against the bound | CLOSED | revert the skip: `TestAWriteInFlightDoesNotMakeAListingLookTruncated` fails |
| G3 download pays the classification scan | CLOSED | accepted and recorded in `design.md` |
| G4 cancelled scan reports the catch-all code | CLOSED | accepted and recorded in `design.md` (this round) |
| T1 shrink-direction framing error | CLOSED | revert the clamp: `TestReadRouteServesAShortenedFileAtItsNewLength` fails |
| T2 exact-time zero indistinguishable from unset | CLOSED | contract documented on `ReadResult.MtimeNanos` |
| F1 the `moved` notice asserted something false | CLOSED | the text now claims only what the client did |
| F2 the overlay integration is untested | PARTIAL | see below |
| F3 dead `immediate: true` | CLOSED | flag and its stale comment gone |
| F4 `tabs.ts` importing the renderer | CLOSED | it imports `./api` only |
| F5 unqualified history guarantee | CLOSED | spec sentence qualified |
| F6 unvalidated exact time | CLOSED | `exactNanos`, and this round bounded it to int64 |
| D1 `roots` statement self-contradicting | CLOSED | spec rewritten |
| D2/D3 stale comments | CLOSED | both rewritten |
| D5 "exactly the bytes" overstating | CLOSED | spec states both directions |
| D6/D7 missing tests for the clamp and the bound | CLOSED | both exist and both were confirmed to fail without the fix |
| D8 dangling link's listing metadata | CLOSED | spec sentence and scenario |
| D9 three manual verifications marked done | CLOSED | unchecked, with the reason stated |
| D10 two listing tests passing vacuously | CLOSED | revert `List` to return nothing: both now fail |
| D11 `review.md` missing | CLOSED | this file |

**F2 remains PARTIAL, and cannot be closed here.** `beginSave` now captures the session, path, text,
and expectation in one object, so the wrong value can no longer be passed by accident — but the
closure reviewer showed that deliberately re-reading the path at answer time still leaves every test
green, because nothing exercises the component. Closing it properly needs a DOM test harness, which
this change does not add (see `design.md` decision 5). `tasks.md` 3.2 records the gap rather than
discharging it.

**Round 2 found one new defect in the round-1 fixes**, which is the pattern worth noting: the
round-1 validation of the exact modification time checked the value's *shape* and not what the hub
can parse, so a nineteen-digit string that overflows a signed 64-bit integer was adopted, sent back,
and refused as `invalid_body` — on every save thereafter, with reopening the file as the only way
out. That is exactly what the check's own comment claimed it prevented. Fixed by bounding to the
int64 range. The same bound was applied to the millisecond value, which has the identical exposure
through the pre-existing path: its decimal form goes on the wire and is read back with a 64-bit
integer parse.

**No defect was found in the Go fixes.** Every attack in that reviewer's directive was either
refuted with evidence — including a proof that the skip loop terminates when every entry is
skipped, and an `httptest` sweep of the clamp across range requests, `If-Range`, shrink-to-zero and
416 — or reduced to a residual that is inherent and already recorded: a file that shrinks *during*
the copy still under-delivers, which is what putting a length in a header and then streaming a file
cannot avoid, and which the client detects as a failed transfer rather than as wrong data.

### Accepted, with reasons

- A file that shrinks during the copy under-delivers (inherent; client-detectable; `design.md` 3a).
- The `download` route pays the whole-file classification scan and discards the answer
  (`design.md` decision 3).
- A cancelled or unclassified read reports the catch-all `write_failed` code, which is named for the
  write path (`design.md`, risks).
- `ReadResult.MtimeNanos` has no "unknown": a zero is the epoch. Stated as a contract on the field
  rather than left as a trap.
