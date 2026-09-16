## Context

See `proposal.md` — Why. This change is a response to a review of `v0.3.1..a863232`, so the
constraints that shape it are the ones the existing implementation already committed to:

- `hub/internal/files` is one service plus one path guard, and every operation resolves
  through `RootSet.Resolve` before touching the filesystem. Anything added here has to fit
  that shape rather than introduce a second way in.
- The wire contract is consumed by one first-party browser client, but its error vocabulary,
  the `expected_mtime` query parameter, and the `X-File-*` headers are a published surface.
  Configuration changes are expensive by construction (nine touchpoints); wire changes are
  cheap but must be *additive*, because a hub and a tab can be out of step across a reload.
- The frontend has no store and no router, and its tests run under
  `node --test --experimental-strip-types` against source with **no DOM**. `loader.mjs`
  redirects `@xterm/*`, `codemirror`, and `dompurify` to stubs. There is no component test
  harness, so anything that has to be verified has to be a plain module first.
- Lint is zero-tolerance: `mnd` (no bare magic numbers), `lll` 120, `funlen` 80/50,
  `gocyclo` 15, `errcheck` with `check-blank`, `nolintlint` requiring every directive to name
  its rule and explain itself.
- The design that shipped this feature (`archive/2026-09-15-file-preview-and-editing/design.md`)
  already records the TOCTOU limit as accepted, with its cost stated. The review's finding is
  not that the limit exists; it is that the *product* documentation still describes `roots` as
  a boundary without it.

## Goals / Non-Goals

**Goals:**
- Every claim the hub makes about a write, a read, a listing, or a boundary is one the code
  actually keeps, or is not made.
- Where a guarantee is narrowed, the cost is paid by the *requester* (a conflict, a refusal, a
  visible truncation) rather than by a silent data loss.
- The cost of an operation is bounded by the configured limit, not by what is on disk.
- New behaviour is reachable from a DOM-free unit test.

**Non-Goals:**
- Upload, git-diff preview, a remote-host file protocol, HTML preview, mobile layout — all
  deferred by the original design and untouched here.
- Rewriting the path guard to descriptor-relative access. See decision 1.
- A component test harness (`jsdom` + `@vue/test-utils`). See decision 5.

## Decisions

### 1. `roots` is documented as a caller boundary, not implemented as an isolation boundary

The review's third release blocker is a TOCTOU: authorization resolves the target with
`filepath.EvalSymlinks` and checks containment, and the operation is then performed on that
*presented path*, which the kernel resolves again at syscall time. A local process that can
replace a component of the path in between redirects the operation.

The remedy is `openat`-relative access throughout. Go 1.26 has `os.Root` for exactly this, and
it was probed on the development machine before being rejected for *this* change:

| probe | result |
| --- | --- |
| `Root.Open`/`OpenFile`/`Rename`/`Remove`/`RemoveAll`/`Mkdir`/`ReadDir`/`Chmod` on darwin | all work |
| symlink whose target leaves the root | refused: `path escapes from parent` |
| **absolute** symlink whose target stays **inside** the root | **also refused** |
| error identity | a bare `errors.errorString` inside `*fs.PathError`; **no exported sentinel** |
| `Root.Lstat` on a dangling link | works (does not follow) |
| `Root.Stat` on a dangling link with an absolute target | refused (the `Stat` path follows) |

Two consequences, and together they are why this is not the change for it:

- **The absolute-symlink restriction would regress a capability this same review asks to
  fix.** Finding #6 is that a symlink to a directory cannot be browsed. `os.Root` refuses
  *any* absolute symlink, including one that points inside the root, so a design that
  canonicalizes first and then hands the canonical relative path to the Root is required
  rather than optional — and the paths that survive canonicalization as symlinks are exactly
  the dangling ones, whose `Stat` through the Root then reports "escapes" where today it
  reports not-found. Getting the 404s right again means mapping an unexported, unstable Go
  error string, the way `tooManyLinks` already does for `EvalSymlinks` — a second such
  dependency, in the same file, on the day of a release-blocker fix.
- **The residual window is narrow, and the fix is not free.** It needs a concurrent local
  actor with write permission on a directory the hub traverses, plus the ability to create
  symlinks there. It is not reachable through the API, and it matters where the hub runs as a
  more privileged account than those writers — a real but narrow case, not the case `roots`
  is advertised for.

So this change makes the claim match the code, in the spec and in both READMEs, and records
the migration with the probe above as the immediately following piece of work. The alternative
— landing an `openat` migration in the same change as nine behaviour fixes — is how the
regression the previous review round found (dangling symlinks becoming undeletable) happened.

