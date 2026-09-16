## 1. Writes bound to the entry they started against

- [x] 1.1 `hub/internal/files`: capture the target's identity from the `os.Stat` the pre-transfer check already performs, carry it in `writeOptions.origin`, and re-check it in `confirmUnchanged` immediately before the rename. Verify: `TestAForcedWriteRefusesAFileReplacedDuringTheTransfer` replaces the target inside the transfer window and asserts a conflict; `TestAForcedWriteDoesNotRecreateARenamedFile` renames it and asserts not-found with no file at the original path and no staging residue.
- [x] 1.2 Apply the same check to a name that was free when the write began: a write whose `origin` is nil requires the name to still be free. Verify: `TestAWriteRefusesANameTakenWhileItWasBeingWritten` asserts a conflict and that the created file is untouched; `TestAWriteStillCreatesAFileThatStaysAbsent` asserts the create path still works.
- [x] 1.3 The identity check is unconditional (a forced write is compared against nothing but is still checked for still being the same entry), so a forced overwrite is no longer a window. Verify: the two tests above both pass a nil expectation.
- [x] 1.4 *(review round 1)* The free-name branch asks with `Lstat`, not `Stat`: a name taken by a link whose target is gone is taken, and `Stat` reports it as free — which would let the write replace the link the pre-check refuses. Verify: `TestAWriteRefusesANameTakenByADanglingLink` fails with `Stat` restored (verified by reverting) and passes with `Lstat`.

## 2. Modification times compared at full precision

- [x] 2.1 `hub/internal/files/model.go`: add `WriteResult{Mtime, MtimeNanos}` and `ExpectedMtime{Millis, Nanos *int64}` with a `matches` that uses the exact value when present and milliseconds otherwise; `Write` returns `WriteResult` and takes `*ExpectedMtime`. Verify: `TestAWriteComparesModificationTimesAtFullPrecision` asserts a conflict for an observation one nanosecond stale with the millisecond value unchanged, and a clean save with the exact value.
- [x] 2.2 `ReadResult` carries `MtimeNanos`; the transport sets `X-File-Mtime-Nanos` as a decimal string (never a JSON number) and reads `expected_mtime_nanos`; the write response carries `mtime_nanos`. Verify: `TestReadRouteExposesTheExactModificationTime`, `TestWriteRouteCarriesTheExactModificationTime`, and `TestWriteRouteBoundsTheBodyByTheFileLimit` (which asserts the string field).
- [x] 2.3 Frontend: `Stamp{millis, nanos}` in `files/api.ts`, with `nanos` never parsed; `OpenFile.mtime` becomes `OpenFile.stamp`; `writeFile` sends both and adopts what comes back. Verify: `tabs.test.ts` "a save sends the exact modification time it was given" and `api.test.ts` "readFile carries the exact modification time as an opaque string" / "writeFile adopts the exact modification time it is given back".
- [x] 2.4 A hub that reports only milliseconds still works, on both sides. Verify: `api.test.ts` "readFile tolerates a hub that reports only milliseconds"; `ExpectedMtime.matches` falls back to milliseconds when `Nanos` is nil.
- [x] 2.5 *(review round 1)* An exact time the hub would reject is not adopted, on either route: `exactNanos` accepts only a decimal count, so a malformed value costs the finer comparison instead of making every later save answer invalid-body. Verify: `api.test.ts` "an exact modification time is adopted only when it can be carried back", over eight header values including empty, non-numeric, and a decimal point.

## 3. A save answer is not recorded against a path the tab has left

- [x] 3.1 `files/tabs.ts`: `settleSave` returns the new file list and an outcome of `saved` / `moved` / `closed`, and refuses to record an answer against a tab that no longer holds the written path. Verify: `tabs.test.ts` covers all three, including that a `moved` settlement rewrites nothing about the tab.
- [ ] 3.2 `FileManagerOverlay.vue`: the path the write names is passed to `settleSave` after the await rather than read again, and `moved` is reported as a warning notice. Verify: **not machine-verified.** No DOM test harness exists, so nothing here exercises the component; what is testable was moved into `beginSave`/`settleSave` (3.4) and the rest needs an app run, which this change did not perform.
- [x] 3.3 A conflict prompt is not offered for a tab whose path moved: answering it would write to whatever is at the new name. Verify: the catch path checks the live tab's path before prompting.
- [x] 3.4 *(review round 1)* The session, the path, the text, and the expectation are captured together as one `SaveRequest` before the write travels, so the value the overlay passes at answer time is the value it captured and there is nothing to pass the wrong way round. Verify: `tabs.test.ts` "a save request captures the path it will name", plus the `moved` case driven through `beginSave`.

## 4. Reading: whole-file classification, regular files only, exact length

- [x] 4.1 Replace the sample with a streaming scan (`textScan`) that decides at the first decisive byte and carries a rune cut in half across chunk boundaries. Verify: `TestTextScanToleratesOnlyATruncatedRune`, `TestBinaryIsDecidedByTheWholeFileNotItsPrefix` (binary part past the old sample point), and `TestATextFileIsNotClassifiedByWhereItsChunkBoundariesFall`.
- [x] 4.2 Open with `O_NONBLOCK` and decide the kind from the descriptor; refuse anything that is not a regular file. Verify: `TestReadRefusesANamedPipeInsteadOfWaitingOnIt` fails on a ten-second timeout rather than passing by accident, and `TestReadRefusesADirectoryAndEveryOtherKind`.
- [x] 4.3 Bound the served length in *both* directions: a `SectionReader` over the authorized length, clamped to what the descriptor holds when that is smaller, with `X-File-Size` reporting the length actually sent. Verify: `TestReadRouteServesExactlyTheLengthItAuthorized` (growth: a ten-byte descriptor with an authorized size of four) and `TestReadRouteServesAShortenedFileAtItsNewLength` (shrink: verified by reverting the clamp, which fails it with `Content-Length: 10`).

