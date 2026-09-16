## 1. Writes bound to the entry they started against

- [x] 1.1 `hub/internal/files`: capture the target's identity from the `os.Stat` the pre-transfer check already performs, carry it in `writeOptions.origin`, and re-check it in `confirmUnchanged` immediately before the rename. Verify: `TestAForcedWriteRefusesAFileReplacedDuringTheTransfer` replaces the target inside the transfer window and asserts a conflict; `TestAForcedWriteDoesNotRecreateARenamedFile` renames it and asserts not-found with no file at the original path and no staging residue.
- [x] 1.2 Apply the same check to a name that was free when the write began: a write whose `origin` is nil requires the name to still be free. Verify: `TestAWriteRefusesANameTakenWhileItWasBeingWritten` asserts a conflict and that the created file is untouched; `TestAWriteStillCreatesAFileThatStaysAbsent` asserts the create path still works.
- [x] 1.3 The identity check is unconditional (a forced write is compared against nothing but is still checked for still being the same entry), so a forced overwrite is no longer a window. Verify: the two tests above both pass a nil expectation.

## 2. Modification times compared at full precision

- [x] 2.1 `hub/internal/files/model.go`: add `WriteResult{Mtime, MtimeNanos}` and `ExpectedMtime{Millis, Nanos *int64}` with a `matches` that uses the exact value when present and milliseconds otherwise; `Write` returns `WriteResult` and takes `*ExpectedMtime`. Verify: `TestAWriteComparesModificationTimesAtFullPrecision` asserts a conflict for an observation one nanosecond stale with the millisecond value unchanged, and a clean save with the exact value.
- [x] 2.2 `ReadResult` carries `MtimeNanos`; the transport sets `X-File-Mtime-Nanos` as a decimal string (never a JSON number) and reads `expected_mtime_nanos`; the write response carries `mtime_nanos`. Verify: `TestReadRouteExposesTheExactModificationTime`, `TestWriteRouteCarriesTheExactModificationTime`, and `TestWriteRouteBoundsTheBodyByTheFileLimit` (which asserts the string field).
- [x] 2.3 Frontend: `Stamp{millis, nanos}` in `files/api.ts`, with `nanos` never parsed; `OpenFile.mtime` becomes `OpenFile.stamp`; `writeFile` sends both and adopts what comes back. Verify: `tabs.test.ts` "a save sends the exact modification time it was given" and `api.test.ts` "readFile carries the exact modification time as an opaque string" / "writeFile adopts the exact modification time it is given back".
- [x] 2.4 A hub that reports only milliseconds still works, on both sides. Verify: `api.test.ts` "readFile tolerates a hub that reports only milliseconds"; `ExpectedMtime.matches` falls back to milliseconds when `Nanos` is nil.

## 3. A save answer is not recorded against a path the tab has left

- [x] 3.1 `files/tabs.ts`: add `settleSave(files, id, sent, sentPath, stamp)`, returning the new file list and an outcome of `saved` / `moved` / `closed`. Verify: `tabs.test.ts` covers all three, including that a `moved` settlement rewrites nothing about the tab.
- [x] 3.2 `FileManagerOverlay.vue`: capture the path the write names before sending, pass it to `settleSave` after the await, report `moved` as a warning notice, and look the tab up by session at answer time rather than reusing the object captured before the await. Verify: manual — rename a file with a save in flight and confirm the tab stays dirty and the notice appears.
- [x] 3.3 A conflict prompt is not offered for a tab whose path moved: answering it would write to whatever is at the new name. Verify: the catch path checks the live tab's path before prompting.

## 4. Reading: whole-file classification, regular files only, exact length

