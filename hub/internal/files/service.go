package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unicode/utf8"
)

const (
	// tempFilePrefix marks the sibling a write stages through before renaming it
	// over the target. It is recognisable so that a leftover one from an
	// interrupted run can be identified, and hidden so it does not clutter a
	// listing.
	tempFilePrefix = ".vtc-write-"

	// stagingNameAttempts bounds how many names are tried before a write gives up.
	// A collision needs two writers to draw the same 64 random bits.
	stagingNameAttempts = 100

	// stagingMode is what a staging file is created with when it will be given the
	// target's mode later. Being private while it is incomplete is the point: an
	// unfinished copy is not something anyone else has any business reading.
	stagingMode = 0o600

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

	// stagingMu guards staging, which holds the staging files that exist right
	// now. It is what lets a listing omit a file this service is in the middle of
	// writing, without having to recognise the name of one it is not.
	stagingMu sync.Mutex
	staging   map[string]struct{}
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

// createStaged creates the staging file and records it in one step.
//
// mode is what the file ends up with if it reaches the target. Applying it at
// creation rather than with a later chmod is what lets the process's umask govern
// a *new* file, exactly as it governs one created directly: a chmod ignores the
// umask, and would leave files more permissive than the operator asked for.
//
// The name comes from the system's random source rather than a counter, so it
// cannot be predicted and pre-created to interfere with a write.
func (s *Service) createStaged(dir string, mode os.FileMode) (*os.File, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	for range stagingNameAttempts {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, tempFilePrefix+hex.EncodeToString(suffix[:]))
		stage, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			if s.staging == nil {
				s.staging = make(map[string]struct{})
			}
			s.staging[name] = struct{}{}
			return stage, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no free staging name in %s", ErrWriteFailed, dir)
}

// unmarkStaging forgets a staging file, which is what makes it an ordinary
// directory entry again -- whether it was renamed onto the target or removed.
func (s *Service) unmarkStaging(path string) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	delete(s.staging, path)
}

// isStaging reports whether a path is a file this service is writing right now.
func (s *Service) isStaging(path string) bool {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	_, found := s.staging[path]
	return found
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
		// A staging file belongs to a write that is in flight, and the listing is
		// exactly where a user would otherwise watch it appear and vanish. Only a
		// file this service is writing right now is omitted: recognising one by
		// its name instead would hide a file the user named that way, and put it
		// beyond the only interface that could remove it.
		//
		// The record is keyed by the path a write resolved, and the lookup by the
		// path a listing resolved. Both come from the same resolver, so they agree
		// -- except across two spellings that differ only in case on a
		// case-insensitive volume, where a listing can still catch the file. That
		// costs a transient entry in a listing nobody asked for twice; closing it
		// needs the directory's identity rather than its name.
		if s.isStaging(filepath.Join(resolved, child.Name())) {
			continue
		}
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
// The size is checked against the configured limit before any bytes are served,
// so an oversized file transfers nothing.
func (s *Service) Read(ctx context.Context, path string) (ReadResult, error) {
	resolved, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return ReadResult{}, err
	}

	file, err := os.Open(resolved)
	if err != nil {
		return ReadResult{}, classifyPathError(path, err)
	}
	// Everything below comes from the opened descriptor rather than a stat taken
	// beforehand, so the limit and the reported metadata describe the bytes this
	// call will actually serve. A stat first would leave a window in which the
	// file grows past the limit, or is replaced, between the check and the open.
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() //nolint:errcheck // the stat error is the one worth reporting
		return ReadResult{}, classifyPathError(path, err)
	}
	if info.IsDir() {
		_ = file.Close() //nolint:errcheck // the kind error is the one worth reporting
		return ReadResult{}, fmt.Errorf("%w: %s is a directory", ErrInvalidPath, path)
	}
	if info.Size() > s.maxFileSize {
		_ = file.Close() //nolint:errcheck // the size error is the one worth reporting
		return ReadResult{}, fmt.Errorf("%w: %s is %d bytes, over the %d byte limit",
			ErrFileTooLarge, path, info.Size(), s.maxFileSize)
	}

	binary, err := looksBinary(file, info.Size())
	if err != nil {
		_ = file.Close() //nolint:errcheck // the sample error is the one worth reporting
		return ReadResult{}, classifyPathError(path, err)
	}

	return ReadResult{
		File:   file,
		Size:   info.Size(),
		Mtime:  info.ModTime().UnixMilli(),
		Binary: binary,
	}, nil
}