## 5. Listings bounded by the listing bound

- [x] 5.1 `readListing` reads `limit + 1` entries in batches with a context check between them; `List` sorts explicitly (directories first, then by name) rather than relying on the reader's order. Verify: `TestReadListingStopsOneEntryPastTheBound`, `TestReadListingReportsNoTruncationForADirectoryThatFits`, `TestReadListingStopsWhenTheCallerIsGone`, and the pre-existing ordering and truncation tests.
- [x] 5.2 Describe a symbolic link by its target, resolved through the guard so a link out of the boundary is not described by it. Verify: `TestListReportsALinkToADirectoryAsADirectory` (including that a link to a file reports the target's size) and `TestListDoesNotFollowALinkOutOfTheBoundary`. Both assert the entries were found, so a listing that returned nothing cannot pass them by iterating an empty slice.
- [x] 5.3 *(review round 1)* Apply the staging-file skip inside the read rather than to the result, so the bound counts entries the caller will be shown: filtering afterwards made a directory holding exactly the bound report itself truncated while a write to it was in flight. Verify: `TestAWriteInFlightDoesNotMakeAListingLookTruncated` drives a real in-flight write and asserts the flag stays false and all three entries are shown.

## 6. The pane working directory is returned as the pane holds it

- [x] 6.1 `hub/internal/tmux/panes.go`: `strings.TrimSpace` becomes `strings.TrimSuffix(s, "\n")`, so only the terminator tmux appends is removed. Verify: `TestPaneWorkingDirectoryKeepsWhitespaceInTheName` (trailing space, leading space, tab, internal runs, a name spanning two lines) and `TestPaneWorkingDirectoryKeepsANameThatIsOnlyWhitespace`.
- [x] 6.2 Pin the assumption the fix rests on — that tmux writes the directory and exactly one newline — against a real server. Verify: `TestPaneWorkingDirectoryKeepsATrailingSpaceAgainstARealServer` creates a session in a directory whose name ends with a space and asserts the path comes back byte-exact.

## 7. Editor state survives switching files

- [ ] 7.1 `FileManagerOverlay.vue` keeps every editable tab's `CodeEditor` mounted and shows only the active one, keyed by tab id so a rename keeps the instance. Verify: **not machine-verified**, for the reason in 3.2.
- [ ] 7.2 `CodeEditor.vue` gains an `active` prop and requests a measure when it becomes visible, since an editor laid out while hidden has no dimensions. Verify: **not machine-verified**, for the reason in 3.2. *(Review round 1 removed an `immediate: true` on that watcher: it runs during setup, before `onMounted` creates the view, so it was dead code whose comment described a case it could not cover.)*
- [x] 7.3 The "which tabs the editor holds" rule is `editable`/`presentation` in `files/preview.ts`, testable without a DOM and reachable from `files/tabs.ts` without importing the renderer. Verify: `preview.test.ts` "the editor holds source, and nothing it must not render as text" and "presentation follows the hub over the file name".

## 8. Boundary claims match the implementation

- [x] 8.1 `openspec/specs/file-manager/spec.md`: state what `roots` does and does not guarantee, with a scenario for a caller naming a path that resolves outside every root and one for another local process replacing a path component. Verify: read the "Filesystem access boundary" requirement. *(Review round 1: the first wording contradicted the `SHALL` it sat beside and described the affected caller as "one who holds the token and nothing else" — a set the README's own reasoning shows is empty, since a token also authorizes a terminal.)*
- [x] 8.2 `README.md` and `README.zh-CN.md`: the same statement in operator-facing words, plus the listing bound, the whole-file classification, the regular-files-only rule, and the commit-time identity check. Verify: read the "File access" sections of both; they are paragraph-for-paragraph equivalent.
- [x] 8.3 Record the `os.Root` migration as the follow-up, with the probe results that ruled it out for this change. Verify: `design.md` decision 1.

## 9. Verification

- [x] 9.1 `cd hub && go test ./... && go vet ./...` pass.
- [x] 9.2 `npm --prefix hub/web test` passes, and `npm --prefix hub/web run build` (which is `vue-tsc --noEmit` plus a Vite build) succeeds.
- [x] 9.3 `make lint` passes (golangci-lint config verify plus a run over `./...`).
- [x] 9.4 Independent review of the diff by four subagents that did not write it, split across disjoint file sets (Go service; Go transport and tmux; frontend; claims, tests and docs), with each finding reproduced before it was acted on. Findings and their disposition are in `review.md`.
- [x] 9.5 A second round: closure verification of every round-1 finding (20 of 22 closed, each confirmed by reverting the fix and watching the named test fail) plus adversarial re-review of the fixes themselves, split across Go and frontend. Verify: `review.md` round 2. It found one new defect — the exact modification time was validated by shape and not by whether the hub can parse it — which 9.6 fixes.
- [x] 9.6 *(review round 2)* Bound the exact modification time to what a signed 64-bit integer holds, and the millisecond value to a safe integer, since both are decimal strings on the wire that the hub reads back with a 64-bit integer parse; a value outside those bounds would make every later save answer invalid-body with no way out through the interface. Verify: the `api.test.ts` case table includes both int64 bounds as accepted and two overflowing values as rejected, and "readFile adopts only a millisecond time it can send back" covers the fractions and exponents.