- [x] 4.1 Replace the sample with a streaming scan (`textScan`) that decides at the first decisive byte and carries a rune cut in half across chunk boundaries. Verify: `TestTextScanToleratesOnlyATruncatedRune`, `TestBinaryIsDecidedByTheWholeFileNotItsPrefix` (binary part past the old sample point), and `TestATextFileIsNotClassifiedByWhereItsChunkBoundariesFall`.
- [x] 4.2 Open with `O_NONBLOCK` and decide the kind from the descriptor; refuse anything that is not a regular file. Verify: `TestReadRefusesANamedPipeInsteadOfWaitingOnIt` fails on a ten-second timeout rather than passing by accident, and `TestReadRefusesADirectoryAndEveryOtherKind`.
- [x] 4.3 Serve exactly the length that was checked: `io.NewSectionReader(result.File, 0, result.Size)` in the transport. Verify: `TestReadRouteServesExactlyTheLengthItAuthorized` gives the handler a ten-byte descriptor with an authorized size of four and asserts body, `Content-Length`, and `X-File-Size` all say four.

## 5. Listings bounded by the listing bound

- [x] 5.1 `readListing` reads `limit + 1` entries in batches with a context check between them; `List` sorts explicitly (directories first, then by name) rather than relying on the reader's order. Verify: `TestReadListingStopsOneEntryPastTheBound`, `TestReadListingReportsNoTruncationForADirectoryThatFits`, `TestReadListingStopsWhenTheCallerIsGone`, and the pre-existing ordering and truncation tests.
- [x] 5.2 Describe a symbolic link by its target, resolved through the guard so a link out of the boundary is not described by it. Verify: `TestListReportsALinkToADirectoryAsADirectory` (including that a link to a file reports the target's size) and `TestListDoesNotFollowALinkOutOfTheBoundary`.

## 6. The pane working directory is returned as the pane holds it

- [x] 6.1 `hub/internal/tmux/panes.go`: `strings.TrimSpace` becomes `strings.TrimSuffix(s, "\n")`, so only the terminator tmux appends is removed. Verify: `TestPaneWorkingDirectoryKeepsWhitespaceInTheName` (trailing space, leading space, tab, internal runs, a name spanning two lines) and `TestPaneWorkingDirectoryKeepsANameThatIsOnlyWhitespace`.
- [x] 6.2 Pin the assumption the fix rests on — that tmux writes the directory and exactly one newline — against a real server. Verify: `TestPaneWorkingDirectoryKeepsATrailingSpaceAgainstARealServer` creates a session in a directory whose name ends with a space and asserts the path comes back byte-exact.

## 7. Editor state survives switching files

- [x] 7.1 `FileManagerOverlay.vue` keeps every editable tab's `CodeEditor` mounted and shows only the active one, keyed by tab id so a rename keeps the instance. Verify: manual — edit a file, switch away and back, undo still works.
- [x] 7.2 `CodeEditor.vue` gains an `active` prop and requests a measure when it becomes visible, since an editor laid out while hidden has no dimensions. Verify: manual — open two files, switch between them, and confirm the second is laid out at full size.
- [x] 7.3 Move the "which tabs the editor holds" rule into `files/tabs.ts` as `editable`/`presentation` so it is testable without a DOM. Verify: `tabs.test.ts` "the editor holds source, and nothing it must not render as text" and "presentation follows the hub over the file name".

## 8. Boundary claims match the implementation

- [x] 8.1 `openspec/specs/file-manager/spec.md`: state what `roots` does and does not guarantee, with a scenario for a caller naming a path that resolves outside every root and one for another local process replacing a path component. Verify: read the "Filesystem access boundary" requirement; the limit is stated rather than implied.
- [x] 8.2 `README.md` and `README.zh-CN.md`: the same statement in operator-facing words, plus the listing bound, the whole-file classification, the regular-files-only rule, and the commit-time identity check. Verify: read the "File access" sections of both.
- [x] 8.3 Record the `os.Root` migration as the follow-up, with the probe results that ruled it out for this change. Verify: `design.md` decision 1.

## 9. Verification

- [x] 9.1 `cd hub && go test ./... && go vet ./...` pass.
- [x] 9.2 `npm --prefix hub/web test` passes, and `npm --prefix hub/web run build` (which is `vue-tsc --noEmit` plus a Vite build) succeeds.
- [x] 9.3 `make lint` passes (golangci-lint config verify plus a run over `./...`).
- [ ] 9.4 Independent review of the diff by a subagent that did not write it, with the findings fixed and the review repeated until a round yields nothing new. Recorded in `review.md`.
