# Review

Independent review of this change. The reviewers did not write the code; each was given a disjoint
file set so they could not duplicate each other, and each was asked for a concrete failure
scenario per finding rather than a style opinion.

## Round 1

Four subagents, run concurrently:

| reviewer | scope | outcome |
| --- | --- | --- |
| Go service | `hub/internal/files/{service,model,guard,errors}.go` | 4 findings, 2 CONFIRMED |
| Go transport and tmux | `hub/internal/transport/http/{files,dto}.go`, `hub/internal/tmux/panes.go` | 2 findings, 1 CONFIRMED |
| Frontend | `hub/web/src/**` | 6 findings, 5 CONFIRMED |
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
