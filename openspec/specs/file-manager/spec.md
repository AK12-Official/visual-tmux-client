# file-manager Specification

## Purpose
Lets an authenticated browser user browse, preview, edit, and organize files on the machine running the hub, within an operator-configured access boundary, without leaving the terminal workspace.

## Requirements

### Requirement: Filesystem access boundary

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

### Requirement: Directory listing

The hub SHALL return the immediate children of a directory, each carrying at least its name, whether it is a directory, its size, and its modification time. A child that is a symbolic link SHALL be described by what it points at, within the boundary: a link to a directory SHALL be reported as a directory, so the browser offers it for expanding rather than for opening. A link whose target lies outside every configured root SHALL NOT be described by that target, so a listing never answers with the size or modification time of a path the caller may not name. An entry that cannot be described at all SHALL be reported as present without a size or modification time, rather than failing the listing or reporting the link's own size in place of a target's; a link whose target is missing is one of those. Listing SHALL order directories before files, and each group by name. Listing SHALL be bounded by the configured maximum number of entries and SHALL report whether the result was truncated. Listing a directory with no children SHALL succeed and return an empty result, not an error.

Reading SHALL be bounded by the same configured maximum, plus the entries the hub is itself holding back from the listing -- the staging files of writes in flight, of which there are as many as there are concurrent writes. The cost of listing a directory is therefore the cost of the listing rather than the cost of the directory. A directory holding more entries than that bound SHALL be reported as truncated, and which of its entries appear in a truncated listing is not specified. The hub SHALL abandon a listing when the caller's request is cancelled.

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

### Requirement: Bounded file reading

The hub SHALL stream a file's contents to the caller without loading the whole file into memory, and SHALL report the file's byte size and modification time alongside the contents. The hub SHALL refuse to read a file whose size exceeds the configured per-file limit. The hub SHALL allow the caller to distinguish binary content from text content so the browser does not render binary bytes as text. The hub SHALL read only regular files: a directory, a named pipe, a socket, a device, and any other kind SHALL be refused, and the hub SHALL NOT wait on a file that would block the request. The classification of a file's contents as binary or text SHALL be decided by the whole of its contents, not by a prefix of them.

The hub SHALL NOT serve more bytes than the size it checked against the limit, and SHALL report a size that describes the body it sends. The classification of a file and the bytes served are read from the same file at different moments, so a file rewritten in between can be served with a classification taken before the change; a caller that then writes it is refused as a conflict, because the modification time it recorded is no longer the file's -- unless the change landed within the finest interval the filesystem records, in which case no comparison of times can tell the two apart, which is the same limit that applies to concurrent writes. A file that grows after the check is served at the length that was checked; a file that shrinks after it is served as what it holds now, reported at that shorter length. A response never promises more bytes than it carries, because a client cannot tell such a response from one whose transfer failed.

#### Scenario: Read a text file

- **WHEN** a caller reads a file within the size limit
- **THEN** the hub streams the exact bytes and reports the file's size and modification time

#### Scenario: Read exceeds the size limit

- **WHEN** a caller reads a file larger than the configured per-file limit
- **THEN** the hub refuses with a too-large error and transfers no file content

#### Scenario: A file that grows while it is served

- **WHEN** a file grows after its size has been checked and read
- **THEN** the hub serves exactly the bytes that were checked, and the size it reports describes the body it sent

#### Scenario: A file truncated while it is opened

- **WHEN** a file is truncated after its size has been checked
- **THEN** the hub serves what the file holds now and reports that length, rather than promising the length it measured

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

### Requirement: File and directory operations

The hub SHALL support creating an empty file, creating a directory, renaming or moving a file or directory, and deleting a file or directory. Creating or renaming SHALL validate both the source and the destination against the access boundary. Creating a target that already exists SHALL be refused. Deleting a directory that contains entries SHALL require the caller to request recursive deletion, and SHALL be refused otherwise.

