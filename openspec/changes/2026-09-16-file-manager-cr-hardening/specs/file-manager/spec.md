## MODIFIED Requirements

### Requirement: Filesystem access boundary

By default the hub SHALL allow file operations on any regular path its own operating-system user can access, mirroring the access that user already has through an attached terminal. When an operator configures a set of root directories, the hub SHALL confine every operation to those roots instead. The hub SHALL authorize each operation against the canonical, symlink-resolved form of the target path, not the caller-supplied lexical form, so that a symbolic link cannot be used to reach a target outside a root. The hub SHALL reject `/proc`, `/sys`, and `/dev` before applying any root check, so that no configuration can expose them. A target that does not yet exist SHALL be validated by resolving the canonical form of its nearest existing ancestor and re-appending the remaining segments. A caller SHALL supply absolute, platform-native paths of at most 4096 characters.

Roots govern what a **caller** may name, and that is the whole of what they guarantee. The hub SHALL NOT be described as containing a caller against another process on the same machine: authorization is decided against the target's resolved path and the operation is then performed on that path, which the kernel resolves again when the call is made, so a local process that can replace a component of the path in between has the operation follow the replacement. Closing that requires descriptor-relative access throughout — `openat`-relative walks, or an equivalent rooted handle — which this hub does not implement. This is a recorded limit of the implementation, stated so that the boundary is not credited with more than it does; it is not reachable through the API, and it is distinct from the guarantee below, which is about a caller who holds the token and nothing else.

#### Scenario: Default boundary

- **WHEN** no root directories are configured and the hub's operating-system user can access a regular path outside the user's home directory
- **THEN** the hub performs the operation on that path

#### Scenario: A caller names a path that resolves outside every root

- **WHEN** roots are configured and a caller names a path whose resolved form lies outside all of them
- **THEN** the hub refuses the operation with a not-allowed error, which is the guarantee roots do make

#### Scenario: Another local process replaces a path component

- **WHEN** a local process replaces a directory on an authorized path with a symbolic link before the operation is performed
- **THEN** the operation follows the replacement, and this is a recorded limit rather than a guarantee that the boundary held

#### Scenario: Target inside a configured root

- **WHEN** roots are configured and a caller operates on a path contained in one of them
- **THEN** the hub performs the operation

#### Scenario: Target outside configured roots

- **WHEN** roots are configured and a caller operates on a regular path outside every one of them
- **THEN** the hub refuses the operation with a not-allowed error

#### Scenario: Symlink escapes a root

- **WHEN** roots are configured and a path lexically inside a root is a symbolic link, or traverses one, whose resolved target lies outside every configured root
- **THEN** the hub refuses the operation with a not-allowed error and does not touch the resolved target

#### Scenario: Lexical traversal

- **WHEN** a caller supplies a path containing a `..` segment, a path that is not absolute, or a path longer than 4096 characters
- **THEN** the hub refuses the operation with an invalid-path error

#### Scenario: Pseudo-filesystem target

- **WHEN** a caller targets a path under `/proc`, `/sys`, or `/dev`
- **THEN** the hub refuses the operation with a not-allowed error regardless of the configured roots

#### Scenario: Creating a file that does not exist

- **WHEN** a caller writes or creates a path whose final segment does not exist but whose parent lies inside a configured root
- **THEN** the hub validates the parent's canonical path and performs the operation

#### Scenario: Escaping parent through a symlink

- **WHEN** roots are configured and a caller writes a new file whose parent directory is a symbolic link pointing outside every configured root
- **THEN** the hub refuses the operation rather than creating the file at the resolved parent

### Requirement: Directory listing

The hub SHALL return the immediate children of a directory, each carrying at least its name, whether it is a directory, its size, and its modification time. A child that is a symbolic link SHALL be described by what it points at, within the boundary: a link to a directory SHALL be reported as a directory, so the browser offers it for expanding rather than for opening. A link whose target lies outside every configured root SHALL NOT be described by that target, so a listing never answers with the size or modification time of a path the caller may not name. Listing SHALL order directories before files, and each group by name. Listing SHALL be bounded by the configured maximum number of entries and SHALL report whether the result was truncated. Listing a directory with no children SHALL succeed and return an empty result, not an error.

Reading SHALL be bounded by the same configured maximum, so that the cost of listing a directory is the cost of the listing and not the cost of the directory. A directory holding more entries than that bound SHALL be reported as truncated, and which of its entries appear in a truncated listing is not specified. The hub SHALL abandon a listing when the caller's request is cancelled.

#### Scenario: Directory with contents

- **WHEN** a caller lists a directory containing both files and subdirectories
- **THEN** the hub returns one entry per child with its name, directory flag, size, and modification time, with subdirectories ordered before files

#### Scenario: Empty directory

- **WHEN** a caller lists a directory with no children
- **THEN** the hub returns an empty list and reports success

#### Scenario: Directory exceeds the listing bound