*Alternatives considered:* implementing `os.Root` now (rejected above); leaving the docs alone
because the design doc records the limit (rejected — the design doc is not what an operator
reads, and the review's point is precisely that the product's claim is stronger than the
design's admission).

### 2. Writing is bound to the entry it started against, at every precision available

Two separate holes produced one symptom — a save reported as stored somewhere it was not.

*Hub side.* `confirmUnchanged` returned early for a forced write, so the whole upload was a
window in which a rename could land. The commit check is now unconditional and asks two
questions: is the target still the same file (`os.SameFile`, which compares device and inode
and is portable), and — when the caller supplied a time — is that time still current. A write
that began against a free name now also requires the name to still be free, closing the same
hole from the other side: a create landing on a file created meanwhile would replace something
its author never agreed to lose.

The free-name branch asks with `Lstat` rather than `Stat`, which review caught: a
name taken by a link whose target is gone is taken. `Stat` follows the link,
finds nothing where it points, and reports the name as free -- so the write would
replace the link, which is precisely the shape the check at the start of `Write`
exists to refuse.

The origin is captured by the *same* `os.Stat` that the pre-transfer check already performs,
so this adds one `stat` at commit and no new failure mode when the target never existed.

*Frontend side.* The overlay folded a save answer in by tab id alone. A rename completing
during the flight retargets the tab to the new path — creating a *new* tab object — so the
captured reference agreed with itself and `applySaved` was applied to the tab at the new path.
`settleSave` now takes the path the write named and refuses to record the answer against a tab
that no longer holds it, returning `moved` so the caller can say so. It is a plain function in
`files/tabs.ts` precisely so the interleaving can be tested without a DOM.

*Alternatives considered:* folding the answer in and letting the next save conflict (rejected:
it reports the edit as stored where nothing was written, which is the finding); re-saving
automatically to the new path (rejected: that writes to a name the user has not agreed to
overwrite, and the rename may have been of a directory).

*Precision.* Both comparisons used `UnixMilli`, so two edits inside one millisecond looked like
none. Milliseconds cannot become nanoseconds on the wire — `Number.MAX_SAFE_INTEGER` is about
9.007e15 and nanoseconds since the epoch are about 1.7e18 — so the exact value travels as an
opaque decimal string beside the millisecond number, and is compared only when present. A
hub that sends only milliseconds still works, with the weaker comparison; so does a client
that sends only milliseconds.

### 3. The whole file decides whether it is text, and only regular files can be read

A prefix is a guess of exactly the kind the classification exists to avoid. The scan is
streaming (`textScan`) rather than a bigger sample, because the interesting file — a log that
is text for megabytes and then binary — is the one a sample of any fixed size gets wrong, and
the replacement characters written back over it are not recoverable. Cost is bounded: the scan
stops at the first decisive byte, so a binary file is refuted as early as before, and only a
file that stays text to its end is read to its end. That is the file whose answer has to be
right before it is offered as editable.

A chunk boundary can fall inside a rune, and neither chunk is valid UTF-8 on its own, so the
scanner carries the undecided tail across chunks. Bytes still held back at the end of the file
were cut in half by the end of the file, which no encoding produces, so they are binary.

`O_NONBLOCK` plus a descriptor check replaces "not a directory". `open(2)` on a named pipe
with no writer blocks until one appears, and no context interrupts it, so a path that is a
pipe would hold a request goroutine indefinitely; the flag makes the open return immediately
for every kind of file, and for a regular file it changes nothing about reading one.
*Alternatives considered:* `Lstat` before opening (rejected: a check on the path rather than on
what was opened, which is the TOCTOU of decision 1 in miniature).

*The cost is paid by the download route too*, where the classification is
discarded: `download` streams bytes to disk and never consults `X-File-Binary`,
so a large text file is read twice to serve one. Accepted rather than fixed,
because the alternative is a second read path or a parameter that exists only to
say "do not classify", and the bound is `files.max_file_size` with the second
pass usually served from page cache. Recorded here so it is a decision rather
than an oversight; it was raised by review.

### 3a. The served length is bounded in both directions

The first version of this fix replaced ServeContent's own measurement with a
`SectionReader` bounded by the size the service authorized. That closes the
growth direction and opens the opposite one: a file truncated after the check was
then served with `Content-Length` promising the authorized length and a body
carrying fewer bytes. `ServeContent` sets the header from the section and copies
with an error it discards, so the result is a response contradicting its own
framing -- which the browser reports as a transport failure, and which no client
can do better with. The old code was self-consistent in that direction
(`Content-Length: 0` for a file truncated to empty) and wrong only about
`X-File-Size`. Review found this; it is the shape of regression a fix invites
when it is reasoned about in one direction.

So the section is bounded by the smaller of the authorized length and what the
descriptor holds *now*, and `X-File-Size` reports that same number: never more
than was checked, never more than is there. The modification time stays the one
the service observed even where the body is shorter, because it is the token the
next save is checked against -- handing the client the time of a change it never
saw would let it overwrite that change with no prompt.

The window is narrowed, not closed: a file truncated *during* the copy still
under-delivers, which is inherent to putting a length in a header and then
streaming a file. That direction is detectable by the client, and what it
produces is a failed transfer rather than wrong data.

### 4. A listing's cost is bounded by the listing bound

`os.ReadDir` reads and sorts the whole directory before its caller can apply any bound, so a
directory of a million entries cost a million entries' worth of memory to produce a thousand
entries. `readListing` reads `maxDirEntries + 1` entries — one past the bound is all it takes
to know there is more — in batches, checking `ctx` between them.

The honest consequence is recorded in the spec rather than hidden: a truncated listing is
whatever the directory happened to hold first, not the alphabetically first entries. The
ordering the API promises (directories first, then by name) is now imposed by an explicit sort
because reading in batches yields directory order, which is no order a caller can use.

Links are resolved through the guard rather than with a bare `stat`, so a link that leaves the
boundary is described as the link it is instead of disclosing the target's size and time.

*Alternatives considered:* keeping the full read and only checking `ctx` (rejected: the memory
peak is the finding); reading `maxDirEntries × k` to make the truncated subset more useful
(rejected: no principled `k`, and the subset is arbitrary however large it is).

### 5. Frontend: logic in modules, editors mounted, no new test dependency

The overlay's editor was keyed on the active path and rendered one at a time, so switching
tabs destroyed the instance and its undo history — against the comment in `CodeEditor.vue`
that says one instance per open file. Every editable tab now gets its own mounted editor and
`v-show` picks the visible one, keyed by tab id so a rename keeps the instance. `CodeEditor`
gains an `active` prop and re-measures when it becomes visible, because an editor laid out
while hidden has no dimensions.

Everything that could be tested is in `files/tabs.ts` — `settleSave`, `editable`,
`presentation` — and is covered by the existing `node --test` modules. The remaining gap is
unchanged and unchanged *deliberately*: there is still no DOM test harness, so CodeMirror's
mount/unmount and real DOMPurify sanitization are not behaviourally tested. Adding `jsdom` and
`@vue/test-utils` changes the test environment for the whole project, and mocked CodeMirror
component tests mostly exercise the mock; that is a decision to take on its own, not as a
side-effect of a bug-fix branch. It is listed as a follow-up.

## Risks / Trade-offs

- **[Risk] The commit-time identity check adds a refusal that did not exist**, so a write that
  used to succeed now conflicts: a file replaced between the read and the save, a name created
  between the check and the commit. → *Accepted.* Both are situations where the write would
  have destroyed something its author did not agree to lose, and both surface as a conflict
  the user can confirm, which is the flow the feature already has.
- **[Risk] Reading the whole file to classify it makes a large text file cost a second pass.**
  → *Bounded by `files.max_file_size` (100 MiB default) and by the early exit, and the pass
  reads from page cache in the common case where the file was just opened. The alternative is
  a classification that is wrong exactly where being wrong destroys data.*
- **[Risk] A truncated listing is no longer the alphabetically-first entries**, so a file that
  a user could previously find by scrolling may not appear at all. → *Stated in the spec and
  both READMEs. The listing was already truncated and already said so; what changes is which
  entries are missing.*
- **[Risk] The `expected_mtime_nanos` value is compared as an exact string-to-int round trip.**
  → *It is produced by `UnixNano()` on the same inode within the same read, and adopted
  verbatim by the client, so a mismatch means the file changed. A filesystem with coarser
  granularity reports a stable value and compares equal to itself.*
- **[Known wart, accepted] A cancelled or unclassified read reports the catch-all
  wire code**, which is named `write_failed` because the write path was the first
  thing to need a fallback. A request whose client has gone away is answered to
  nobody, so what is lost is a log line that names the wrong operation; an
  unclassified I/O error on a read is answered with the same code. Renaming it
  would change a published code and the browser's message table, and the browser
  prefixes every refusal with the action it was attempting ("Could not open X"),
  which is where the operation is legible today. Two reviewers raised it as an
  out-of-scope observation; recorded here rather than left as an oversight.
- **[Trade-off] The TOCTOU stays open**, with its scope now written down. → *Recorded in the
  spec as a limit, in both READMEs, and as the follow-up in decision 1.*

## Migration Plan

Additive on the wire: `expected_mtime_nanos`, `X-File-Mtime-Nanos`, and `mtime_nanos` are
optional in both directions, and each side falls back to milliseconds. No configuration key
changes, no persisted state is introduced, and no file is modified by the hub outside an
explicit user action. Rolling back is reverting the release.

## Open Questions

- Whether to migrate the guard to `os.Root` now that the limit is stated plainly. The probe in
  decision 1 is the starting point; the work is a change of its own, and it needs the symlink
  and dangling-link behaviours re-verified against the cases the current tests already cover.
- Whether the frontend should gain a DOM test environment. Deferred; see decision 5.
