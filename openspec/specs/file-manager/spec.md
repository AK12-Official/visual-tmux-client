# file-manager Specification

## Purpose
Lets an authenticated browser user browse, preview, edit, and organize files on the machine running the hub, within an operator-configured access boundary, without leaving the terminal workspace.

## Requirements

### Requirement: Filesystem access boundary

By default the hub SHALL allow file operations on any regular path its own operating-system user can access, mirroring the access that user already has through an attached terminal. When an operator configures a set of root directories, the hub SHALL confine every operation to those roots instead. The hub SHALL authorize each operation against the canonical, symlink-resolved form of the target path, not the caller-supplied lexical form, so that a symbolic link cannot be used to reach a target outside a root. The hub SHALL reject `/proc`, `/sys`, and `/dev` before applying any root check, so that no configuration can expose them. A target that does not yet exist SHALL be validated by resolving the canonical form of its nearest existing ancestor and re-appending the remaining segments. A caller SHALL supply absolute, platform-native paths of at most 4096 characters.

#### Scenario: Default boundary

- **WHEN** no root directories are configured and the hub's operating-system user can access a regular path outside the user's home directory
- **THEN** the hub performs the operation on that path

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

The hub SHALL return the immediate children of a directory, each carrying at least its name, whether it is a directory, its size, and its modification time. Listing SHALL order directories before files. Listing SHALL be bounded by the configured maximum number of entries and SHALL report whether the result was truncated. Listing a directory with no children SHALL succeed and return an empty result, not an error.

#### Scenario: Directory with contents

- **WHEN** a caller lists a directory containing both files and subdirectories
- **THEN** the hub returns one entry per child with its name, directory flag, size, and modification time, with subdirectories ordered before files

#### Scenario: Empty directory

- **WHEN** a caller lists a directory with no children
- **THEN** the hub returns an empty list and reports success

#### Scenario: Directory exceeds the listing bound

- **WHEN** a directory holds more children than the configured maximum
- **THEN** the hub returns at most that many entries and reports the result as truncated

#### Scenario: Target is not a directory

- **WHEN** a caller lists a path that is a regular file, or that does not exist
- **THEN** the hub refuses the operation with an error distinguishing the two cases from an empty directory

### Requirement: Bounded file reading

The hub SHALL stream a file's contents to the caller without loading the whole file into memory, and SHALL report the file's byte size and modification time alongside the contents. The hub SHALL refuse to read a file whose size exceeds the configured per-file limit. The hub SHALL allow the caller to distinguish binary content from text content so the browser does not render binary bytes as text.

#### Scenario: Read a text file

- **WHEN** a caller reads a file within the size limit
- **THEN** the hub streams the exact bytes and reports the file's size and modification time

#### Scenario: Read exceeds the size limit

- **WHEN** a caller reads a file larger than the configured per-file limit
- **THEN** the hub refuses with a too-large error and transfers no file content

#### Scenario: Read a missing file

- **WHEN** a caller reads a path that does not exist
- **THEN** the hub returns a not-found error

#### Scenario: Read a directory as a file

- **WHEN** a caller reads a path that is a directory
- **THEN** the hub refuses the operation rather than returning directory contents

#### Scenario: Binary content

- **WHEN** a caller reads a file whose contents are not valid UTF-8 text
- **THEN** the hub does not present it as editable text and the browser does not render its bytes as text

### Requirement: Optimistic concurrent writes

A write SHALL carry the modification time the caller last observed for the target. The hub SHALL refuse the write with a conflict error when that value does not match the file's current modification time. A caller MAY omit the observed modification time to force an overwrite. The hub SHALL write through a temporary file created in the target's own directory and then atomically replace the target, so that a failed, interrupted, or oversized write never leaves a partially written or truncated file at the target path. The hub SHALL remove the temporary file when a write fails. A write whose received body length differs from the declared length SHALL be refused. When a caller supplies an observed modification time for a file that does not exist, the hub SHALL report not-found rather than creating it. Modification times SHALL be conveyed as integer milliseconds since the Unix epoch, and a successful write SHALL return the target's resulting modification time so the caller can continue editing without re-reading the file.

#### Scenario: Write with a matching modification time

- **WHEN** a caller writes a file supplying the modification time it last observed, and the file is unchanged
- **THEN** the hub replaces the file's contents, returns the resulting modification time, and the caller can read back exactly the bytes written

