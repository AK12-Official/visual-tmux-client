## MODIFIED Requirements

### Requirement: Directory listing

The hub SHALL return the immediate children of a directory, each carrying at least its name, whether it is a directory, its size, and its modification time. A child that is a symbolic link SHALL be described by what it points at, within the boundary: a link to a directory SHALL be reported as a directory, so the browser offers it for expanding rather than for opening. A link whose target lies outside every configured root SHALL NOT be described by that target, so a listing never answers with the size or modification time of a path the caller may not name. An entry that cannot be described at all SHALL be reported as present without a size or modification time, rather than failing the listing or reporting the link's own size in place of a target's; a link whose target is missing is one of those. Listing SHALL order directories before files, and each group by name. Listing SHALL be bounded by the configured maximum number of entries and SHALL report whether the result was truncated. Listing a directory with no children SHALL succeed and return an empty result, not an error.

For local directories, reading SHALL be bounded by the same configured maximum, plus the entries the hub is itself holding back from the listing -- the staging files of writes in flight, of which there are as many as there are concurrent writes. The cost of listing a directory is therefore the cost of the listing rather than the cost of the directory. A directory holding more entries than that bound SHALL be reported as truncated, and which of its entries appear in a truncated listing is not specified. The hub SHALL abandon a listing when the caller's request is cancelled.

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

#### Scenario: A symbolic link whose target is missing

- **WHEN** a caller lists a directory containing a symbolic link whose target does not exist
- **THEN** the entry is reported as present, without a size or modification time

#### Scenario: Target is not a directory

- **WHEN** a caller lists a path that is a regular file, or that does not exist
- **THEN** the hub refuses the operation with an error distinguishing the two cases from an empty directory

For remote directories, the same result limit, ordering and truncation indication SHALL apply; a bounded SFTP response batch of lookahead is permitted as specified under Bounded remote transfer and capability reporting.

### Requirement: Filesystem access boundary

The following filesystem-handle, identity and local metadata guarantees apply to the built-in local host and remain unchanged there. Remote SFTP operations follow Remote file access boundary and Remote file replacement semantics instead.

By default the hub SHALL allow file operations on any regular path its own operating-system user can access, mirroring the access that user already has through an attached terminal. When an operator configures a set of root directories, the hub SHALL confine every caller-supplied operation to those roots instead. The hub SHALL authorize each operation against the canonical, symlink-resolved form of the target path, not the caller-supplied lexical form, so that a symbolic link cannot be used to reach a target outside a root. The hub SHALL reject `/proc`, `/sys`, and `/dev` before applying any root check, so that no configuration can expose them. A target that does not yet exist SHALL be validated by resolving the canonical form of its nearest existing ancestor and re-appending the remaining segments. A caller SHALL supply absolute, platform-native paths of at most 4096 characters.

Roots govern what a **caller** may name, and no path supplied through this API reaches outside them: each operation is authorized against the target's resolved form, and one that resolves outside every root is refused.

When roots are configured the hub SHALL also *perform* each operation through an open handle on the containing root, with the path expressed relative to it, so that the components of the target are resolved by the call that performs the operation rather than by an earlier check. This is what makes the boundary hold against another process on the machine and not merely against the caller: a component replaced between the check and the operation by a symbolic link that leads outside the root SHALL cause the operation to be refused, rather than silently redirecting it. A replacement that leads back inside the same root is followed, which stays within the boundary -- what the handle refuses is leaving it, not being redirected. Moving an entry between two configured roots SHALL be refused: a handle moves only within its own tree, so such a move could only be performed on the two absolute paths, where a component replaced in between carries the entry out of the boundary in one direction or into it in the other. A destination typed into the browser is built from the source's own directory, so this is reached only by descending through a symbolic link into another configured root; it is reported as a refusal rather than attempted, and the move the user asked for is one that crosses the operator's boundary in the first place.

With no roots configured there is no boundary to keep, and none is claimed: the hub acts on plain paths, where a replaced component takes the operation wherever it points. That is the operating-system user's own access, which an attached terminal already grants.

Roots are therefore a boundary against a caller, and against a replaced path component for every operation the hub performs. They are not a boundary against the hub's own user, who has the terminal, or against a file being changed while it is read: that is a different problem, specified under Bounded file reading.

#### Scenario: Default boundary

- **WHEN** no root directories are configured and the hub's operating-system user can access a regular path outside the user's home directory
- **THEN** the hub performs the operation on that path

#### Scenario: A caller names a path that resolves outside every root

- **WHEN** roots are configured and a caller supplies a path whose resolved form lies outside all of them
- **THEN** the hub refuses the operation with a not-allowed error, which is the guarantee roots do make

#### Scenario: Another local process replaces a path component

- **WHEN** a local process replaces a directory on an authorized path, after the target has been authorized and before the operation is performed, with a symbolic link that leads outside the root
- **THEN** the hub refuses the operation rather than following the replacement out of the boundary

#### Scenario: A replacement that stays inside the root

- **WHEN** a local process replaces a directory on an authorized path with a symbolic link whose target is inside that same root
- **THEN** the operation follows the replacement, because it has not left the boundary

