package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// tempFilePrefix marks the sibling a write stages through before renaming it
	// over the target. It is recognisable so that a leftover one from an
	// interrupted run can be identified, and hidden so it does not clutter a
	// listing.
	tempFilePrefix = ".vtc-write-"

	createFileMode = 0o644
	createDirMode  = 0o755
)

// Options configure a Service.
type Options struct {
	// Roots is the boundary every operation is authorized against.
	Roots *RootSet
	// MaxFileSize is the largest file the service will read or write.
	MaxFileSize int64
	// MaxDirEntries bounds one listing.
	MaxDirEntries int
	// Enabled turns the whole capability off without touching the filesystem.
	Enabled bool
}

// Service performs filesystem operations on behalf of an authenticated caller.
// Every operation resolves its path through the RootSet first; there is no
// method here that reaches a caller-supplied path any other way.
type Service struct {
	roots         *RootSet
	maxFileSize   int64
	maxDirEntries int
	enabled       bool
}

// NewService constructs a Service.
func NewService(opts Options) *Service {
	return &Service{
		roots:         opts.Roots,
		maxFileSize:   opts.MaxFileSize,
		maxDirEntries: opts.MaxDirEntries,
		enabled:       opts.Enabled,
	}
}

// Enabled reports whether file operations are available. A disabled hub is
// answered by the transport without any operation being attempted, so nothing
// reads the filesystem on behalf of a capability that is turned off.
func (s *Service) Enabled() bool {
	return s.enabled
}

// StartDirectory returns the directory a browser should open the file manager
// at, given the candidate a session reported, and whether it substituted one.
//
// The substitution exists because only the hub knows the boundary: a browser
// cannot discover a permitted directory for itself, so when the candidate falls
// outside a configured root the hub names one that does not. A candidate that is
// unusable for any other reason -- it no longer exists, say -- is returned
// unchanged, because that is not a boundary decision and the caller can do
// something more useful with it than this function can.
func (s *Service) StartDirectory(candidate string) (string, bool) {
	if _, err := s.roots.Resolve(candidate, ModeRead); err == nil {
		return candidate, false
	} else if !errors.Is(err, ErrPathNotAllowed) {
		return candidate, false
	}

	roots := s.roots.Roots()
	if len(roots) == 0 {
		return candidate, false
	}
	return roots[0], true
}

// List returns one directory's immediate children, directories first and then
// by name. An empty directory is a successful empty result, not an error.
func (s *Service) List(ctx context.Context, path string) (ListResult, error) {
	resolved, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return ListResult{}, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return ListResult{}, classifyPathError(path, err)
	}
	if !info.IsDir() {
		return ListResult{}, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, path)
	}

	children, err := os.ReadDir(resolved)
	if err != nil {
		return ListResult{}, classifyPathError(path, err)
	}

	dirs, plain := make([]Entry, 0, len(children)), make([]Entry, 0, len(children))
	for _, child := range children {
		entry := Entry{Name: child.Name()}
		if child.IsDir() {
			entry.IsDir = true
			dirs = append(dirs, entry)
			continue
		}
		// An entry that cannot be stat'd still exists, so it is reported without
		// its size rather than taking the whole listing down.
		if childInfo, statErr := child.Info(); statErr == nil {
			entry.Size = childInfo.Size()
			entry.Mtime = childInfo.ModTime().UnixMilli()
		}
		plain = append(plain, entry)
	}

	// os.ReadDir sorts by name, so grouping is all that is left to do and each
	// group is already ordered.
	entries := append(dirs, plain...)
	result := ListResult{Path: resolved, Truncated: len(entries) > s.maxDirEntries}
	if result.Truncated {
		entries = entries[:s.maxDirEntries]
	}
	result.Entries = entries
	return result, nil
}

// Read opens a file for streaming and reports its size and modification time.
// The size is checked against the configured limit before the file is opened,
// so an oversized file transfers nothing and is never held in memory.
func (s *Service) Read(ctx context.Context, path string) (ReadResult, error) {
	resolved, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return ReadResult{}, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return ReadResult{}, classifyPathError(path, err)
	}
	if info.IsDir() {
		return ReadResult{}, fmt.Errorf("%w: %s is a directory", ErrInvalidPath, path)
	}
	if info.Size() > s.maxFileSize {
		return ReadResult{}, fmt.Errorf("%w: %s is %d bytes, over the %d byte limit",
			ErrFileTooLarge, path, info.Size(), s.maxFileSize)
	}

	file, err := os.Open(resolved)
	if err != nil {
		return ReadResult{}, classifyPathError(path, err)
	}
	return ReadResult{File: file, Size: info.Size(), Mtime: info.ModTime().UnixMilli()}, nil
}

// Write replaces a file's contents and returns the resulting modification time.
//
// expectedMtime is the value the caller last observed. A mismatch is a conflict
// and nothing is written; a nil expectedMtime forces the overwrite, which is
// what the browser sends once the user has confirmed. A non-nil expectedMtime
// against a file that does not exist is a not-found rather than a create: a
// caller that believes it is editing an existing file must not be handed a new
// one.
func (s *Service) Write(
	ctx context.Context, path string, body io.Reader, declaredSize int64, expectedMtime *int64,
) (int64, error) {
	resolved, err := s.roots.Resolve(path, ModeCreate)
	if err != nil {
		return 0, err
	}

	existing, statErr := os.Stat(resolved)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return 0, classifyPathError(path, statErr)
	}
	if exists && existing.IsDir() {
		return 0, fmt.Errorf("%w: %s is a directory", ErrInvalidPath, path)
	}
	if expectedMtime != nil {
		if !exists {
			return 0, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		if existing.ModTime().UnixMilli() != *expectedMtime {
			return 0, fmt.Errorf("%w: %s changed since it was read", ErrConflict, path)
		}
	}

	return s.writeAtomically(ctx, resolved, body, declaredSize)
}