#### Scenario: Write after an external change

- **WHEN** a caller writes a file supplying a modification time that no longer matches because the file changed since it was read
- **THEN** the hub refuses with a conflict error, leaves the file's current contents intact, and transfers no partial content

#### Scenario: Consecutive saves

- **WHEN** a caller saves a file and then saves it again using the modification time returned by the first save
- **THEN** the second save succeeds without a spurious conflict

#### Scenario: Forced overwrite

- **WHEN** a caller writes a file omitting the observed modification time
- **THEN** the hub replaces the file regardless of its current modification time

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

### Requirement: File and directory operations

The hub SHALL support creating an empty file, creating a directory, renaming or moving a file or directory, and deleting a file or directory. Creating or renaming SHALL validate both the source and the destination against the access boundary. Creating a target that already exists SHALL be refused. Deleting a directory that contains entries SHALL require the caller to request recursive deletion, and SHALL be refused otherwise.

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

### Requirement: Authenticated file access

The hub SHALL require every file operation to be performed by an authenticated caller. The hub SHALL NOT disclose the existence, size, type, or contents of any path to an unauthenticated caller, and SHALL NOT perform any filesystem operation on its behalf. Rejecting an unauthenticated caller SHALL NOT require reading the filesystem.

#### Scenario: Missing or invalid credentials

- **WHEN** a caller invokes a file operation without credentials, or with credentials that do not match
- **THEN** the hub refuses with an authentication error and performs no filesystem operation

#### Scenario: File operations do not weaken terminal authorization

- **WHEN** a file operation succeeds with valid credentials
- **THEN** terminal attachment still requires its own session-bound single-use ticket

### Requirement: Session working directory

The hub SHALL expose the working directory of a session's active pane, so the browser can open the file manager at the directory the user is currently working in.

#### Scenario: Session has an active pane

- **WHEN** a caller requests the working directory of an existing session
- **THEN** the hub returns the absolute working directory of that session's active pane

#### Scenario: Session is absent

- **WHEN** a caller requests the working directory of a session that does not exist
- **THEN** the hub returns a not-found error

### Requirement: Browser file manager

The browser SHALL provide a file manager opened from the terminal for the current session. On opening, the browser SHALL resolve the manager's starting directory from the session's active pane working directory. That starting directory SHALL be captured once, so that later changes to the active pane do not move an already-open manager. The browser SHALL then let the user navigate freely within the boundary, including moving to a parent directory and selecting any directory in the tree as the current one. When the pane working directory is not permitted by the boundary, the browser SHALL open at a permitted directory instead and inform the user that it did so. The manager SHALL load directory contents on demand as the user expands the tree. The manager SHALL offer creating a file, creating a directory, renaming, deleting, and downloading, and SHALL require the user to confirm a delete before it is performed.

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

#### Scenario: Operation fails

- **WHEN** a file operation is refused by the hub
- **THEN** the browser reports the reason using the application's existing notification mechanism and leaves the manager usable

### Requirement: Editing and unsaved changes

The browser SHALL allow multiple files to be open at once, each in its own tab, and SHALL mark a file as modified while its in-memory contents differ from what was last read or saved. The browser SHALL surface that unsaved changes exist outside the editor, so that closing the manager or the terminal can warn before discarding them. Closing a modified tab SHALL require confirmation. When a save is refused because the file changed externally, the browser SHALL tell the user and offer to overwrite.

#### Scenario: Modify and save

- **WHEN** the user edits an open file and saves
- **THEN** the browser writes the current contents and the file becomes unmodified

#### Scenario: Switch between open files

- **WHEN** the user switches tabs while one file has unsaved edits
- **THEN** the edits are retained and the file remains marked as modified

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

Rendering SHALL be bounded independently of the transfer limit, so that a large file cannot freeze the interface: an image larger than the preview bound SHALL NOT be rendered, and rendered Markdown SHALL be limited to a bounded prefix of the source.

#### Scenario: Image file

- **WHEN** the user opens a file whose type is a previewable image within the preview bound
- **THEN** the browser displays the image

#### Scenario: Large image

- **WHEN** the user opens an image larger than the browser's image preview bound but within the transfer limit
- **THEN** the browser presents the file as information with the option to download it, rather than rendering it

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