A create request SHALL name the kind of entry it asks for, from the set the hub defines. A request that names none of them -- an absent field, or a spelling this hub does not have -- SHALL be refused rather than carried out as the creation of a file, since the caller asked for something else and would be told it succeeded. Every request on these routes SHALL be exactly one JSON object carrying the fields the route defines: an unknown field, a body with nothing in it, and a second value after the object SHALL each be refused, because acting on the part of such a request that is recognised would carry out something the caller did not write.

#### Scenario: Create a file

- **WHEN** a caller creates a file at a path inside a configured root whose parent exists and which does not already exist
- **THEN** the hub creates an empty file at that path

#### Scenario: Create a directory

- **WHEN** a caller creates a directory at a path inside a configured root whose parent exists and which does not already exist
- **THEN** the hub creates the directory

#### Scenario: Create over an existing entry

- **WHEN** a caller creates a file or directory at a path that already exists
- **THEN** the hub refuses and leaves the existing entry unmodified

#### Scenario: Parent does not exist

- **WHEN** a caller creates an entry whose parent directory does not exist
- **THEN** the hub refuses rather than creating intermediate directories

#### Scenario: Rename within the boundary

- **WHEN** a caller renames an existing entry to a path inside a configured root
- **THEN** the entry is reachable at the new path and no longer at the old one

#### Scenario: Rename across the boundary

- **WHEN** a caller renames an entry to a destination outside every configured root
- **THEN** the hub refuses and leaves the entry at its original path

#### Scenario: Rename onto an existing entry

- **WHEN** a caller renames an entry to a path that already exists
- **THEN** the hub refuses and leaves both entries unmodified

#### Scenario: Delete a non-empty directory without recursion

- **WHEN** a caller deletes a directory that contains entries without requesting recursive deletion
- **THEN** the hub refuses with a distinct error and removes nothing

#### Scenario: Delete a non-empty directory recursively

- **WHEN** a caller deletes a directory that contains entries and requests recursive deletion
- **THEN** the hub removes the directory and its contents

#### Scenario: Create names no kind of entry

- **WHEN** a caller asks for a create without naming the kind of entry, or names a kind the hub does not define
- **THEN** the hub refuses the request and creates nothing

#### Scenario: A request carries a field the hub does not define

- **WHEN** a caller's body on a create, rename, or delete route carries a field the hub does not define
- **THEN** the hub refuses the request rather than acting on the fields it recognises

#### Scenario: A request body is not one JSON object

- **WHEN** a caller's body on one of those routes is empty, is not JSON, or carries a second value after the object
- **THEN** the hub refuses the request

### Requirement: Authenticated file access

The hub SHALL require every file operation to be performed by an authenticated caller. The hub SHALL NOT disclose the existence, size, type, or contents of any path to an unauthenticated caller, and SHALL NOT perform any filesystem operation on its behalf. Rejecting an unauthenticated caller SHALL NOT require reading the filesystem.

#### Scenario: Missing or invalid credentials

- **WHEN** a caller invokes a file operation without credentials, or with credentials that do not match
- **THEN** the hub refuses with an authentication error and performs no filesystem operation

#### Scenario: File operations do not weaken terminal authorization

- **WHEN** a file operation succeeds with valid credentials
- **THEN** terminal attachment still requires its own session-bound single-use ticket

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

### Requirement: Browser file manager

The browser SHALL provide a file manager opened from the terminal for the current session. On opening, the browser SHALL resolve the manager's starting directory from the session's active pane working directory. That starting directory SHALL be captured once, so that later changes to the active pane do not move an already-open manager. The browser SHALL then let the user navigate freely within the boundary, including moving to a parent directory and selecting any directory in the tree as the current one. When the pane working directory is not permitted by the boundary, the browser SHALL open at a permitted directory instead and inform the user that it did so. The manager SHALL load directory contents on demand as the user expands the tree. The manager SHALL offer creating a file, creating a directory, renaming, deleting, and downloading, and SHALL require the user to confirm a delete before it is performed.