- **WHEN** a directory holds more children than the configured maximum
- **THEN** the hub returns at most that many entries and reports the result as truncated

#### Scenario: Directory far exceeds the listing bound

- **WHEN** a caller lists a directory holding hundreds of thousands of children
- **THEN** the hub reads no more of it than the bound requires, so the memory and time the listing costs do not grow with the directory

#### Scenario: A symbolic link to a directory

- **WHEN** a caller lists a directory containing a symbolic link that points at a directory
- **THEN** that entry is reported as a directory

#### Scenario: A symbolic link that leaves the boundary

- **WHEN** roots are configured and a listed directory contains a symbolic link whose target lies outside every root
- **THEN** the entry is not described by its target, and no size or modification time of it is disclosed

#### Scenario: Target is not a directory

- **WHEN** a caller lists a path that is a regular file, or that does not exist
- **THEN** the hub refuses the operation with an error distinguishing the two cases from an empty directory

### Requirement: Bounded file reading

The hub SHALL stream a file's contents to the caller without loading the whole file into memory, and SHALL report the file's byte size and modification time alongside the contents. The hub SHALL refuse to read a file whose size exceeds the configured per-file limit. The hub SHALL allow the caller to distinguish binary content from text content so the browser does not render binary bytes as text. The hub SHALL read only regular files: a directory, a named pipe, a socket, a device, and any other kind SHALL be refused, and the hub SHALL NOT wait on a file that would block the request. The bytes served SHALL be exactly the bytes whose size was checked against the limit and reported to the caller, so that a file which changes size while it is being served is neither sent beyond the limit nor described by metadata that disagrees with its body. The classification of a file's contents as binary or text SHALL be decided by the whole of its contents, not by a prefix of them.

#### Scenario: Read a text file

- **WHEN** a caller reads a file within the size limit
- **THEN** the hub streams the exact bytes and reports the file's size and modification time

#### Scenario: Read exceeds the size limit

- **WHEN** a caller reads a file larger than the configured per-file limit
- **THEN** the hub refuses with a too-large error and transfers no file content

#### Scenario: A file that grows while it is served

- **WHEN** a file grows after its size has been checked and read
- **THEN** the hub serves exactly the bytes that were checked, and the size it reports describes the body it sent

#### Scenario: Read a missing file

- **WHEN** a caller reads a path that does not exist
- **THEN** the hub returns a not-found error

#### Scenario: Read a directory as a file

- **WHEN** a caller reads a path that is a directory
- **THEN** the hub refuses the operation rather than returning directory contents

#### Scenario: Read a named pipe

- **WHEN** a caller reads a path that is a named pipe with no writer
- **THEN** the hub refuses it rather than waiting, and no request is left open on it

#### Scenario: Binary content

- **WHEN** a caller reads a file whose contents are not valid UTF-8 text
- **THEN** the hub does not present it as editable text and the browser does not render its bytes as text

#### Scenario: A file that turns binary after its opening bytes

- **WHEN** a caller reads a file whose opening bytes are text and whose later bytes are not
- **THEN** the hub reports the contents as binary, because decoding them as text is what would replace those bytes on the next save

### Requirement: Optimistic concurrent writes

A write SHALL carry the modification time the caller last observed for the target. The hub SHALL refuse the write with a conflict error when that value does not match the file's current modification time. A caller MAY omit the observed modification time to force an overwrite. The hub SHALL write through a temporary file created in the target's own directory and then atomically replace the target, so that a failed, interrupted, or oversized write never leaves a partially written or truncated file at the target path. The hub SHALL remove the temporary file when a write fails. A write whose received body length differs from the declared length SHALL be refused. When a caller supplies an observed modification time for a file that does not exist, the hub SHALL report not-found rather than creating it.

Immediately before replacing the target the hub SHALL re-check that the target is still the entry the write began against, and SHALL refuse with a conflict error if it is not. This check SHALL apply to a forced overwrite too: omitting the observed time is agreement to replace the file that was there, not agreement to recreate a file that has since been moved or removed, nor to replace a different file that has taken the name. A write that began against a name which did not exist SHALL likewise be refused if that name has been taken by the time the body has arrived.

Modification times SHALL be conveyed as integer milliseconds since the Unix epoch, which a JSON client can hold exactly. When the filesystem records finer precision than that, the hub SHALL convey the exact modification time alongside it as an opaque value the caller carries back unchanged, and SHALL compare against that exact value when the caller supplies one, so that two changes inside a single millisecond are two changes rather than one. A caller that supplies only milliseconds SHALL be compared against milliseconds. A successful write SHALL return the target's resulting modification time at both precisions, so the caller can continue editing without re-reading the file.

#### Scenario: Write with a matching modification time

- **WHEN** a caller writes a file supplying the modification time it last observed, and the file is unchanged
- **THEN** the hub replaces the file's contents, returns the resulting modification time, and the caller can read back exactly the bytes written

#### Scenario: Write after an external change