#### Scenario: A move between two configured roots

- **WHEN** a caller moves an entry from one configured root to another
- **THEN** the hub refuses the move with a cross-root error and leaves the entry at its original path, because a handle moves only within its own tree and no safe spelling of the move exists without one

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

### Requirement: Optimistic concurrent writes

The following filesystem-handle, identity and local metadata guarantees apply to the built-in local host and remain unchanged there. Remote SFTP operations follow Remote file access boundary and Remote file replacement semantics instead.

A write SHALL carry the modification time the caller last observed for the target. The hub SHALL refuse the write with a conflict error when that value does not match the file's current modification time. A caller MAY omit the observed modification time to force an overwrite. The hub SHALL write through a temporary file created in the target's own directory and then atomically replace the target, so that a failed, interrupted, or oversized write never leaves a partially written or truncated file at the target path. The hub SHALL remove the temporary file when a write fails. A write whose received body length differs from the declared length SHALL be refused. When a caller supplies an observed modification time for a file that does not exist, the hub SHALL report not-found rather than creating it.

Immediately before replacing the target the hub SHALL re-check that the target is still the entry the write began against, and SHALL refuse with a conflict error if it is not. For an existing target the hub SHALL keep a metadata-only descriptor open for the duration of the write, so removing the entry cannot free and reuse its inode while the identity comparison is outstanding. That re-check and the replacement are two adjacent system calls rather than one indivisible step, because no filesystem primitive replaces a name only if it still holds the file that was there; an entry moved or taken in that interval is therefore acted on rather than detected. What the browser does when it loses that race is specified under Editing and unsaved changes. This check SHALL apply to a forced overwrite too: omitting the observed time is agreement to replace the file that was there, not agreement to recreate a file that has since been moved or removed, nor to replace a different file that has taken the name. A write that began against a name which did not exist SHALL likewise be refused if that name has been taken by the time the body has arrived.

Modification times SHALL be conveyed as integer milliseconds since the Unix epoch, which a JSON client can hold exactly. When the filesystem records finer precision than that, the hub SHALL convey the exact modification time alongside it as an opaque value the caller carries back unchanged, and SHALL compare against that exact value when the caller supplies one, so that two changes inside a single millisecond are two changes rather than one. A caller that supplies only milliseconds SHALL be compared against milliseconds. A successful write SHALL return the target's resulting modification time at both precisions, so the caller can continue editing without re-reading the file.

A replacement replaces the file rather than writing through it, and that has costs the specification
states where it states the requirement. What is carried over is the target's permission bits and, where
the hub may set it, its owner. What is not is everything else the file carried that belongs to the inode
it lived in: a replaced file has the empty set of access control entries and extended attributes that a
new file has. And a target reachable under more than one name SHALL be refused unless the caller has
agreed to the replacement, because the entry the caller named is the one that is written while every
other name keeps the contents it had -- which is a thing to be told, not to discover.

Two rules bound what a replacement may meet and what it keeps. A target that is not a regular file SHALL be
refused, exactly as the read path refuses one: the staged file is a regular file, so replacing a named pipe,
a socket or a device with it would *remove* the node rather than write it, and a caller asking for contents
to be written has not agreed to that. And what the replacement keeps is the target's metadata as it
stands once the body has arrived, not as it stood when the upload began: a mode or an owner changed while
the body was being transferred touches the ctime and not the modification time, so nothing else in the
write would notice it, and applying what was captured at the start would undo a change that was made for
safety. A change made in the tail between that reading and the replacement is not carried, which is what
keeps the reading adjacent to the metadata work and the last identity check adjacent to the replacement.

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

#### Scenario: A target reachable under other names

- **WHEN** a caller writes a file that is reachable under more than one name, without having said it knows what a replacement means for the others
- **THEN** the hub refuses with a distinct error and leaves every name as it was

#### Scenario: A target whose other names the caller agreed about

- **WHEN** the caller writes that file having agreed
- **THEN** the entry it named holds the new contents, and every other name still holds the contents it had

#### Scenario: A name linked to the target while the body travelled

- **WHEN** another name is linked to the target after the write began and before it is committed
- **THEN** the hub refuses the write and leaves the file as it was

#### Scenario: A replacement keeps what can be kept

- **WHEN** a caller writes an existing file that has one name
- **THEN** the replacement carries its permission bits, and its owner where the hub is allowed to set it

#### Scenario: A target that is not a regular file

- **WHEN** a caller writes a path that holds a named pipe, a socket, or a device
- **THEN** the hub refuses and leaves the node as it was

#### Scenario: The target's mode changed while the body travelled

- **WHEN** the target's permissions are changed while the body is being transferred
- **THEN** the replacement carries the permissions the target has when the body has arrived

## ADDED Requirements

### Requirement: Host-scoped file workspace

All file operations and working-directory queries SHALL target an explicit host. Remote hosts with SFTP SHALL support directory browsing, bounded reading, preview, editing, download, creation, rename and deletion, subject to reported capabilities. Existing preview safety, text classification, size limits and unsaved-edit behavior SHALL remain applicable. Tabs, pending operations and directory state SHALL be bound to host, revision and path. Path insertion SHALL target a live terminal on the same host only. A global file disablement SHALL refuse local and remote file operations before filesystem or SFTP access.