The browser SHALL NOT let the deletes it sends race the saves it sends. The hub's last check before replacing a file and the replacement itself are two adjacent system calls, so a write landing between them leaves the file present at a path the user has just been told it was deleted from, while the delete's own answer reports success. Before sending a delete the browser SHALL wait for the writes already travelling that name the entry or anything beneath it, and it SHALL refuse to send a save whose path, or a directory holding it, has a delete in flight rather than letting the two race. This orders the browser's own requests against each other; a write from any other process is not ordered by it, which is specified under Optimistic concurrent writes.

The manager SHALL be operable without a pointer. Its context menu SHALL take the focus when it opens, SHALL move that focus among the actions a user can choose -- an action that cannot be chosen is not a stop -- and SHALL return it to the entry the menu was opened from when it closes. A menu the browser raised for the keyboard SHALL be anchored to that entry, because such an event carries no pointer position to open at.

The manager SHALL NOT open a tab for a file whose contents it has asked the hub for when its own delete of that file, or of a directory holding it, is answered before that answer arrives. The read was granted before the delete, so it still returns the file's contents; a tab built from them would name a path that no longer exists, and every later save of it could only be refused. A delete SHALL record what it removed for the reads that were travelling when it was answered, and those reads SHALL install nothing.

#### Scenario: Open from the terminal

- **WHEN** the user opens the file manager while attached to a session whose active pane is at a permitted directory
- **THEN** the manager opens showing that directory and the terminal remains usable

#### Scenario: Active pane changes while open

- **WHEN** the user changes the active pane or its working directory after the manager has opened
- **THEN** the manager keeps its current directory rather than following the pane

#### Scenario: Navigate to a parent directory

- **WHEN** the user navigates to the parent of the current directory
- **THEN** the manager shows that parent's contents as its current directory

#### Scenario: Pane directory is not permitted

- **WHEN** the session's active pane working directory is not permitted by the boundary
- **THEN** the manager opens at a permitted directory and tells the user the pane directory was not accessible

#### Scenario: Directory loads on demand

- **WHEN** the user expands a directory that has not been loaded yet
- **THEN** the browser requests that directory's children at that point rather than loading the whole tree up front

#### Scenario: Deletion requires confirmation

- **WHEN** the user deletes a file or directory
- **THEN** the browser asks for confirmation first and performs no deletion if the user declines

#### Scenario: A delete while a save of the same file is travelling

- **WHEN** the user confirms deleting a file that has a save in flight
- **THEN** the browser sends the delete only once that save has been answered, so the write cannot land between the hub's last check and its replacement

#### Scenario: A save while a delete of the same file is in flight

- **WHEN** the user saves a file whose deletion has been sent and not yet answered
- **THEN** the browser sends no write and tells the user that a delete of that file, or of a directory above it, is in flight

#### Scenario: Operation fails

- **WHEN** a file operation is refused by the hub
- **THEN** the browser reports the reason using the application's existing notification mechanism and leaves the manager usable

#### Scenario: The context menu from the keyboard

- **WHEN** the user opens an entry's context menu from the keyboard and then closes it
- **THEN** the menu holds the focus while it is open, the arrow keys move between the actions that can be chosen, and the focus returns to that entry when it closes

#### Scenario: A file deleted while its contents were being read

- **WHEN** the user deletes a file whose contents the browser has already asked the hub for, and the delete is answered before that read answers
- **THEN** the browser opens no tab for it and reports that it was deleted

### Requirement: Editing and unsaved changes

The browser SHALL allow multiple files to be open at once, each in its own tab, and SHALL mark a file as modified while its in-memory contents differ from what was last read or saved. The browser SHALL surface that unsaved changes exist outside the editor, so that closing the manager or the terminal can warn before discarding them. Closing a modified tab SHALL require confirmation. When a save is refused because the file changed externally, the browser SHALL tell the user and offer to overwrite.

Each open file's editor state, including its undo history, SHALL be retained while its tab is open and its contents are shown in the editor. Viewing another file SHALL NOT discard it, because the undo stack a user reaches for is usually the one belonging to the file they just switched away from. Renaming a file to a kind that is not shown in the editor is the one case that does discard it: the tab keeps its contents and its unsaved marks, and only the editor and its history go.

