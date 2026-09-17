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
| F4 `tabs.ts` importing the renderer | CLOSED | it imports `./api` only *(corrected in the seventh review: it also imported the `Classification` type from `./preview`, which erases at compile time but names the sanitizer's module; the type now lives in `files/classification.ts`)* |
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

## Round 5

One subagent, asked only whether anything in the round-4 diff is false. **Verdict: nothing clearly
false remains, and another round over this material is not worth running.**

Two wordings were flagged as ambiguous rather than wrong, and both were tightened anyway because the
fix is one clause. `A value that fails either bound is reported as no time at all` is true of the
write path and not of the read path, which refuses the file outright; it now says the value is
refused rather than adopted, which holds on both. And a `tasks.md` clause claiming only the first
bound is about unreadability was overstated, since `1.5` fails the safe-integer bound and is a string
`ParseInt` rejects.

Every other statement in the diff was verified clause by clause, including all five links of the
round-4 causal chain (`ErrConflict` → only `conflict` prompts → `save(true)` sends `expected: null`
→ the hub skips the comparison → the overwrite lands) and the corrected Trajectory arithmetic
(23 raised, 22 distinct, 21 closed).

### The loop, concluded

Five rounds: 23 findings, then 2, then 1, then 1, then 0. The last three changed no behaviour — they
were false or overreaching statements about behaviour that had already been verified, twice by
reverting the fix and watching the test fail, once by a differential probe of the guard against the
Go parser. The code stopped moving at round 2; the explanations took three more rounds to stop
moving, which is the shape to expect from a loop that treats every claim — including its own
record — as something to be checked.

## The second review of the change

A second review of the branch raised seven findings. Two were the P1s this change had *dispositioned*
rather than fixed -- the boundary TOCTOU and the write's install window -- and both were fixed this
time.

| finding | outcome |
| --- | --- |
| The boundary can still be redirected by a local process (P1) | **Fixed.** Operations run through an `os.Root` handle; see `design.md` decision 1. Only the rooted case changes, so the default configuration is byte-identical. |
| A save and a rename can still cross (P1) | **Fixed on the browser side.** The server window between the write's check and its replacement cannot be closed with a POSIX rename, so the browser refuses to record an answer that crossed a rename of the same path; see decisions 1a and 2. |
| The image preview bound uses a stale listing size (P2) | **Fixed.** The bound is applied to the length the hub reports when the file is read, and a file over it is presented as information with the reason given. |
| Files named binary are never classified by the hub (P2) | **Fixed.** A bodyless `HEAD` on the read route asks the hub what the file is, and its answer decides. |
| The classification and the body are not one snapshot (P2) | **Recorded**, with the bounded harm: the client's next save is refused as a conflict. Closing it means buffering the file, which is what streaming exists to avoid. |
| A cancelled listing is not abandoned during its metadata phase (P3) | **Fixed.** The loop over the entries checks the context. |
| No automated coverage of the component behaviour (test gap) | **Fixed.** A jsdom document, an SFC loader for `node:test`, and nine mounted tests; see `design.md` decision 5. |

### Rounds over those fixes

Four reviewers over disjoint file sets, then three, then one.

- **Round 1 (15 findings).** Three were defects the round's own work introduced: the probe is an
  await, so a rename can land inside it, and only the read path corrected for that; `Number(null)`
  is `0`, so an absent `X-File-Size` read as a size of zero and the image bound stopped applying in
  the case it exists for; and `os.Root.Rename` wraps its refusal in a `LinkError`, so a refused
  rename reached the client as a server fault. Two more were the round's own tests: a
  rename-during-probe test whose probe answered "text" and therefore never ran the code it named,
  and an assertion subsumed by another in the same test.
- **Round 2 (5 findings).** Two were again its own tests -- the absent-header check had none, and
  the editor stand-in modelled a multi-change transaction by applying the changes sequentially to
  the evolving document, where CodeMirror reads every position against the document as it was before
  the transaction. The stand-in produced a different document for the same input.
- One reviewer in round 2 **edited the repository** despite being told to work read-only, leaving
  `internal/files` failing mid-run. The edit was sound -- it keeps a rename's own error shape rather
  than collapsing it, because a rename carries two paths and a refusal does not say which end left
  the tree -- and was verified and kept rather than reverted.

### Residuals, all named rather than implied

- ~~A move between two configured roots acts on path strings~~ — **closed in the third review of the
  change**, below. It is refused now, with an error of its own.
- The write's check and its replacement are two adjacent system calls; no primitive makes them one.
  The browser's side of that is specified.
- A root of `/` contains everything, so there is nothing for the handle to refuse, and `os.Root` does
  not prohibit `/proc` traversal by its own documentation.
- A file modified between its classification and its body can be served with the earlier
  classification; the client's next save is refused as a conflict.
- A response that does not report its length is measured rather than refused, which reads a body this
  function would otherwise have kept off the wire.

## The third review of the change

Four subagents again, run concurrently and read-only, scoped so they could not overlap: Go
confinement; Go transport and composition root; the browser; and claims, tests and docs. This round
reviewed six findings from the third report, so every finding below is one *the fixes* introduced or
left standing — the pattern the previous rounds also showed, and the reason the loop is worth
running even when the fix looks obvious.

Three of the four found something in my own work, and one of those was serious.

### The shutdown close was a data race — a defect the fix introduced

`Close` had only ever been called by tests, and a test does not close a set under a running request.
Making `Shutdown` call it put an unsynchronized writer (`handles`, `closed`) in front of a reader
(`Confine`) with nothing between them, and the reader's losing branch indexes a slice that has been
emptied. The reviewer named the lines and the interleaving; two goroutines and `-race` reproduced it
in ten lines, which is how it was confirmed rather than argued about. Fixed with a read-write mutex,
and closed properly at the other end too: a handle closed *underneath* an operation yields
`fs.ErrClosed`, which now maps to the same not-allowed refusal instead of reaching the transport's
fallback as a server fault.

The reachability argument is what makes it more than theoretical: `http.Server.Shutdown` is bounded
by a context, so it returns when the budget expires, and the force-close that follows closes
connections without stopping handlers.

### The delete waited on the wrong thing, and the reason was written down

Both the browser reviewer and the claims reviewer independently found it, from different directions,
which is the strongest signal this loop produces. The wait was on `doomed` — the tabs the delete is
about to close — on the stated reasoning that those are the paths a save of that subtree can be
travelling for. They are not: a write outlives its tab. Save a file, close the tab, delete the file,
and the wait is empty while the write is still in flight.

The second reviewer's version was sharper: even leaving tabs aside, the *refusal* was exact-path, so
a save of `/d/dir/f.txt` was neither waited for nor refused while `/d/dir` was being deleted. Two
statements I had written — in `pending.ts` and in the specification — said otherwise.

Both are now the same fix: the record covers the subtree, and both the wait and the refusal ask by
path. The lesson is in the shape of the mistake rather than the code: the reasoning that justified
the narrow version was written down, sounded sound, and was never checked against the case that
breaks it, and the tests both deleted a *file*. There is a directory test now.

### Claims that were false, of the kind this round was fixing

- "Nothing reaches it: a rename names a sibling of its source." A destination typed into the prompt
  may descend, and a link on the way may lead into another configured root — so the refusal is
  reachable from the browser and needed a client message, which the same round's other reviewer had
  separately reported missing. The sentence was in six places (`confine.go`, both specifications,
  both READMEs, `design.md`) and all six were corrected — though the first attempt at that is itself
  the first item of the next round, below.
- "`renameAt` is the last call in the package on a plain path." Every helper has a plain-path
  branch; what is true is that with roots configured none of those branches is reachable.
- "The write that never answers leaves the delete unsent" — true, and incomplete: the marker stays
  up, so saves of that path are refused from then on. The behaviour is the better of the two
  available; the description was not.
- `Close`'s "safe on a service built without roots" — such a service panicked on every other method.
  `NewService` substitutes an empty set now, so the type has one story.

### Closed from the report, and the gap it left

The report's own finding about the error-code table was "the user impact is nil, the gap is that
nothing enumerates codes". Both halves were right: the new code was missing, *and* nothing would have
caught the next one. `reasons.test.ts` now reads the hub's error mapper out of the Go source and
asserts every code it can answer with has words in the browser's table — the one place a test here
reads across the language boundary. A generated artifact would hold them together as well; a list on
either side would only be a second thing to forget.

### Residuals after this round

- The write's check and its replacement are two adjacent system calls. The browser does not let its
  own two requests race across it; a write from another process is not ordered by anything the
  browser can do.
- A root of `/` contains everything, so there is nothing for the handle to refuse.
- A file modified between its classification and its body can be served with the earlier
  classification. A post-scan re-check was considered and rejected: it cannot tell an append from an
  in-place rewrite, and refusing on either would break reading a log that is being written to, which
  the specification allows.
- A response that does not report its length is measured rather than refused.
- A delete waiting on a write that never answers waits forever, and the marker stays up with it.
  Named in `design.md` 1b rather than described as transient; the tree still shows the file, which is
  the truth.

## The fourth review of the change

Three subagents, again read-only and disjoint: Go confinement and synchronization; the browser; and
claims, tests and docs. Their brief was to attack the fixes rather than the original code, which is
where the previous rounds kept finding things — and this one was no different. Four of the findings
below are defects the fixes introduced.

### A relation written in one direction, used from both ends

The worst of them, and the one nobody reported: it was found by a test written to close a coverage
gap the browser reviewer had just named. He observed that the *wait* was covered end to end and the
*refusal* only through the predicate's own unit test, so a mounted test was added for the refusal —
and it failed immediately, because `isPending` and `idle` are asked from opposite ends and had been
given one predicate. A delete names an entry and waits for the writes inside it; a save names an
entry and asks whether anything holding it is being deleted. "Covers what is beneath it" is right for
the first and exactly backwards for the second, so a save of `/d/dir/f.txt` was refused by an
outstanding delete of that file and *not* by one of `/d/dir`.

The unit tests had agreed with it, because they asked the predicate from the same end the
implementation answered from. That is the general lesson of this round, and the reason a coverage
gap is worth closing even when the code looks right.

### A decode event with no ticket

`ImagePreview` guards its fetch with a ticket precisely because a load can be superseded, and the
`@error` handler added in the previous commit was the one path in the file without one. Leaving a
file for another revokes its blob, which aborts the decode and raises `error` for a URL the component
has already let go of — and the revoke queues that event ahead of the next fetch, so it normally
arrives while the next file is still loading. Unticketed it marked the *next* file undecodable, named
it in a warning, and hid the image that would have rendered. Fixed by recording which load the URL on
screen came from.

### One cause, two answers

Making `Close` a production path meant an operation could fail with a handle closed underneath it,
and the previous commit mapped that to `path_not_allowed` — but only on the routes that classify
through `classifyPathError`. A write and a recursive delete report their own failures, so the same
shutdown race answered `write_failed` on them. The mapping is now shared by all of them.

### Four claims that were still wrong

The pattern this change keeps producing, and this round produced four more:

- `tasks.md` 12.1 still carried the sentence the next item claimed to have corrected.
- The replacement sentence in `design.md` — "no operation reaches a syscall on a plain path" — was
  false too, and contradicted `review.md` ninety lines away: the authorization walk itself runs on
  plain paths, whatever the configuration. `Resolve` canonicalizes and `Lstat`s them; that is what
  authorizing a path *is*. What is true is narrower: none of the `c.root == nil` branches in
  `confine.go` is reachable with roots configured.
- "Corrected in four places" was six.
- `reasons.ts` claimed reading the Go file was the only way to hold both halves of the code list
  together, where a generated artifact would also do it, at the cost of a build step.

### Findings accepted rather than fixed

- A listing that loses its handles to a shutdown degrades: an entry whose metadata cannot be read is
  reported without its size rather than failing the listing, which is what `entryFor` does by design.
  Newly reachable — before this change the same race panicked — and left as it is.
- `NewService` substituting an empty root set for a nil one makes "forgot the roots" and "wanted no
  roots" the same thing at that layer, in the permissive direction. Deliberate: an empty set is the
  documented default configuration, and the distinction belongs where configuration is read. The
  comment records the choice.
- The commit message for the previous round filed the substitution under "claims corrected" when it
  also changed behaviour. The record has it in `tasks.md` 13.6 and `design.md` 6; noted here because
  the message alone reads as though nothing changed.

## The fifth review of the change

Three subagents again, read-only and disjoint, over the fixes the fourth round produced. This round
found one real gap in those fixes, two wrong statements about mechanisms, four more prose defects,
and — for the first time in five rounds — nothing wrong with the *behaviour* of the synchronization,
the predicate directions, the ordering, or the classification.

### The decode guard was half a guard

The fourth round's ticket stopped a stale decode event from being read against the next file while
that file was *loading*. The reviewer showed the other half: once the next file has rendered,
`shownAt` matches the live load, the ticket passes, and a stale event from the element the previous
file left behind is still attributed to it. That element is reachable — the template tears the branch
down on a switch, so Vue builds a fresh node and the old one keeps its listener — and the ordering
that produces it (a slow decode whose revocation is noticed only after a fast next load) is ordinary.

Fixed by keying the image on its own URL and checking that the event's target is the live element:
identity answers what the ticket cannot, because a superseded URL is now a different node rather than
the same node re-pointed. Both halves are pinned by the one test, which was confirmed to fail with
the element check removed.

### A test that pins the helper and not the routes it serves

The fourth round's classification fix made one cause answer the same way on every route, and its test
calls the three helpers directly. The reviewer showed that reverting any one *call site* to the old
inline wrap leaves the test green, because nothing drives `Write`, `Rename` or `Delete` against a
handle that closes mid-operation — a closed set refuses earlier and a closed handle fails the
preceding `stat`. So the property is pinned where it can be and the gap is named (`tasks.md` 15.9)
rather than papered over with a test that would not have failed.

### Two statements about mechanisms

- `RootSet.Close`'s comment said a handle obtained just before the close is "left with a closed
  descriptor, which makes its operation fail rather than act". Wrong: `os.Root` refcounts, so an
  operation already inside its syscall completes against a descriptor the close keeps alive. Only one
  that *begins* after the close is refused. The conclusion held; the reason given for it did not.
- `NewService`'s comment implied the composition root takes its nil-substituting branch. It cannot:
  it always passes a set. The substitution is for a caller constructing a service directly.

### Four prose defects, including one that contradicted its own document

The recurring class, and this round it produced the clearest instance yet: `design.md` decision 1b
states the current design as a bullet list, and the first bullet still gave the *replaced* premise —
a delete asking about the tabs it is about to close "and nothing more" — which the paragraphs twenty
lines below record as the defect that was fixed. A reader who takes the bullet list as the design
gets the design that was removed.

The other three: `tasks.md` 12.1 still carried the claim 13.6 said it had corrected (fourth round);
"The only way to hold the two halves against each other" was corrected in `reasons.test.ts` and left
standing in `reasons.ts` and `review.md` (fourth round); and `idle`'s justification named a caller it
does not have (found independently by two reviewers this round).

### Accepted, and named

- A create is not ordered against an outstanding delete. Either ordering leaves the user with the
  empty file they asked for or with no file at all, and nothing of theirs is lost — which is exactly
  what distinguishes it from the save case, where the ordering exists to stop an edit being undone.
- The image decode-failure path rests on a browser raising `error` for a revoked blob URL. No test
  here can check that, because they dispatch the event themselves, and the fallback's test asserts a
  premise about the platform rather than about this code.

## The sixth review of the change

Three subagents, read-only and disjoint, over the fixes the fifth round produced. The result is worth
recording precisely, because it is the first round of six that found **no confirmed defect in the
behaviour of anything**: not the synchronization, not the predicate directions, not the ordering,
not the classification, not the image preview. What it found is one plausible hole it could not
construct, three items the record did not name, and four more prose defects.

### The hole that turned out not to be one, and the test that could not fail

The identity guard added in the fifth round rests on the node on screen being a different node from
the one a stale event came from. The reviewer showed that a key on the *URL* would leave that resting
on the platform producing a distinct string per call — which it does, but which this code cannot
check — and labelled it PLAUSIBLE rather than CONFIRMED, having been unable to construct it.

Tracing it settled the question the other way, and the reason is worth keeping: `release()` clears
the URL before every load, so the `v-else-if` branch is torn down and a node never survives from one
load to the next — whatever the key says. The element is always fresh, so the guard is exact by
construction. The key was changed to key on the *load* anyway, but only so that the node's identity
and the handler's ticket are the same fact rather than two facts that happen to agree; the comment
says exactly that, and no more.

The test written to pin it was then deleted, because it passed with either key. A test that passes
whatever the code says is not evidence, and this branch spent five rounds learning that. What it
would have claimed is recorded instead, in the comment, as a property of `release()`.

### The Go round changed nothing but the record

`git diff` over the Go tree is two comments. The reviewer verified the rewritten `Close` comment
against `$GOROOT/src/os/root_openat.go`'s refcounting directly — including that `rootRemoveAll` is a
single `doInRoot` call, so one ref covers the whole recursive walk — and could not falsify item
15.9's claim that the three `writeFailure` call sites are undrivable. It sharpened it instead: the
close must land in one of three adjacent-syscall gaps, and it traced the one injection point a test
might plausibly use (a body that closes the handle mid-copy) to a path that *does* report correctly,
so that case is not among the uncovered ones.

It also named the one thing no previous round had: `StartDirectory` answers from a closed set. It
acts on nothing and is unreachable once the server has stopped, but it is reachable by an embedder,
which is the stated reason `Close` exists — so it is in `design.md`'s accepted items now rather than
unnamed.

And it found the same species of defect this round was fixing, in the file the round edited:
`tasks.md` 14.3 said the closed-handle test "covers the three routes" while 15.9, fifteen lines
below, said it "pins the helper, not the routes". The test is renamed for what it checks.

### The claims round

Four more, three of them narrowings rather than falsehoods: the create's comment promised a refresh
that shows the outcome, where only the delete's refresh is guaranteed to postdate the unlink; a
cross-reference in `design.md` pointed two blocks short of what it named; the two READMEs and the
spec scenario described the refusal as covering the file itself, where the upward predicate also
covers a directory above it; and the commit message accounted for a user-visible notice change under
"prose defects". The last is this round's own commit message problem and is recorded here because
the message cannot be amended without rewriting the commit.

### Where this leaves the loop

Six rounds have produced, in order: a data race and a real ordering hole; a predicate written in one
direction; one narrow gap in a guard added to fix the second; and then, this round, nothing wrong
with any behaviour at all. The findings have been in the *record* — sentences about what changed,
left standing after the thing they described was replaced — for three rounds running, and each round
writes more of that record, which is what the next round checks. The lever is to stop growing the
narrative, not to keep reviewing it.

## The seventh review of the change

The five rounds before this one all reviewed the *latest commit's delta*. This one asked a different
question of three fresh reviewers: read the branch as one artifact — the code the fifteen commits
produced, its seams, its configuration, its product documentation — and say whether you would merge
it. None of them had seen any of it before.

The result is the strongest evidence the change has had, and it is worth stating in their terms
rather than mine: two of the three opened their verdict with "nothing blocking" / "no", and the
third's one actionable finding was about a module boundary rather than behaviour.

### What the whole-change read bought that the deltas could not

- **`Delete`'s emptiness check was the last unbounded operation**, and no delta review could see it:
  each had looked at the commit that introduced a bounded reader, not at the sibling call the change
  had routed through the new helper without noticing it answers the same question the expensive way.
  A directory holding a hundred thousand entries cost a hundred thousand entries' worth of memory to
  establish that it was not empty — in a change whose own design notes promise the opposite. Fixed by
  reading one entry.
- **A module boundary that held only by a keyword.** `tabs.ts` claims to be free of the renderer and
  named the renderer's module in an `import type`. `Classification` now lives in a leaf module of its
  own. This one had been *declared closed* by the second review, on the evidence that `tabs.ts`
  imports `./api` only — which was wrong then and is recorded as wrong now.
- **The seams, re-derived rather than trusted.** That the staging record, the listing filter and the
  confinement layer all key on the same canonical string; that `looksBinary` scans `[0, size)` while
  the transport serves `SectionReader(file, 0, size ≤ that)`, so the classification covers a superset
  of the bytes sent; that `readListing` is correct at both `limit` and `limit+1`, because
  `File.ReadDir` returns `io.EOF` only alongside an empty slice; and that read and write *follow* a
  final symlink while delete and rename do not, which is what each operation means.
- **A product-claim audit that checked numbers, not prose.** Every documented configuration key and
  environment variable, every quantity the product states, the boundary paragraph, the reserved
  session-name set, the tmux defaults and `SECURITY.md` were each checked against the code that
  implements them, and a sweep for the wording this change superseded found none of it left.

### What it found that the deltas had also missed, and what was done with it

Three items were named rather than changed, all pre-existing: the in-app help renders Markdown
without sanitizing (bounded — the guide is compiled into the binary); two CLI exit paths return
without closing the root handles (bounded — the process exits); and the manager's `report` renders a
rejected token and a transport failure as `AuthError: AUTH_FAILED` and `TypeError: Failed to fetch`,
with `listDirectory` not guarding a malformed body where `writeFile` does. Each is in `design.md`'s
accepted items with the reason it is bounded, which is the same disposition the change gives its own
residuals — the difference being that these predate it.

Two of the audit's findings *were* this change's, and both were overstatements of the same kind it
had spent five rounds removing: the READMEs said a file over the preview bound is "transferred no
further than the bound" without the condition that makes it true, and a clipboard notice named one
cause as though it were the only one. Both corrected.

### On stopping

The trajectory over seven rounds: a data race and a real ordering hole; a predicate written in one
direction; one narrow gap in the guard added to fix it; then nothing wrong with behaviour in two
consecutive rounds of fix-review; and now, on a fresh whole-change read by reviewers who had seen
none of it, no correctness or boundary defect either. What remains is a list of accepted costs and
pre-existing gaps, each with the reason it is bounded — which is the state this change set out to
reach, and the point at which another round would be reviewing its own summaries rather than the
code.

## The eighth review of the change

Two reviewers over the whole-change round's own commit, which is small and behavioural and therefore
gets the same treatment as every other fix on this branch.

Both findings are the same shape, and it is worth naming because it is the last one this loop
produced: **an improvement that is correct and pinned by nothing.** The module boundary in the
previous round holds — the reviewer followed `tabs.ts`'s whole runtime graph to `api.ts` and then to
a module with no imports at all, so the built application reaches neither `preview` nor the sanitizer
— but nothing asserted it, and it had already been broken once by a `type` import that a review read
past. There is a test now. And the rooted half of the new emptiness check was unexercised: the
unrooted cases cover both answers, and the rooted ones only ever refused, so a check that called every
rooted directory non-empty would have made them undeletable with no test failing. `dirHasEntries`
answers through the root's handle, which is a different branch, and it is covered now.

Neither is a defect in the code as it stands. Both are the difference between code that is right and
code that would stay right, which is what the last three rounds have been converging on.

The reviewer also confirmed by building a throwaway probe, rather than by reading, that the new
emptiness check classifies identically to what it replaced across every shape asked of it -- missing,
a regular file, an empty directory, a one-entry directory, a mode-0000 directory, a readable-but-not-
executable one, and a symlink -- and that the `io.EOF` swallow is load-bearing rather than dead,
since an empty directory returns exactly that.

## The ninth review of the change

Two reviewers, both new to the code, given the newest commit and the whole state it leaves, and asked
for a plain answer if there was nothing. It is the first round on this branch where one of them
could write that sentence and mean it.

**The Go reviewer: "nothing I can defend."** It said that after trying three mutations against the
round's new assertion — including `if c.root != nil { return true, nil }`, which breaks only the
rooted-and-empty cell and which *this assertion is the only thing that catches* — and after walking
every helper in `confine.go`, both `ReadDir(1)` shapes, the whole `Delete` chain, and the window
between the emptiness read and `remove` (closed from the other side, because an entry added in it
makes `remove` fail `ENOTEMPTY`, which the classifier already maps). A negative result from a reviewer
who looked is worth recording as such; that is why this paragraph exists.

**The browser reviewer found three claims the previous round had left stale**, and they are the same
species the branch has been chasing for five rounds — the user-facing notice was corrected for the
ancestor case and the module's own documentation was not, so `SaveSettlement`'s doc, the comment above
the check, and a test comment all still described `raced` as path-exact. `OpenFile.size`'s doc named
one of its four sources, and it is not a display field but an argument that decides which size bound
applies. Both are fixed.

**And it walked the boundary test through five shapes**, showing that the test searched for the
*statement* rather than the specifier — so a multi-line import, which this repository writes sixteen
times, plus side-effect and dynamic imports and re-exports, all slipped past a test whose whole
purpose was to catch a silent break. That is the third time on this branch that a check written to
close a gap was itself too narrow, and it is the reason the last rounds have been worth running: the
code has been right for three rounds, and what kept being wrong was the confidence in it.

One false sentence stays in a commit message — 9c95c63 says every rooted delete in the tests "either
refused or removed a symlink", where one rooted recursive delete succeeds through `removeAll`, which
never reaches the emptiness check. The claim it supports holds and `tasks.md` 18.2 states it
precisely; a commit message cannot be amended without rewriting what was reviewed, so it is recorded
here instead.

## The tenth review of the change

One reviewer, given the two questions this branch has taught itself to ask: is any check narrower than
it claims, and does any claim describe a superseded behaviour. Both answers were yes.

**The check.** The error-code coverage test reads the hub's `mapFileError` and claimed to cover "every
refusal in the file routes". Every file route is wrapped by `requireLocalHost`, which answers
`host_not_found` — a code with no entry in the table, which the test could not see because it reads
one file. A request to a host that is not this one would have shown the user the bare identifier,
which is exactly what the table exists to prevent and what the test exists to catch. Unreachable
through today's UI, since the client hardcodes the local host; the defect is a check that cannot see
the gap it claims to cover, and it is the second one of those found in two rounds.

**The claim.** Two comments in the manager said "a path covers what is beneath it" while describing an
`isPending` query, which looks *up* — `pending.test.ts` asserts the opposite reading, directly, in a
test whose comment says "a file was reported for the directory holding it". The effect the comments
described was correct and the mechanism was backwards, which is what a reader would have carried away.
The previous round found three of this species in `tabs.ts` and swept one file; this one swept the
other.

**And a second way past the widened test**: `import(/* @vite-ignore */ './preview')`, a real Vite
idiom, matched neither pattern.

### Where the loop has arrived, and why it stops here

Ten rounds. The first five found defects: a data race, an ordering hole, a predicate written in one
direction, a guard that covered half its race. The sixth and seventh found a real cost defect and an
unpinned boundary, both only visible from a whole-change read. Rounds eight, nine and ten found no
product defect at all — they found checks that were narrower than their claims, and claims that
described behaviour the branch had already replaced. Every one of those has been fixed, and each fix
gives the next round something new to measure.

That is a tail without a fixed point: a check can always be narrower than its claim, unless the claim
is narrowed to match, and this round did that too. The product code has been unchanged by five
consecutive rounds; what remains is a list of accepted costs and pre-existing gaps, each with the
reason it is bounded, which is the state this change set out to reach.

## The eleventh review of the change

The seven findings came from a review of the branch as a whole rather than of its newest commit. Six
were defects, one was a claim the code could not support; each was reproduced before anything was
changed, and the reverts are recorded in `tasks.md` section 21.

**The one that lost data.** An image is opened without being read: the preview fetches what it needs,
and the bytes are never decoded as text. That is sound only while the *name* says image, and a rename
replaces the name. Renaming `photo.png` to `photo.txt` left the tab in the editor as an empty document
whose `saved` text was also empty, so one keystroke made it dirty and Save wrote that document over the
picture. Two things were wrong and both are now fixed: the presentation rule let the name decide in
*both* directions for a file nobody had read, and nothing read the file the new name described. The rule
now says what it can support -- an unread file may be *shown* as an image, because the preview decodes
nothing, and may not be *edited*, because there are no contents in hand -- and a rename to a name that
asks for the editor reads the file so that the tab becomes what the hub says it is. Until that answer
lands the tab presents as information, which is what makes the read safe rather than merely fast: there
is no window in which the editor holds a document nobody read, whether the read is still travelling or
has failed. This changed a rule the frontend tests had pinned, and the tests were changed with it.

**The delete that a read outlived.** The manager's delete sweeps the tabs it finds when the hub answers,
and a read the hub had already granted can land behind that sweep -- so the sweep was a moment rather
than a rule, and the tab installed behind it named a path that was gone and could only be refused when
saved. The first attempt at this asked the *tree*: refuse the tab when the directory's cached listing is
known and no longer names the file. That was written, and a run of the existing tests rejected it -- the
rename-during-a-probe test renames a file the static test hub keeps listing under its old name, so the
check refused a legitimate open. A check that answers from a cache can be wrong in both directions, and
the cost of the false refusal is a file the user cannot open. What replaced it is the delete's own
record: a delete that the hub has answered says what it removed, for the reads that were travelling when
it was answered, and those reads install nothing. Nothing is inferred, so nothing is inferred wrongly;
the module that held renames now holds both and is named for it.

**The cache that stepped backwards.** `loadDirectory` wrote the tree's listing cache before its caller
could check whether that load was still the current one, and the check it had -- a navigation ticket --
therefore came after the damage. Two waits for one directory overlap whenever a refresh and a navigation
name it, or two navigations do. The rule is now where the cache is: a wait takes a ticket for the path
it is reading, and only the newest may write it. The ticket is issued *after* the cache check, because a
call answered from the cache makes no claim about the disk and must not retire a wait that does; and a
directory that is dropped retires the waits for it, so a forgotten listing cannot be put back by its own
answer. Three of the four new cases fail against one of those two decisions being wrong.

**A promise about filesystems.** `files.enabled: false` says the capability is off without touching the
filesystem, and the hub resolved every configured root while loading the configuration and opened a
handle on each while building the service. A root that was missing, or unreadable,
therefore stopped the process -- taking the terminal and the session list with it, which the READMEs
explicitly promise will not happen. The roots are no longer resolved while the capability is off and are
no longer opened, so what the service is in that state has to be said rather than assumed: no roots and
no roots and no boundary, behind a transport that refuses every file route before it is called -- and it
is the transport's gate, not the construction, that keeps it out of reach.

**The menu that was not one.** The context menu declared `role="menu"` and its entries `role="menuitem"`,
and it behaved like a list of buttons: no focus on open, no arrow keys, no Home/End, and no focus given
back. Every action it holds -- rename, delete, download, copy path, insert path -- was reachable only
with a pointer, and a browser that raises `contextmenu` from the keyboard (Shift+F10, or the menu key)
opened it somewhere the user was not looking, because such an event reports the pointer origin rather
than a position that means anything. It now takes the focus, walks the actions a user can choose, skips
the disabled ones rather than stopping on them, hands the focus back to the row it came from, and is
anchored to that row when the event carries no pointer. The test mounts into the document, because focus
is a property of one and a detached element cannot hold it -- which is also why the previous rounds'
tests could not have caught this.

**A contract that was not one.** The create route read `kind` and made everything that was not `dir` a
file, so a request asking for a directory -- `"kind":"directory"`, a spelling from another tool -- made
a file at that path and answered 204. The field is now required and checked against the two names the
hub has, and the decoder shared by the three change routes refuses an unknown field, a second value
after the object, and an empty body. The browser and the hub ship together, so a field one of them does
not know is a mistake rather than an older version; silently ignoring it is how a create, a move, or a
delete becomes an action nobody asked for.

**The claim, not the defect.** The last finding read `ParsePanes`' doc comment, which said a field
carrying a newline could split a record and forge one for another session, and named executable names
and argv as the way in. Neither half survived measurement. Against a real tmux, a session name and a
window name containing a newline are refused when they are set, and `select-pane -T` refuses a title
containing one -- silently, leaving the title as it was, which is why the test reads the title back
rather than a status. The comment was the defect: it described a vulnerability as a known limitation,
which is how a reviewer came to report a live one.

The first draft of the corrected comment was wrong in the other direction, and the review of this round
caught that too. It said a break would *always* drop the pane's own record and let the tail parse in its
place. Measured, that is one of two outcomes: a break in the **last** field leaves the head with all nine
fields, so the pane is read with its command truncated and the tail is an extra record -- a forgery that
loses the pane nothing. Both outcomes are now pinned by
`TestALineBreakInAFieldWouldNotBeDetected`, because the difference is what makes the unmeasured field the
one worth naming.

That field is the command, the one a program names itself, and it is the only one no test covers: what
tmux reports for it was seen to escape a line break once, which later attempts could not reproduce, and
a process whose own name carries one could not be produced on this machine -- a copy of a system binary
under such a name does not run. tmux escaping control bytes on its way out is real behaviour, which is
why the field separator in this code is printable, so the observation stands as the likely answer rather
than being dismissed. The comment now states what the parser does and does not do, names the guarantee's
owner, and records that limit instead of implying the whole protocol is measured.

### The review of this round's fixes

Three reviewers were given the fixes themselves, split across the browser, the hub's Go, and the claims
this record makes. Eight findings came back, and every one was reproduced before it was acted on; the
reverts are in `tasks.md` section 21, and the two that changed product code are worth naming here.

**A read that landed on a tab which was no longer unread.** Filling in a renamed tab identified the tab
by session *and path*, and a path cannot tell one read of it from another. A tab renamed away from a text
name and back -- image name, text name, image name, text name -- leaves two reads in flight for one path,
and the second to land replaced the contents the first had brought: the user's keystrokes went with them,
and the tab reported itself saved against contents they had never seen. The fix is not another ticket. It
is that the *state* the answer was asked about is what makes it wanted: only a tab that is still unread
is filled in, so an answer that arrives for a tab which has since been read -- or edited -- is dropped.

**A reason the panel had no basis for.** The information panel decides between "binary or too large"
and "nobody has read this" by asking what the name asks for. That is false for a rename to a
binary-extension name: `archive.zip` is not a name the editor would hold, so no read is started, and the
panel then described a file it had never looked at as binary -- at five bytes. What separates the two
cases is whether the name says *image*, which is size-independent where the other question is not.

**And three of the eight were claims rather than behaviour.** The disabled file manager was described as
"failing closed" when it has no boundary at all and the operations that change the filesystem consult no
limit; the standing `runtime-configuration` requirement that a root which cannot be used is a startup
error had been contradicted without being amended; a wire contract had been made stricter with nothing in
the specification saying what a request carries. Each is the species the last three rounds of this branch
kept finding, and each was found by a reviewer reading the *change* rather than the diff.

### What this round says about the previous one

The tenth round concluded that the product code had been unchanged for five rounds and that only claims
and checks were still being found. This round found six product defects, two of them reachable in
ordinary use, one of them a data-loss path -- from a review given the branch as a whole rather than the
latest commit's delta, and asked what a user could actually do to it. The review of those fixes then
found two more in the fixes themselves, one of which was another silent loss of the user's work: the
instrument that found the first six answers a different question from the one that found these two, and
neither answers the other's. The tenth round's own conclusion
was that a check can always be narrower than its claim; the sharper lesson here is that a *reader* of
the code is a different instrument from a reader of the diff, and that the accessibility of a component
is a product property that no amount of prose about it will exercise.