#### Scenario: Same path on two hosts
- **WHEN** files at /home/user/notes exist on A and B and the user saves the tab belonging to A while viewing B
- **THEN** the write targets A with its original revision and does not modify B

#### Scenario: Path insertion across hosts
- **WHEN** the file belongs to A but the only active terminal belongs to B
- **THEN** insertion is refused with an explanation rather than sending A's path to B

#### Scenario: Files globally disabled
- **WHEN** file operations are globally disabled and a remote file request arrives
- **THEN** it is refused without opening SFTP or querying a remote working directory

#### Scenario: Remote working directory differs from SFTP namespace
- **WHEN** the tmux directory cannot be mapped to the SFTP namespace
- **THEN** the browser reports the mismatch and permits explicit directory selection without inventing a translation

### Requirement: Remote file access boundary

Remote file access SHALL use the configured SSH account's server-side permissions. Local configured roots SHALL NOT be interpreted as remote roots. The hub SHALL identify this remote boundary and SHALL NOT claim the local root-handle confinement guarantee over SFTP. Remote paths SHALL be absolute POSIX paths subject to existing length and traversal rejection. Lexical and resolved paths under /proc, /sys and /dev SHALL be refused; this check SHALL NOT be represented as protection from concurrent server-side path replacement. Non-regular file contents SHALL be refused, and stalled operations SHALL be cancelled within finite budgets.

#### Scenario: Local roots and remote path
- **WHEN** local roots restrict local files to /srv/projects and a remote account can read /home/user/notes
- **THEN** remote access is evaluated on the remote server and never against the Hub's local /srv/projects

#### Scenario: Remote pseudo-filesystem
- **WHEN** a caller names /proc or a path resolved to /proc on a remote host
- **THEN** the file operation is refused

### Requirement: Bounded remote transfer and capability reporting

The hub SHALL enforce file byte and directory entry limits during remote work, not after collecting unbounded results. Remote capabilities SHALL report SFTP availability, overwrite support and consistency limitations to authenticated clients. Unsupported overwrite SHALL NOT disable browsing and downloading. A bounded protocol response batch beyond the listing limit is permitted, but reading the whole directory before truncating is not. Remote reads SHALL present a stable bounded body and a revision tied to its content.

#### Scenario: Very large remote directory
- **WHEN** a remote directory contains far more entries than the listing limit
- **THEN** the hub stops reading after the limit and bounded protocol lookahead and reports truncation

#### Scenario: Replacement unsupported
- **WHEN** the SFTP server does not support verified atomic replacement
- **THEN** the editor explains why overwrite is unavailable and browse/download remain usable

### Requirement: Remote file replacement semantics

Remote saves SHALL stage complete bounded content in the destination directory and replace only after validating body length and supported atomic replacement. The hub SHALL NOT fall back to deleting the target first or truncating it in place. Normal saves SHALL compare a content-derived revision and available metadata with the observed file before commit, detecting changed bytes even when size and reported timestamp are unchanged. Forced saves SHALL still recheck target existence and regular-file type. Local inode identity and compare-and-swap guarantees SHALL NOT be claimed: remote processes can change a target after the final check, and an indistinguishable replacement cannot be detected reliably.

The hub SHALL serialize its own conflicting writes, renames and deletes. Replacement SHALL preserve current permission bits and preserve owner/group where permitted; preservation failures SHALL be reported. The editor SHALL explain that replacement does not preserve ACLs/xattrs or update other hard-link names. If link count cannot be determined, replacement SHALL require acknowledgement of that limitation. A lost commit acknowledgement SHALL leave the edit unsaved with an outcome-unknown message pending reconciliation. Failed staging cleanup SHALL be recorded for a later safe attempt; cleanup SHALL target only identifiable Hub-owned staging files. Success SHALL NOT imply remote power-loss durability when that has not been established.

#### Scenario: Same timestamp but different bytes
- **WHEN** a remote file's bytes change without a distinguishable size or modification-time change before the final save check
- **THEN** a normal save reports a conflict and leaves the changed target intact

#### Scenario: Interrupted upload before commit
- **WHEN** a staged upload fails before replacement is sent
- **THEN** the destination remains unchanged and cleanup is attempted or recorded as pending if the host is unreachable

#### Scenario: Lost commit acknowledgement
- **WHEN** the connection drops after replacement is sent but before its result arrives
- **THEN** the editor remains dirty, reports an uncertain outcome and does not automatically replay the save

#### Scenario: Target removed during upload
- **WHEN** an existing target is absent at the final check, including for a forced save
- **THEN** the save is refused instead of recreating the missing target

#### Scenario: Remote hard-link metadata unavailable
- **WHEN** the server cannot establish whether the target has other hard-link names
- **THEN** the editor requires acknowledgement of replacement semantics before issuing the save

#### Scenario: Cleanup after restart
- **WHEN** the hub reconnects with a pending staging cleanup record
- **THEN** it removes only a staging entry it can identify as owned by that write and never arbitrary files sharing a naming pattern