A save whose answer arrives after the file has been renamed SHALL NOT be recorded against the tab at its new path. The browser SHALL report that the file moved and leave the edit unsaved, so that the contents are written to the name the tab holds by a further save, rather than being reported as stored at a name where nothing was written.

The same refusal SHALL apply when a rename of that file is still in flight as the answer arrives. The write and the rename are then racing on the server, and the browser cannot tell which landed first: the write may have landed before the entry moved, in which case the edit is at the new name and refusing to record it only costs a second save, or the rename may have landed first and the write recreated the old name, in which case recording it would report the edit as stored where nothing was written. The browser SHALL report the crossing and leave the tab modified.

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

#### Scenario: A rename is still running when the save is answered

- **WHEN** a rename of a file has been sent and not yet been answered at the moment that file's save is answered
- **THEN** the browser reports that the save crossed a rename, does not record it as saved, and leaves the tab modified

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

### Requirement: Content preview

The browser SHALL present a file according to its content type. Images SHALL be previewed as images. Markdown SHALL be previewable both as rendered output and as editable source, with the editable source as the default view. Text and source code SHALL open in the editor. Files detected as binary SHALL be presented as information about the file rather than as rendered or editable text. The browser SHALL NOT render file content as an HTML document.

The browser SHALL NOT decide what a file is from its name alone. A name that suggests binary contents, or an image too large to render, SHALL be put to the hub before the browser presents the file, so that text the hub reads as text opens in the editor whatever it is called. That question SHALL cost the caller no file content.

The hub's classification answers whether a file's bytes may be decoded as text, and that is the only question it answers. An image within the preview bound SHALL be presented as an image whether or not the hub classified its contents as binary -- every image is binary, so that answer is true and about something else -- and "binary" SHALL NOT on its own be read as "not previewable".

Rendering SHALL be bounded independently of the transfer limit, so that a large file cannot freeze the interface: an image larger than the preview bound SHALL NOT be rendered, and rendered Markdown SHALL be limited to a bounded prefix of the source. The bound SHALL be applied to the size the hub reports at the moment of the read rather than to a size a directory listing reported earlier, and no content beyond it SHALL be transferred for a preview that will not use it. A file found to be over the bound SHALL be presented as information with a download action, and the user SHALL be told why.

A file the manager opened without reading has no contents in hand, and only an image is opened that way: the preview fetches what it needs, and the bytes are never decoded as text. What such a file is *called* SHALL NOT decide that its contents may be shown in the editor, because there are none to show. A rename that gives it a name the editor would hold SHALL start a read instead, and the hub's answer SHALL decide how the file is presented. Until that answer arrives the file SHALL be presented as information about the file, so that a picture renamed to a text extension cannot be saved over with a document nobody read.

#### Scenario: Image file

- **WHEN** the user opens a file whose type is a previewable image within the preview bound
- **THEN** the browser displays the image

#### Scenario: Large image

- **WHEN** the user opens an image larger than the browser's image preview bound but within the transfer limit
- **THEN** the browser presents the file as information with the option to download it, rather than rendering it

#### Scenario: An image that grew after it was listed

- **WHEN** the user opens an image whose size was within the preview bound when the directory was listed and is over it by the time it is read
- **THEN** the browser transfers no content beyond the bound, presents the file as information with a download action, and says that it is too large to render

#### Scenario: An image that shrank since it was listed

- **WHEN** the user opens an image whose size was over the preview bound when the directory was listed and is under it by the time it is read
- **THEN** the browser presents it as an image, because the bound is applied to the size reported at the read, and the hub's classification of its contents as binary is not read as "not previewable"

#### Scenario: A text file with a binary name

- **WHEN** the user opens a file whose extension suggests binary contents and whose contents the hub reads as text
- **THEN** the browser opens it in the editor

#### Scenario: A binary file with a text name

- **WHEN** the user opens a file whose extension suggests text and which the hub reads as binary
- **THEN** the browser presents it as information and does not decode its bytes as text

#### Scenario: Markdown file

- **WHEN** the user opens a Markdown file
- **THEN** the browser opens it as editable source and offers a rendered view