// sniffBytes bounds how much of a file is examined to decide whether it is text.
// A prefix is enough: a file that is text throughout starts as text, and every
// format the browser would otherwise mangle declares itself early.
const sniffBytes = 8192

// looksBinary reports whether a file's contents are not text.
//
// The caller cannot render binary bytes as text without replacing them with
// something the file never contained, so the hub answers the question rather
// than leaving the browser to guess from the file's name -- a name is a guess,
// and a wrong guess either mojibakes a file or refuses to open a log.
//
// The sample is read with ReadAt, which does not move the file offset, so the
// caller still streams the whole file from the beginning.
func looksBinary(file *os.File, size int64) (bool, error) {
	if size == 0 {
		return false, nil
	}
	n := min(int64(sniffBytes), size)
	sample := make([]byte, n)
	read, err := file.ReadAt(sample, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	// A sample shorter than the file stopped at the bound rather than at the end,
	// which is the case that can end inside a rune.
	return binarySample(sample[:read], size <= int64(sniffBytes)), nil
}

// binarySample classifies a sample. A NUL byte is decisive -- no text encoding
// the browser can read contains one -- and anything that is not valid UTF-8 is
// treated as binary, since that is what the browser would decode into
// replacement characters and then save back over the original bytes.
//
// complete says whether the sample is the whole file. It matters because a
// sample that stopped at the sniff bound can end inside a rune, and that is not
// the same thing as bytes that are not UTF-8 at all.
func binarySample(sample []byte, complete bool) bool {
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if utf8.Valid(sample) {
		return false
	}
	if complete {
		return true
	}
	// Only a truncated rune is tolerated: the bytes after the last complete rune
	// have to be the start of a valid encoding, not merely short enough that what
	// precedes them happens to parse.
	for drop := 1; drop <= 3 && drop <= len(sample); drop++ {
		if utf8.Valid(sample[:len(sample)-drop]) && isPartialRune(sample[len(sample)-drop:]) {
			return false
		}
	}
	return true
}

// isPartialRune reports whether b is the beginning of a UTF-8 encoding that has
// been cut short, as opposed to bytes that are not UTF-8 at all.
func isPartialRune(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	var width int
	switch lead := b[0]; {
	case lead >= 0xC2 && lead <= 0xDF:
		width = 2
	case lead >= 0xE0 && lead <= 0xEF:
		width = 3
	case lead >= 0xF0 && lead <= 0xF4:
		width = 4
	default:
		return false
	}
	if len(b) >= width {
		return false // complete, so nothing here was truncated
	}
	for _, continuation := range b[1:] {
		if continuation < 0x80 || continuation > 0xBF {
			return false
		}
	}
	return true
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
	if !exists {
		// A name held by a link whose target is gone is not a name to write over.
		// The rename that commits the write would replace the link, destroying
		// where it pointed without saying so; the caller asked to write a file,
		// not to remove a link. Reading it fails on its own, so nothing else can
		// act on it either, and "no such target" is what is true of it.
		if info, linkErr := os.Lstat(resolved); linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("%w: %s is a link whose target does not exist", ErrNotFound, path)
		}
	}
	if expectedMtime != nil {
		if !exists {
			return 0, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		if existing.ModTime().UnixMilli() != *expectedMtime {
			return 0, fmt.Errorf("%w: %s changed since it was read", ErrConflict, path)
		}
	}

	// The replacement carries the target's permission bits. Staging through a
	// fresh file would otherwise reset them: a temporary file is created without
	// any of them, so renaming it over a 0644 file would quietly make it private
	// and over an executable script would strip the bit that lets it run.
	//
	// Only the permission bits, deliberately. The staging file is owned by the
	// hub's user, so the replacement is too, and carrying setuid or setgid across
	// would not preserve a capability -- it would hand one to whoever just wrote
	// the file. Dropping them is what a plain shell redirect does, and the cost is
	// a chmod the owner can reapply.
	opts := writeOptions{expectedMtime: expectedMtime, display: path}
	if exists {
		opts.mode = existing.Mode().Perm()
		opts.preserve = true
	} else {
		opts.mode = os.FileMode(createFileMode)
	}

	return s.writeAtomically(ctx, resolved, body, declaredSize, opts)
}

// writeOptions carries what the staging path needs beyond the body itself.
type writeOptions struct {
	// mode is what the file ends up with: the target's permission bits when it is
	// replacing one, the documented default when it is creating one.
	mode os.FileMode
	// preserve says mode describes an existing file rather than a new one. An
	// existing mode is a value to keep, so it is applied with chmod -- which the
	// umask does not reduce. A new file's mode is a default, so creation applies
	// it and lets the umask decide.
	preserve bool
	// expectedMtime is the modification time the caller last observed, re-checked
	// immediately before the target is replaced.
	expectedMtime *int64
	// display is the caller's own spelling of the path, for error messages.
	display string
}

// writeAtomically stages the body in a sibling of the target and renames it
// over the target only once every byte is on disk. A failure at any point
// leaves the target exactly as it was, which is the property a plain truncate
// and write cannot offer.
func (s *Service) writeAtomically(
	ctx context.Context, target string, body io.Reader, declaredSize int64, opts writeOptions,
) (int64, error) {
	// A new file is created with the mode it will keep, so the umask governs it
	// exactly as it would govern a file created directly. The cost is that an
	// unfinished copy of a *new* file carries whatever that mode allows, in a
	// directory the writer can already list -- it is readable by anyone who can
	// read the directory, during the upload, by its random name. A replacement
	// does not have that exposure: it is created private and given the target's
	// mode once the body is complete, so an unfinished copy is never more
	// readable than the file it will replace.
	createMode := opts.mode
	if opts.preserve {
		createMode = stagingMode
	}
	stage, err := s.createStaged(filepath.Dir(target), createMode)
	if err != nil {
		return 0, stageFailure(opts.display, err)
	}
	defer s.unmarkStaging(stage.Name())

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
	// A replacement keeps the target's mode exactly, which the umask must not
	// reduce -- so it is set here, after the body and before the sync, rather than
	// at creation.
	if opts.preserve {
		if err := stage.Chmod(opts.mode); err != nil {
			_ = stage.Close() //nolint:errcheck // the chmod error is the one worth reporting
			return 0, fmt.Errorf("%w: chmod: %w", ErrWriteFailed, err)
		}
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close() //nolint:errcheck // the sync error is the one worth reporting
		return 0, fmt.Errorf("%w: sync: %w", ErrWriteFailed, err)
	}
	if err := stage.Close(); err != nil {
		return 0, fmt.Errorf("%w: close: %w", ErrWriteFailed, err)
	}

	// The caller's observed modification time is checked again here, and this is
	// the check that makes the write optimistic. The first one happened before
	// the body was transferred, which for a large file is as long as the request
	// lasts: a second writer landing inside that window would be overwritten
	// without either writer being told. What remains is the gap between this stat
	// and the rename below, two adjacent syscalls rather than a whole upload.
	if err := confirmUnchanged(target, opts.display, opts.expectedMtime); err != nil {
		return 0, err
	}

	if err := os.Rename(stage.Name(), target); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	committed = true

	return mtimeOf(target)
}

// confirmUnchanged re-applies the observed-modification-time check. A nil
// expected time is a forced overwrite and has nothing to confirm.
func confirmUnchanged(target, display string, expectedMtime *int64) error {
	if expectedMtime == nil {
		return nil
	}
	info, err := os.Stat(target)
	if err != nil {
		return classifyPathError(display, err)
	}
	if info.ModTime().UnixMilli() != *expectedMtime {
		return fmt.Errorf("%w: %s changed while it was being written", ErrConflict, display)
	}
	return nil
}

// stageFailure reports why a staging file could not be created. A target whose
// directory is missing, or is not writable, is the caller's situation rather
// than a server fault, so it is reported the way the other operations report it
// -- otherwise a write into a deleted directory answers 500 where a create into
// the same directory answers 404.
//
// A name too long is the same: the caller chose a path near the limit, and the
// staging prefix is what tipped it over, so it is their path to shorten.
func stageFailure(display string, err error) error {
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("%w: %s", ErrNotFound, display)
	case os.IsPermission(err):
		return fmt.Errorf("%w: %s", ErrPermissionDenied, display)
	case errors.Is(err, syscall.ENAMETOOLONG):
		return fmt.Errorf("%w: %s leaves no room for a staging file", ErrInvalidPath, display)
	default:
		return fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
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
//
// As with Delete, the entries acted on are the ones the caller named rather than
// the ones they resolve to: renaming a link must move the link, not the file it
// points at.
func (s *Service) Rename(ctx context.Context, path, newPath string) error {
	source, err := s.entryTarget(path)
	if err != nil {
		return err
	}
	target, err := s.entryTarget(newPath)
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

// entryTarget resolves a path into the canonical directory that holds the entry
// and the entry's name within it.
//
// Operations that act on a directory entry -- removing it, moving it -- must not
// let the kernel re-resolve the path's parents at syscall time. A link among
// them can be swapped after the boundary check, and the operation then lands
// wherever the swapped link points: authorizing one string and acting on another
// is what reintroduces that. Resolving the parent here and joining the final
// element to that already-canonical directory leaves no *link* in the acted-on
// path for a swap to redirect, and unlink and rename never follow the last
// element at all.
//
// Authorizing the parent is also the right question for these operations. The
// entry belongs to the directory that holds it, wherever a link at that name
// happens to point -- so a link leading outside the boundary can still be
// removed, and removing it cannot touch what it pointed at.
//
// The shape of the caller's own spelling is checked before anything is
// normalized, so that `..` is refused here exactly as the other operations
// refuse it. Collapsing it instead would make the same string denote one entry
// for this operation and a different one for the rest.
//
// A trailing slash is stripped by that normalization, so `link/` names the link
// rather than what it points at. That is a deliberate divergence from the shell,
// which follows: following here is what would let a delete reach the target.
func (s *Service) entryTarget(path string) (string, error) {
	if err := validatePathShape(path); err != nil {
		return "", err
	}
	cleaned := filepath.Clean(path)
	if cleaned == string(os.PathSeparator) {
		// The one path with nothing above it, and so not an entry in any
		// directory. No listing offers it, which leaves only a caller that named
		// it by accident -- and handing it to RemoveAll would ask the kernel to
		// delete the filesystem the hub is standing on.
		return "", fmt.Errorf("%w: the filesystem root is not an entry", ErrInvalidPath)
	}

	parent := filepath.Dir(cleaned)
	canonicalParent, err := s.roots.Resolve(parent, ModeRead)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonicalParent)
	if err != nil {
		return "", classifyPathError(path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, parent)
	}

	// The entry itself, not just its parent. Authorizing the parent covers every
	// path *under* a kernel interface -- the parent of /dev/shm is already
	// refused -- but the interface's own name has an ordinary parent, so /dev,
	// /proc and /sys would otherwise be reachable as entries. They are refused
	// before any root check, so no configuration can expose them.
	target := filepath.Join(canonicalParent, filepath.Base(cleaned))
	if isBlocked(target) {
		return "", fmt.Errorf("%w: %s is not reachable through the file API", ErrPathNotAllowed, path)
	}
	return target, nil
}

// Delete removes an entry. A directory that still contains something is refused
// unless the caller asked for it to go recursively.
func (s *Service) Delete(ctx context.Context, path string, recursive bool) error {
	target, err := s.entryTarget(path)
	if err != nil {
		return err
	}

	info, err := os.Lstat(target)
	if err != nil {
		return classifyPathError(path, err)
	}
	if !info.IsDir() {
		return classifyPathError(path, os.Remove(target))
	}
	if !recursive {
		children, err := os.ReadDir(target)
		if err != nil {
			return classifyPathError(path, err)
		}
		if len(children) > 0 {
			return fmt.Errorf("%w: %s", ErrDirNotEmpty, path)
		}
		return classifyPathError(path, os.Remove(target))
	}
	if err := os.RemoveAll(target); err != nil {
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
