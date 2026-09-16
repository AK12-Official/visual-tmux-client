// Package files serves the browser file manager: directory listing, streamed
// reads, optimistic-concurrency writes, and basic file operations, all behind
// one path guard that decides which paths an operation may touch.
package files

import "errors"

// The service's error vocabulary. Callers map these to wire codes rather than
// exposing a filesystem error directly, because the browser has to tell one
// failure from another -- the conflict flow in particular depends on
// distinguishing a lost race from an ordinary failure.
var (
	// ErrInvalidPath is a path the caller may not name at all: not absolute, too
	// long, containing a parent-directory segment. It also covers a path that
	// names the wrong kind of thing for the operation, such as listing a file.
	ErrInvalidPath = errors.New("invalid path")
	// ErrPathNotAllowed is a well-formed path that falls outside the boundary.
	ErrPathNotAllowed = errors.New("path not allowed")
	// ErrNotFound is a path the operation requires to exist, which does not.
	ErrNotFound = errors.New("path not found")
	// ErrPermissionDenied is a path the hub's own operating-system user cannot
	// read or write.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrFileTooLarge is a file beyond the configured per-file limit.
	ErrFileTooLarge = errors.New("file too large")
	// ErrConflict is a target that changed, or already exists where the
	// operation required it not to.
	ErrConflict = errors.New("conflict")
	// ErrInvalidBody is a request body that does not match what it declared.
	ErrInvalidBody = errors.New("invalid request body")
	// ErrDirNotEmpty is a directory that still contains entries.
	ErrDirNotEmpty = errors.New("directory not empty")
	// ErrWriteFailed is a write that could not be completed safely. Nothing was
	// changed at the target path.
	ErrWriteFailed = errors.New("write failed")
	// ErrCrossRoot is a move whose two ends lie in different configured roots,
	// which this hub does not perform.
	//
	// It is its own error rather than a not-allowed path because neither path is
	// outside the boundary: what cannot be expressed is the move between them,
	// and telling a caller one of two paths it may name is forbidden would be
	// false. See renameAt for why it is refused rather than attempted.
	ErrCrossRoot = errors.New("cross-root move")
)