// writeAtomically stages the body in a sibling of the target and renames it
// over the target only once every byte is on disk. A failure at any point
// leaves the target exactly as it was, which is the property a plain truncate
// and write cannot offer.
func (s *Service) writeAtomically(
	ctx context.Context, target string, body io.Reader, declaredSize int64,
) (int64, error) {
	stage, err := os.CreateTemp(filepath.Dir(target), tempFilePrefix+"*")
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(stage.Name()) //nolint:errcheck // best effort on a failure path
		}
	}()

	if err := s.copyBody(ctx, stage, body, declaredSize); err != nil {
		_ = stage.Close() //nolint:errcheck // the copy error is the one worth reporting
		return 0, err
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close() //nolint:errcheck // the sync error is the one worth reporting
		return 0, fmt.Errorf("%w: sync: %w", ErrWriteFailed, err)
	}
	if err := stage.Close(); err != nil {
		return 0, fmt.Errorf("%w: close: %w", ErrWriteFailed, err)
	}
	if err := os.Rename(stage.Name(), target); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	committed = true

	return mtimeOf(target)
}

// copyBody checks the body against both the declared length and the configured
// limit as it goes, so a mismatched body is refused rather than partially
// committed.
func (s *Service) copyBody(ctx context.Context, dst io.Writer, body io.Reader, declaredSize int64) error {
	if declaredSize < 0 {
		return fmt.Errorf("%w: declared size %d is negative", ErrInvalidBody, declaredSize)
	}
	if declaredSize > s.maxFileSize {
		return fmt.Errorf("%w: declared %d bytes, over the %d byte limit",
			ErrFileTooLarge, declaredSize, s.maxFileSize)
	}

	// One byte past the declared length is all it takes to know the body is
	// longer than it claimed, so nothing beyond that is read or written.
	written, err := io.Copy(dst, io.LimitReader(cancelableReader{ctx: ctx, body: body}, declaredSize+1))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	if written != declaredSize {
		return fmt.Errorf("%w: declared %d bytes, received %d", ErrInvalidBody, declaredSize, written)
	}
	return nil
}

// Create makes an empty file or a directory. The parent must already exist:
// creating intermediate directories would let a mistyped path quietly build a
// tree instead of being refused.
func (s *Service) Create(ctx context.Context, path string, isDir bool) error {
	resolved, err := s.roots.Resolve(path, ModeCreate)
	if err != nil {
		return err
	}

	parent := filepath.Dir(resolved)
	if info, statErr := os.Stat(parent); statErr != nil {
		return classifyPathError(filepath.Dir(path), statErr)
	} else if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, filepath.Dir(path))
	}

	// Lstat, so that a dangling symlink counts as an existing entry rather than
	// as a free name to create over.
	if _, err := os.Lstat(resolved); err == nil {
		return fmt.Errorf("%w: %s already exists", ErrConflict, path)
	} else if !os.IsNotExist(err) {
		return classifyPathError(path, err)
	}

	if isDir {
		if err := os.Mkdir(resolved, createDirMode); err != nil {
			return classifyPathError(path, err)
		}
		return nil
	}
	file, err := os.OpenFile(resolved, os.O_CREATE|os.O_EXCL|os.O_WRONLY, createFileMode)
	if err != nil {
		return classifyPathError(path, err)
	}
	return file.Close()
}

// Rename moves an entry. Both ends are authorized, so an entry cannot be moved
// out of the boundary and a destination outside it cannot be created.
func (s *Service) Rename(ctx context.Context, path, newPath string) error {
	source, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return err
	}
	target, err := s.roots.Resolve(newPath, ModeCreate)
	if err != nil {
		return err
	}

	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%w: %s already exists", ErrConflict, newPath)
	} else if !os.IsNotExist(err) {
		return classifyPathError(newPath, err)
	}

	return classifyPathError(newPath, os.Rename(source, target))
}

// Delete removes an entry. A directory that still contains something is refused
// unless the caller asked for it to go recursively.
func (s *Service) Delete(ctx context.Context, path string, recursive bool) error {
	resolved, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return err
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		return classifyPathError(path, err)
	}
	if !info.IsDir() {
		return classifyPathError(path, os.Remove(resolved))
	}
	if !recursive {
		children, err := os.ReadDir(resolved)
		if err != nil {
			return classifyPathError(path, err)
		}
		if len(children) > 0 {
			return fmt.Errorf("%w: %s", ErrDirNotEmpty, path)
		}
		return classifyPathError(path, os.Remove(resolved))
	}
	if err := os.RemoveAll(resolved); err != nil {
		return fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	return nil
}

// cancelableReader stops a transfer once the caller has gone away, rather than
// finishing a write nobody is waiting for.
type cancelableReader struct {
	ctx  context.Context
	body io.Reader
}

func (r cancelableReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}

// mtimeOf reports a path's modification time in milliseconds since the epoch.
// Nanoseconds would be finer but unusable: Go's UnixNano exceeds JavaScript's
// Number.MAX_SAFE_INTEGER and would be silently corrupted by a JSON client.
func mtimeOf(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, classifyPathError(path, err)
	}
	return info.ModTime().UnixMilli(), nil
}