#### Scenario: Large Markdown file

- **WHEN** the user views a Markdown file whose content exceeds the render bound
- **THEN** the browser renders a bounded prefix and states that the preview was truncated

#### Scenario: Source file

- **WHEN** the user opens a text or source file
- **THEN** the browser opens it in the editor

#### Scenario: Binary file

- **WHEN** the user opens a file detected as binary
- **THEN** the browser presents file information and a download action instead of the contents

#### Scenario: An image renamed to a name the editor would hold

- **WHEN** the user renames a file that is open as an image to a name the editor would hold
- **THEN** the browser reads the file and presents it according to the hub's answer, rather than offering the editor an empty document

#### Scenario: A renamed file whose read has not answered

- **WHEN** a file opened as an image has been renamed to a name the editor would hold, and the read that rename started has not yet answered
- **THEN** the browser presents it as information about the file, and the editor is not offered for it

### Requirement: Preview rendering safety

Rendered Markdown SHALL be sanitized before insertion into the document, so that a file containing script elements, event-handler attributes, or embedded document elements cannot execute code or load remote content when previewed. The browser SHALL never insert raw file contents into the document as markup.

#### Scenario: Markdown containing script

- **WHEN** the user previews a Markdown file containing a script element or an event-handler attribute
- **THEN** the rendered output contains neither, and no script from the file executes

#### Scenario: Markdown containing embedded document

- **WHEN** the user previews a Markdown file containing an embedded frame or object element
- **THEN** the rendered output omits it

### Requirement: File download

The browser SHALL allow downloading a file's exact bytes to the user's machine, subject to the configured per-file size limit, without requiring the user to open it in the editor first.

#### Scenario: Download a file

- **WHEN** the user downloads a file within the size limit
- **THEN** the browser saves the file under its own name with its bytes unchanged

#### Scenario: Download exceeds the limit

- **WHEN** the user downloads a file larger than the configured per-file limit
- **THEN** the browser reports the limit and saves nothing

### Requirement: Insert a path into the terminal

The browser SHALL allow the user to insert a file's path into the current terminal's input line from the file manager. The inserted text SHALL NOT contain a line terminator, so that an insertion never executes a command on its own. A path whose characters a shell would otherwise interpret SHALL be quoted so that the inserted text denotes the path literally. Insertion SHALL require a live terminal attachment; when none is available the browser SHALL report that instead of silently doing nothing.

#### Scenario: Insert a path

- **WHEN** the user inserts a file's path into the terminal from the file manager
- **THEN** the path appears in the terminal's input line and no command is executed

#### Scenario: Path needs quoting

- **WHEN** the inserted path contains a space or a character a shell would interpret
- **THEN** the inserted text quotes it so that submitting the line would refer to that exact path

#### Scenario: No live terminal

- **WHEN** the user inserts a path while no terminal attachment is active
- **THEN** the browser reports that no terminal is available and inserts nothing

#### Scenario: File manager state is unaffected

- **WHEN** a path is inserted into the terminal
- **THEN** open files, unsaved edits, and the current directory are unchanged

### Requirement: Disabled file manager

When the hub's file capability is disabled, the hub SHALL refuse every file operation without touching the filesystem, and it SHALL NOT resolve or open the configured roots. A root that cannot be used supplies no boundary to a capability that is off, and refusing to start over one would take down operations that have nothing to do with files.

The hub SHALL therefore start, and serve its terminal and session operations, with a configuration whose roots cannot be used, as long as the capability is disabled. With the capability enabled the same roots SHALL be resolved while the configuration loads, so that one which cannot enclose anything is reported at startup rather than surfacing later as every operation being refused.

#### Scenario: Disabled with a root that cannot be used

- **WHEN** the hub is configured with the file capability disabled and a root that does not exist
- **THEN** the hub starts, and its file operations are refused without the filesystem being touched

#### Scenario: Enabled with a root that cannot be used

- **WHEN** the hub is configured with the file capability enabled and a root that does not exist
- **THEN** the configuration is refused and the root is reported
