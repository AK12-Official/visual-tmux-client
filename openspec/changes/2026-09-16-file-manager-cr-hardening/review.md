# Review

Independent review of this change. The reviewers did not write the code; each was given a disjoint
file set so they could not duplicate each other, and each was asked for a concrete failure
scenario per finding rather than a style opinion.

## Round 1

Four subagents, run concurrently:

| reviewer | scope | outcome |
| --- | --- | --- |
| Go service | `hub/internal/files/{service,model,guard,errors}.go` | 4 findings, 4 CONFIRMED (G1–G4) |
| Go transport and tmux | `hub/internal/transport/http/{files,dto}.go`, `hub/internal/tmux/panes.go` | 2 findings, 1 CONFIRMED |
| Frontend | `hub/web/src/**` | 6 findings, 6 CONFIRMED |
| Claims and tests | spec, change docs, READMEs, every test in the diff | 11 findings, 9 CONFIRMED, 1 PLAUSIBLE, 1 informational |

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

**Closure verification: 21 of 22 findings closed.** Where a finding had a test to revert, the
closure was verified by reverting the fix in a throwaway copy and watching that test fail (G1, G2,
T1, D6, D7, D10). The rest are comment, documentation, structure, or
acceptance changes, none of which has a test that could have been reverted to prove it; their
evidence is the line named in the table:

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

## Round 3

Two subagents, over the round-2 diff only: one adversarial re-review of the frontend change, one
checking whether the round-2 documents say what the code does.

**The frontend fix is sound.** A differential probe ran the client's real guard against
`strconv.ParseInt(value, 10, 64)` over 27 candidates — signs, leading zeros, both int64 bounds and
each side of them, hex, exponents, underscores, whitespace, decimals, a 1,000-digit value, a
100,000-digit value and a 1,000,001-character one. **No input is accepted by the client and rejected
by the hub**, which is the defect round 2 fixed, and `BigInt` cannot throw on anything the regex
passes. The only disagreement runs the safe way: `ParseInt` accepts a leading `+` and the regex does
not, so the client falls back to the millisecond comparison.

**Round 3 found one new defect, and it was in a comment the round-2 fix added.** The bound on the
millisecond value was justified by "the hub cannot read a value past what a double holds exactly".
It can — the hub reads those fine. What actually happens is worse and different in kind: the client
cannot *carry* the value unchanged, so it echoes a **different** time, and every save is refused as
a **conflict**. Accepting the overwrite the prompt then offers produces the same refusal, so there
is no way out through the interface. The check is right; the reason given for it named a hazard that
does not exist and a discriminator that does not do the work. Rewritten to state the real one.

**Four bookkeeping errors in this file**, all raised by the claims reviewer and all corrected: the
closure count said 20 where the table's own rows say 21; the round-1 Go row undercounted its
confirmed findings as 3 where the round-1 report marked 4; the round-1 claims row counted 10
confirmed where the report marked 9 plus one PLAUSIBLE and one informational; and the closing
sentence claimed every closure was verified by reverting a test, which is true of the six findings
that have a test and meaningless for the fifteen that are documentation or acceptance changes. Two
of those four were introduced by *correcting* the same numbers in the previous commit, in the wrong
direction — a reminder that the record of a review is a claim like any other.

**One spec sentence overstated**, found by the same reviewer: "Reading SHALL be bounded by the same
configured maximum" did not survive round 1's fix to the staging/bound interaction, because the
bound now counts kept entries while the hub also reads the staging files of writes in flight. The
design document already stated the true bound; the spec now does too.

### Trajectory

| round | over | findings that changed code or a claim |
| --- | --- | --- |
| 1 | the whole CR fix set | 23 raised across four reviewers, 22 distinct (D4 duplicates F3) — two of them defects introduced by the fix set itself |
| 2 | the round-1 fixes | 2 — one real (a value bound that checked shape rather than parseability), one partial |
| 3 | the round-2 fixes | 1 — a comment justifying the right check with the wrong reason — plus 4 bookkeeping errors and 1 spec overstatement |

Each round has been over a smaller surface than the last, and the last two found no defect in the
code at all — only false statements about it.

## Round 4

One subagent, over the round-3 diff, deciding convergence. It found one more — the same kind as
round 3's: a correct check explained by a false reason.

**The round-3 rewrite ranked the two failure modes backwards.** It claimed the dead end was a plain
integer past 2^53 that the client rounds. Traced end to end, that one is recoverable: the hub parses
it, the comparison fails, the refusal is `conflict`, the browser offers the overwrite, and
`save(true)` sends `expected: null` — which the hub skips the comparison for entirely, so the write
lands. The overwrite *is* the way out. (And with an exact nanosecond time present it is not even a
conflict, because the comparison prefers that value and the rounded millisecond never reaches it.)
The genuine dead end is the case the rewrite called lesser: a fraction or an exponent is a request
the hub cannot read at all, so it answers `invalid_body` rather than a conflict — the browser offers
no overwrite, and every retry sends the same unreadable value.

**Fixed by shortening, not by rewriting.** Two consecutive rounds found the *justification* of this
guard wrong while agreeing the guard is right, so the comment now states only what was traced — the
decimal form the hub parses, and the exact round trip a double cannot always provide — and no longer
ranks the harms or claims what the user can recover. That analysis lives here instead, where an
error in it is a note rather than a false statement in the code.

**Two smaller corrections.** `tasks.md` 9.6 described one bound and covered two. The Trajectory
table's round-1 figure (21) could not be derived from the tables above it, which sum to 23 raised
and 22 distinct; it now says so.

### Where the loop stands

Rounds 3 and 4 changed no code. Both found false statements *about* code that three independent
reviewers — including an exhaustive differential probe and a full revert-verified closure pass —
agree is correct. Each round of prose written to explain the loop has been able to contain its own
new error, so the honest stopping point is the code being verified sound and the explanations being
reduced to what can be checked, rather than continuing to add commentary that the next round can
falsify.