- **WHEN** a caller writes a file supplying a modification time that no longer matches because the file changed since it was read
- **THEN** the hub refuses with a conflict error, leaves the file's current contents intact, and transfers no partial content

#### Scenario: Two changes inside one millisecond

- **WHEN** a file is changed twice inside the same millisecond and a caller writes it with the observation it took before the first of them
- **THEN** the hub refuses with a conflict error, rather than treating the two changes as none

#### Scenario: Consecutive saves

- **WHEN** a caller saves a file and then saves it again using the modification time returned by the first save
- **THEN** the second save succeeds without a spurious conflict

#### Scenario: Forced overwrite

- **WHEN** a caller writes a file omitting the observed modification time and the file it began against is still there
- **THEN** the hub replaces it regardless of its current modification time

#### Scenario: Forced overwrite of a target that has moved

- **WHEN** a caller writes a file omitting the observed modification time, and the target is renamed or removed while the body is being transferred
- **THEN** the hub refuses the write and creates no file at the original path

#### Scenario: A name taken while the write was in flight

- **WHEN** a write begins against a name that does not exist and something else creates that name before the body has arrived
- **THEN** the hub refuses with a conflict error and leaves the created file unmodified

#### Scenario: Write fails midway

- **WHEN** a write fails or is interrupted after it has begun
- **THEN** the target file retains either its previous contents or the fully written new contents, never a partial write, and no temporary file is left behind

#### Scenario: Body length mismatch

- **WHEN** the received body length differs from the length the caller declared
- **THEN** the hub refuses the write with an invalid-body error and leaves the target unchanged

#### Scenario: Write with an observed modification time to a missing file

- **WHEN** a caller supplies an observed modification time for a path that does not exist
- **THEN** the hub reports not-found and does not create the file

#### Scenario: Write exceeds the size limit

- **WHEN** a caller writes a body larger than the configured per-file limit
- **THEN** the hub refuses with a too-large error and leaves the target unchanged

### Requirement: Session working directory

The hub SHALL expose the working directory of a session's active pane, so the browser can open the file manager at the directory the user is currently working in. The directory SHALL be reported exactly as the pane holds it, since leading and trailing whitespace is part of a name rather than formatting around it: a trimmed name denotes a different directory, or none at all.

#### Scenario: Session has an active pane

- **WHEN** a caller requests the working directory of an existing session
- **THEN** the hub returns the absolute working directory of that session's active pane

#### Scenario: A directory whose name ends with a space

- **WHEN** a caller requests the working directory of a session whose active pane sits in a directory whose name ends with a space
- **THEN** the hub returns that name with the trailing space intact

#### Scenario: Session is absent

- **WHEN** a caller requests the working directory of a session that does not exist
- **THEN** the hub returns a not-found error

### Requirement: Editing and unsaved changes

The browser SHALL allow multiple files to be open at once, each in its own tab, and SHALL mark a file as modified while its in-memory contents differ from what was last read or saved. The browser SHALL surface that unsaved changes exist outside the editor, so that closing the manager or the terminal can warn before discarding them. Closing a modified tab SHALL require confirmation. When a save is refused because the file changed externally, the browser SHALL tell the user and offer to overwrite.

Each open file's editor state, including its undo history, SHALL be retained for as long as its tab is open: viewing another file SHALL NOT discard it, because the undo stack a user reaches for is usually the one belonging to the file they just switched away from.

A save whose answer arrives after the file has been renamed SHALL NOT be recorded against the tab at its new path. The browser SHALL report that the file moved and leave the edit unsaved, so that the contents are written to the name the tab holds by a further save, rather than being reported as stored at a name where nothing was written.

#### Scenario: Modify and save

- **WHEN** the user edits an open file and saves
- **THEN** the browser writes the current contents and the file becomes unmodified

#### Scenario: Switch between open files

- **WHEN** the user switches tabs while one file has unsaved edits
- **THEN** the edits are retained and the file remains marked as modified

#### Scenario: Switch away and back

- **WHEN** the user edits one file, switches to another, and switches back
- **THEN** the first file's editor holds the undo history it had, rather than a fresh one

#### Scenario: A rename completes during a save

- **WHEN** a file is renamed after a save of it has been sent and before its answer arrives
- **THEN** the browser reports that the file moved, does not mark the tab saved at the new name, and leaves the edit unsaved

#### Scenario: Close a modified tab

- **WHEN** the user closes a tab whose file has unsaved edits
- **THEN** the browser asks for confirmation before discarding them

#### Scenario: Save conflict

- **WHEN** a save is refused because the file changed on disk since it was read
- **THEN** the browser reports the conflict and offers to overwrite

#### Scenario: Overwrite after conflict

- **WHEN** the user accepts the overwrite offered after a conflict
- **THEN** the browser saves without an observed modification time and the file contains the user's contents

#### Scenario: Discard on close

- **WHEN** the user closes the manager while files have unsaved edits
- **THEN** the browser warns before discarding them
