package files

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
//
// A missing root set becomes an empty one rather than staying nil, because every
// method here goes through it: a nil one would be a service that panics on the
// first call rather than a service with no boundary, and that is not a state this
// type offers. The composition root never takes this branch -- it always builds a
// set, empty when the configuration names no roots -- so this is for a caller
// that constructs a Service directly.
func NewService(opts Options) *Service {
	roots := opts.Roots
	if roots == nil {
		roots, _ = NewRootSet(nil) //nolint:errcheck // a set with no roots has nothing to resolve
	}
	return &Service{
		roots:         roots,
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
func (s *Service) createStaged(dir Confined, mode os.FileMode) (*os.File, Confined, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	for range stagingNameAttempts {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, Confined{}, err
		}
		stage := dir.join(tempFilePrefix + hex.EncodeToString(suffix[:]))
		file, err := openFile(stage, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			if s.staging == nil {
				s.staging = make(map[string]struct{})
			}
			// Keyed by the canonical absolute path rather than by whatever form
			// the syscall was given, because a listing looks the record up by the
			// path *it* resolved: the two have to be the same string for a
			// staging file to stay hidden while it is being written.
			s.staging[stage.abs] = struct{}{}
			return file, stage, nil
		}
		if !os.IsExist(err) {
			return nil, Confined{}, err
		}
	}
	return nil, Confined{}, fmt.Errorf("%w: no free staging name in %s", ErrWriteFailed, dir.abs)
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

// Close gives back the descriptors the configured roots are held through.
//
// A hub holds one per root for its lifetime, so this is the end of that
// lifetime: after it, every operation is refused rather than performed without
// the handle. Nothing forces a process to call it -- the operating system
// reclaims the descriptors at exit -- but a hub that is stopped and started
// again in one process, or embedded in a program that outlives it, would leak
// one per root per run, and a leaked descriptor on a mount point is also what
// keeps the mount from being released.
//
// Safe to call more than once, including while requests are in flight. See
// RootSet.Close.
func (s *Service) Close() {
	s.roots.Close()
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

	// Opened before it is asked what it is, so that the kind of thing this
	// listing will read comes from the descriptor rather than from a stat taken
	// beforehand -- the descriptor is what the reads will use either way.
	dir, err := openDir(s.roots.Confine(resolved))
	if err != nil {
		return ListResult{}, classifyPathError(path, err)
	}
	defer func() {
		_ = dir.Close() //nolint:errcheck // the listing has already been read
	}()

	info, err := dir.Stat()
	if err != nil {
		return ListResult{}, classifyPathError(path, err)
	}
	if !info.IsDir() {
		return ListResult{}, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, path)
	}

	// A staging file belongs to a write that is in flight, and the listing is
	// exactly where a user would otherwise watch it appear and vanish. Only a
	// file this service is writing right now is omitted: recognising one by its
	// name instead would hide a file the user named that way, and put it beyond
	// the only interface that could remove it.
	//
	// The record is keyed by the path a write resolved, and the lookup by the
	// path a listing resolved. Both come from the same resolver, so they agree --
	// except across two spellings that differ only in case on a case-insensitive
	// volume, where a listing can still catch the file. That costs a transient
	// entry in a listing nobody asked for twice; closing it needs the directory's
	// identity rather than its name.
	//
	// The skip is applied as the directory is read rather than to the result, so
	// that the bound counts entries the caller will be shown. Filtering
	// afterwards would count a staging file against the bound, and a directory
	// holding exactly as many entries as the bound permits would report itself
	// truncated -- while showing all of them -- for as long as a write to it was
	// in flight.
	target := s.roots.Confine(resolved)
	children, cut, err := readListing(ctx, dir, s.maxDirEntries, func(name string) bool {
		return s.isStaging(target.join(name).abs)
	})
	if err != nil {
		return ListResult{}, classifyPathError(path, err)
	}

	// Describing an entry can be work: a symbolic link is resolved through the
	// guard and then stat'd, and there may be as many of those as the bound
	// allows. Nothing below is worth finishing once the caller has gone.
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return ListResult{}, err
		}
		entries = append(entries, s.entryFor(target, child))
	}

	// The order is imposed here rather than taken from the reader. Reading in
	// batches to bound a listing's cost means the entries arrive in whatever
	// order the directory stores them, which is no order a caller can use.
	slices.SortStableFunc(entries, compareEntries)

	// At most one entry past the bound, which is what the flag was decided by.
	if len(entries) > s.maxDirEntries {
		entries = entries[:s.maxDirEntries]
	}
	return ListResult{Path: resolved, Entries: entries, Truncated: cut}, nil
}

// compareEntries orders a listing: directories before files, then by name.
func compareEntries(a, b Entry) int {
	if a.IsDir != b.IsDir {
		if a.IsDir {
			return -1
		}
		return 1
	}
	return cmp.Compare(a.Name, b.Name)
}

// readListing reads a directory until one entry past the listing bound has been
// kept, and reports whether it stopped with entries left unread.
//
// Stopping there is the whole point. os.ReadDir reads and sorts the entire
// directory before its caller can apply any bound, so a directory holding a
// hundred thousand entries costs a hundred thousand entries' worth of memory and
// blocks for as long as reading them takes -- to produce a listing that is then
// cut to the configured maximum. Reading in batches caps that cost at the bound
// itself, and leaves somewhere to notice that the caller has gone away.
//
// An entry left unread is not an error: it is what the truncated flag is for.
//
// skip is applied as entries arrive rather than to the result, so the bound
// counts what the caller will actually be shown. The entries it removes are the
// ones a write in flight has staged, of which there are as many as there are
// concurrent writes -- so a directory cannot be inflated into an unbounded read
// by naming files the way this service names its staging files.
func readListing(
	ctx context.Context, dir *os.File, limit int, skip func(name string) bool,
) ([]os.DirEntry, bool, error) {
	// One entry past the bound is all it takes to know there are more, and
	// reading exactly that many is what makes the bound the bound.
	want := limit + 1
	entries := make([]os.DirEntry, 0, want)
	for len(entries) < want {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		// A short batch is not the end of the directory -- the kernel fills what
		// the buffer holds -- so the loop keeps going until either the bound or
		// io.EOF says to stop.
		batch, err := dir.ReadDir(want - len(entries))
		for _, entry := range batch {
			if !skip(entry.Name()) {
				entries = append(entries, entry)
			}
		}
		if errors.Is(err, io.EOF) {
			return entries, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return entries, true, nil
}

// entryFor describes one child of the directory it was read from.
func (s *Service) entryFor(dir Confined, child os.DirEntry) Entry {
	entry := Entry{Name: child.Name()}

	info, err := s.childInfo(dir, child)
	if err != nil {
		// An entry that cannot be described still exists, so it is reported
		// without its size rather than taking the whole listing down.
		return entry
	}
	entry.IsDir = info.IsDir()
	// A directory reports no size: the size of a directory inode is meaningless
	// to a browser, and obtaining one would not change that.
	if !entry.IsDir {
		entry.Size = info.Size()
		entry.Mtime = info.ModTime().UnixMilli()
	}
	return entry
}

// childInfo describes a directory child, following a symbolic link to what it
// points at when the boundary permits one.
//
// Following matters because this is where "is it a directory" is decided, and a
// reader that does not follow reports a link to a directory as a file. The
// browser offers the first for expanding and the second for opening, so the
// wrong answer produces an entry that invites a click and then refuses it --
// which is how a symlinked working directory, an entirely ordinary arrangement,
// becomes unbrowsable.
//
// The link is resolved through the guard rather than with a bare stat, so a link
// leading outside every configured root is described as the link it is instead
// of answering with the size and modification time of a path the caller may not
// name.
func (s *Service) childInfo(dir Confined, child os.DirEntry) (os.FileInfo, error) {
	if child.Type()&os.ModeSymlink == 0 {
		return child.Info()
	}
	target, err := s.roots.Resolve(dir.join(child.Name()).abs, ModeRead)
	if err != nil {
		return nil, err
	}
	return stat(s.roots.Confine(target))
}

// Read opens a file for streaming and reports its size and modification time.
// The size is checked against the configured limit before any bytes are served,
// so an oversized file transfers nothing.
func (s *Service) Read(ctx context.Context, path string) (ReadResult, error) {
	resolved, err := s.roots.Resolve(path, ModeRead)
	if err != nil {
		return ReadResult{}, err
	}

	file, err := openDir(s.roots.Confine(resolved))
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
	if err := readableKind(path, info, s.maxFileSize); err != nil {
		_ = file.Close() //nolint:errcheck // the kind error is the one worth reporting
		return ReadResult{}, err
	}

	binary, err := looksBinary(ctx, file, info.Size())
	if err != nil {
		_ = file.Close() //nolint:errcheck // the sample error is the one worth reporting
		return ReadResult{}, classifyPathError(path, err)
	}

	return ReadResult{
		File:       file,
		Size:       info.Size(),
		Mtime:      info.ModTime().UnixMilli(),
		MtimeNanos: info.ModTime().UnixNano(),
		Binary:     binary,
	}, nil
}

// readableKind reports why a file may not be read, if it may not.
//
// Only regular files are readable. The kind matters as much as the size: a named
// pipe, a socket, or a device is not a file with contents that a browser could
// show, and reading one is a different operation with different consequences. A
// pipe would block a request goroutine until a writer appeared -- which, for a
// pipe nothing is writing to, is never -- and a device answers with whatever it
// produces rather than with what it holds.
func readableKind(path string, info os.FileInfo, maxFileSize int64) error {
	if info.IsDir() {
		return fmt.Errorf("%w: %s is a directory", ErrInvalidPath, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrInvalidPath, path)
	}
	if info.Size() > maxFileSize {
		return fmt.Errorf("%w: %s is %d bytes, over the %d byte limit",
			ErrFileTooLarge, path, info.Size(), maxFileSize)
	}
	return nil
}

// scanChunk bounds how much of a file is held in memory at a time while
// deciding whether its contents are text. It is a working-set bound rather than
// a decision bound: the whole file is examined either way.
const scanChunk = 32 * 1024

// looksBinary reports whether a file's contents are not text.
//
// The caller cannot render binary bytes as text without replacing them with
// something the file never contained, so the hub answers the question rather
// than leaving the browser to guess from the file's name -- a name is a guess,
// and a wrong guess either mojibakes a file or refuses to open a log.
//
// The whole file decides, not a prefix of it. A prefix is a guess of the same
// kind: text that turns binary further in is ordinary -- a log with a binary
// record appended, a source file with an embedded blob -- and a reader told the
// file is text decodes the rest of it into replacement characters, which the
// next save writes back over the bytes that were there. Scanning costs
// little more than the sample it replaces: a binary file is refuted by its first
// decisive byte, and most have one in the first few kilobytes, so only a file
// that stays text to its end is read to its end -- which is exactly the file
// whose classification has to be right before it is offered as editable.
//
// Reading uses ReadAt, which does not move the file offset, so the caller still
// streams the whole file from the beginning.
func looksBinary(ctx context.Context, file *os.File, size int64) (bool, error) {
	if size == 0 {
		return false, nil
	}
	buf := make([]byte, scanChunk)
	var scan textScan
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		read, err := file.ReadAt(buf[:min(int64(len(buf)), size-offset)], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		if read == 0 {
			// The file is shorter than it said it was. What is there has been
			// examined, and it is the whole of it.
			break
		}
		offset += int64(read)
		if scan.decidedBy(buf[:read]) {
			return true, nil
		}
	}
	// Bytes still held back at the end of the file were a rune the end of the
	// file cut in half. No encoding produces those, so they are not text.
	return len(scan.carry) > 0, nil
}

// textScan classifies a file's contents as they are read, one chunk at a time.
//
// It exists because a decision about a whole file has to survive being reached
// in pieces. A multi-byte character can straddle a chunk boundary, and the two
// chunks around it are neither of them valid UTF-8 on their own -- the first
// ends inside a rune, the second begins with its continuation bytes. A scanner
// that judged each chunk alone would call perfectly ordinary text binary at
// whichever boundary it happened to land on, and the position of that boundary
// is a buffer size rather than anything about the file.
type textScan struct {
	// carry is the tail of the last chunk that could be a rune cut in half,
	// held back to be decided against the bytes that follow it.
	carry []byte
}

// decidedBy reports whether a chunk proves the contents are not text, holding
// back a trailing rune that the chunk may have cut in half.
//
// A NUL byte is decisive -- no text encoding the browser can read contains one
// -- and so is a byte sequence that is not valid UTF-8, since that is what the
// browser would decode into replacement characters and then save back over the
// original bytes.
func (s *textScan) decidedBy(chunk []byte) bool {
	sample := chunk
	if len(s.carry) > 0 {
		sample = append(append([]byte{}, s.carry...), chunk...)
		s.carry = nil
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if utf8.Valid(sample) {
		return false
	}
	// Invalid as it stands, which is either the answer or a consequence of where
	// this chunk ended. Only a truncated rune is tolerated: everything before it
	// must be valid, and the bytes after the last complete rune must be the start
	// of a valid encoding rather than merely short enough that what precedes them
	// happens to parse.
	for drop := 1; drop < utf8.UTFMax && drop <= len(sample); drop++ {
		tail := sample[len(sample)-drop:]
		if utf8.Valid(sample[:len(sample)-drop]) && isPartialRune(tail) {
			s.carry = append([]byte{}, tail...)
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

// writeTarget resolves the path a write names and applies every check that can be
// made before a byte of the body is transferred, so that a refusal costs the
// caller a round trip rather than an upload. It returns the target and what was
// found there -- nil for a name that was free -- or the refusal that stopped it.
//
// Each check is here for the reason it is stated at:
//
//   - the path must resolve inside the boundary, and a path it cannot stat is
//     classified rather than reported as a server fault;
//   - a directory is not a file to write;
//   - a name held by a link whose target is gone is not a name to write over: the
//     rename that commits the write would replace the link, destroying where it
//     pointed without saying so, and the caller asked to write a file rather than
//     to remove a link. Reading it fails on its own, so nothing else can act on
//     it either, and "no such target" is what is true of it;
//   - an observed time that does not match is a conflict, and one supplied for a
//     file that does not exist is a not-found: a caller that believes it is
//     editing an existing file must not be handed a new one;
//   - a target reachable under more than one name is refused unless the caller has
//     agreed, because the replacement writes the name it was given and leaves the
//     others holding the contents they had.
func (s *Service) writeTarget(
	path string, expected *ExpectedMtime, allowOtherNames bool,
) (Confined, os.FileInfo, error) {
	resolved, err := s.roots.Resolve(path, ModeCreate)
	if err != nil {
		return Confined{}, nil, err
	}
	target := s.roots.Confine(resolved)

	existing, statErr := stat(target)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			// Returned as it is: Write's own return classifies everything that
			// leaves it, which is the one place that has to be right. See Write.
			return Confined{}, nil, statErr
		}
		if info, linkErr := lstat(target); linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return Confined{}, nil, fmt.Errorf("%w: %s is a link whose target does not exist", ErrNotFound, path)
		}
		if expected != nil {
			return Confined{}, nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return target, nil, nil
	}
	if existing.IsDir() {
		return Confined{}, nil, fmt.Errorf("%w: %s is a directory", ErrInvalidPath, path)
	}
	if expected != nil && !expected.matches(existing) {
		return Confined{}, nil, fmt.Errorf("%w: %s changed since it was read", ErrConflict, path)
	}
	if err := linkedError(path, existing, allowOtherNames); err != nil {
		return Confined{}, nil, err
	}
	return target, existing, nil
}

// Write replaces a file's contents and reports what it produced.
//
// expected is the modification time the caller last observed. A mismatch is a
// conflict and nothing is written; a nil expected forces the overwrite, which is
// what the browser sends once the user has confirmed. A non-nil expected against
// a file that does not exist is a not-found rather than a create: a caller that
// believes it is editing an existing file must not be handed a new one.
//
// allowOtherNames is agreement to a replacement whose target is reachable under
// more than one name. Replacing a file replaces the inode, so a target with
// other names loses them: the entry the caller edited is written, and every
// other name keeps the contents it had. That is not a thing to do without
// saying so, and the caller is the only one who can say it -- so a linked target
// is refused until this is set, and the browser sets it once the user has
// confirmed.
func (s *Service) Write(
	ctx context.Context, path string, body io.Reader, declaredSize int64,
	expected *ExpectedMtime, allowOtherNames bool,
) (result WriteResult, err error) {
	// Every error this returns is classified here, once, rather than at each
	// place one can be raised. A handle closed by a shutdown can be met at any of
	// them -- and the two that matter are inside windows of two adjacent syscalls,
	// where no test can put it -- so a rule that has to be remembered at each site
	// is a rule that can be dropped at one of them without anything noticing.
	defer func() {
		if err != nil {
			err = classifyPathError(path, err)
		}
	}()

	target, existing, err := s.writeTarget(path, expected, allowOtherNames)
	if err != nil {
		return WriteResult{}, err
	}

	// The replacement carries the target's permission bits and, where the hub may
	// give them back, its owner. Staging through a fresh file would otherwise
	// reset them: a temporary file is created without any of them, so renaming it
	// over a 0644 file would quietly make it private and over an executable script
	// would strip the bit that lets it run. The owner is taken back by takeOwner,
	// which also says what happens where the hub may not.
	//
	// Only those two, deliberately. Setuid and setgid are not carried across: the
	// replacement is a file the writer just wrote, so preserving them would hand a
	// capability to whoever wrote it rather than keep one for whoever held it.
	// Dropping them is what a plain shell redirect does, and the cost is a chmod
	// the owner can reapply.
	//
	// Everything else the file carried goes with the inode it belonged to: the
	// replacement is a new file, so its ACLs and extended attributes are the empty
	// set a new file has. That is a consequence of *replacing* rather than
	// rewriting, and replacing is what makes a failed write leave the target
	// intact -- the property the specification requires. It states this cost where
	// it states the requirement, and both READMEs say the same in operator's words.
	opts := writeOptions{expected: expected, display: path, allowOtherNames: allowOtherNames}
	if existing != nil {
		// What the target was when this write began, whether or not the caller
		// supplied a time to compare it against. See confirmUnchanged.
		opts.origin = existing
		opts.mode = existing.Mode().Perm()
		opts.preserve = true
	} else {
		opts.mode = os.FileMode(createFileMode)
	}

	return s.writeAtomically(ctx, target, body, declaredSize, opts)
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
	// origin is the target as this write found it, or nil when the name was free.
	// It is what the target is checked against immediately before it is replaced,
	// so that a write against an entry that has since been moved, removed, or
	// taken by something else is refused rather than landing on whatever now
	// answers to that name.
	origin os.FileInfo
	// expected is the modification time the caller last observed, re-checked
	// immediately before the target is replaced. A nil one forces the overwrite
	// and is compared against nothing -- the origin check is what still has to
	// hold.
	expected *ExpectedMtime
	// display is the caller's own spelling of the path, for error messages.
	display string
	// allowOtherNames is the caller's agreement that a target reachable under
	// more than one name may be replaced, which leaves those other names holding
	// the contents they had. It is carried this far because the answer is asked
	// again at commit: a link can be made while the body travels.
	allowOtherNames bool
}

// writeAtomically stages the body in a sibling of the target and renames it
// over the target only once every byte is on disk. A failure at any point
// leaves the target exactly as it was, which is the property a plain truncate
// and write cannot offer.
func (s *Service) writeAtomically(
	ctx context.Context, target Confined, body io.Reader, declaredSize int64, opts writeOptions,
) (WriteResult, error) {
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
	stage, staged, err := s.createStaged(target.dir(), createMode)
	if err != nil {
		return WriteResult{}, stagingFailure(opts.display, err)
	}
	defer s.unmarkStaging(staged.abs)

	committed := false
	defer func() {
		if !committed {
			_ = remove(staged) //nolint:errcheck // best effort on a failure path
		}
	}()

	if err := s.copyBody(ctx, stage, body, declaredSize); err != nil {
		_ = stage.Close() //nolint:errcheck // the copy error is the one worth reporting
		return WriteResult{}, err
	}
	// A replacement keeps the target's mode exactly, which the umask must not
	// reduce -- so it is set here, after the body and before the sync, rather than
	// at creation.
	if opts.preserve {
		if err := stage.Chmod(opts.mode); err != nil {
			_ = stage.Close() //nolint:errcheck // the chmod error is the one worth reporting
			return WriteResult{}, fmt.Errorf("%w: chmod: %w", ErrWriteFailed, err)
		}
		// And the owner, where the hub may give it back. A replacement is a file
		// the hub created, so it belongs to the hub's user unless this succeeds --
		// which it does only for an owner the hub already is, or one it may adopt.
		// See takeOwner for what that leaves, and why it is not an error.
		if err := takeOwner(stage, opts.origin); err != nil {
			_ = stage.Close() //nolint:errcheck // the stat error is the one worth reporting
			return WriteResult{}, fmt.Errorf("%w: owner: %w", ErrWriteFailed, err)
		}
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close() //nolint:errcheck // the sync error is the one worth reporting
		return WriteResult{}, fmt.Errorf("%w: sync: %w", ErrWriteFailed, err)
	}
	if err := stage.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("%w: close: %w", ErrWriteFailed, err)
	}

	// The target is checked again here, and this is the check that makes the write
	// optimistic. The first one happened before the body was transferred, which for
	// a large file is as long as the request lasts: a second writer landing inside
	// that window would be overwritten without either writer being told. What
	// remains is the gap between this check and the rename below, two adjacent
	// syscalls rather than a whole upload.
	if err := confirmUnchanged(target, opts); err != nil {
		return WriteResult{}, err
	}

	if err := renameAt(staged, target); err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	committed = true

	return stampOf(target)
}

// confirmUnchanged re-applies, immediately before the target is replaced, every
// check that makes a write safe to land.
//
// Four things can have gone wrong while the body was travelling, and each is
// refused rather than resolved:
//
//   - the target is gone, or is no longer the file this write started against.
//     A forced overwrite used to skip this, on the reasoning that the user had
//     already agreed to replace whatever was there -- but agreeing to replace a
//     file is not agreeing to recreate one that has since been moved or removed.
//     A rename landing in this window is the case that matters: the entry comes
//     back at its old name, holding the edit, while the tab that asked to save it
//     has followed the file to the new name and is told the save succeeded.
//   - the target changed, when the caller said what it last observed.
//   - the name was free when the write began and no longer is, which is the same
//     hazard from the other side: a create that lands on a file created in the
//     meantime replaces something its author never agreed to lose.
//   - the target has gained another name, which is a name the caller was never
//     told about and would lose by the replacement. Asked last, after the
//     observed time, so that a file which both changed and is linked is answered
//     as a conflict first -- the browser asks about conflicts and about other
//     names with two different questions, and asking them in the order the answers
//     were captured in is what keeps one retry from dropping the other's.
//
// A nil expected modification time means the caller forced the overwrite, so it
// is compared against nothing. The origin check is not optional in the same way:
// it asks what the path is, not what the caller expected it to be.
func confirmUnchanged(target Confined, opts writeOptions) error {
	if opts.origin == nil {
		// The name was free when the write began, so it is still free only if
		// nothing has taken it.
		//
		// Lstat rather than Stat, because a name taken by a link whose target is
		// gone is taken. Stat follows the link, finds nothing where it points,
		// and reports the name as free -- so the write would replace the link,
		// which is the shape the check at the start of Write exists to refuse.
		if _, err := lstat(target); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return classifyPathError(opts.display, err)
		}
		return fmt.Errorf("%w: %s was created while it was being written", ErrConflict, opts.display)
	}
	current, err := stat(target)
	if err != nil {
		return classifyPathError(opts.display, err)
	}
	if !os.SameFile(opts.origin, current) {
		return fmt.Errorf("%w: %s was replaced while it was being written", ErrConflict, opts.display)
	}
	if opts.expected != nil && !opts.expected.matches(current) {
		return fmt.Errorf("%w: %s changed while it was being written", ErrConflict, opts.display)
	}
	return linkedError(opts.display, current, opts.allowOtherNames)
}

// linkedError is the refusal for a target that is reachable under more than one
// name, or nil when there is nothing to refuse.
//
// It is asked twice on the way to a replacement and both times in the same words:
// once before the body is transferred, so that a linked target costs a round trip
// rather than an upload, and once at commit, because a link made while the body
// travelled is a name the caller was never told about.
func linkedError(display string, info os.FileInfo, allowOtherNames bool) error {
	if allowOtherNames {
		return nil
	}
	other := otherNames(info)
	if other == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s is also reachable as %d other name(s)", ErrHasOtherNames, display, other)
}

// stagingFailure says why a staging file could not be created.
//
// The classification is the caller's -- every error leaving Write is classified
// once, at its return -- and what is here is the one thing the classifier cannot
// say: a name too long is a path the caller chose, and the staging prefix is what
// tipped it over, so the message names the staging file rather than the path.
func stagingFailure(display string, err error) error {
	if errors.Is(err, syscall.ENAMETOOLONG) {
		return fmt.Errorf("%w: %s leaves no room for a staging file", ErrInvalidPath, display)
	}
	return fmt.Errorf("%w: %w", ErrWriteFailed, err)
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
	target := s.roots.Confine(resolved)

	if info, statErr := stat(target.dir()); statErr != nil {
		return classifyPathError(filepath.Dir(path), statErr)
	} else if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, filepath.Dir(path))
	}

	// Lstat, so that a dangling symlink counts as an existing entry rather than
	// as a free name to create over.
	if _, err := lstat(target); err == nil {
		return fmt.Errorf("%w: %s already exists", ErrConflict, path)
	} else if !os.IsNotExist(err) {
		return classifyPathError(path, err)
	}

	if isDir {
		if err := mkdir(target, createDirMode); err != nil {
			return classifyPathError(path, err)
		}
		return nil
	}
	file, err := openFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, createFileMode)
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

	if _, err := lstat(target); err == nil {
		return fmt.Errorf("%w: %s already exists", ErrConflict, newPath)
	} else if !os.IsNotExist(err) {
		return classifyPathError(newPath, err)
	}

	return classifyPathError(newPath, renameAt(source, target))
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
func (s *Service) entryTarget(path string) (Confined, error) {
	if err := validatePathShape(path); err != nil {
		return Confined{}, err
	}
	cleaned := filepath.Clean(path)
	if cleaned == string(os.PathSeparator) {
		// The one path with nothing above it, and so not an entry in any
		// directory. No listing offers it, which leaves only a caller that named
		// it by accident -- and handing it to RemoveAll would ask the kernel to
		// delete the filesystem the hub is standing on.
		return Confined{}, fmt.Errorf("%w: the filesystem root is not an entry", ErrInvalidPath)
	}

	parent := filepath.Dir(cleaned)
	canonicalParent, err := s.roots.Resolve(parent, ModeRead)
	if err != nil {
		return Confined{}, err
	}
	parentPath := s.roots.Confine(canonicalParent)
	info, err := stat(parentPath)
	if err != nil {
		return Confined{}, classifyPathError(path, err)
	}
	if !info.IsDir() {
		return Confined{}, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, parent)
	}

	// The entry itself, not just its parent. Authorizing the parent covers every
	// path *under* a kernel interface -- the parent of /dev/shm is already
	// refused -- but the interface's own name has an ordinary parent, so /dev,
	// /proc and /sys would otherwise be reachable as entries. They are refused
	// before any root check, so no configuration can expose them.
	target := parentPath.join(filepath.Base(cleaned))
	if isBlocked(target.abs) {
		return Confined{}, fmt.Errorf("%w: %s is not reachable through the file API", ErrPathNotAllowed, path)
	}
	return target, nil
}

// Delete removes an entry. A directory that still contains something is refused
// unless the caller asked for it to go recursively.
func (s *Service) Delete(ctx context.Context, path string, recursive bool) (err error) {
	// Classified once, for the reason Write is: a handle closed by a shutdown can
	// be met at any of the places this can fail -- including the window between
	// the check that a directory is empty and the removal of it -- and a rule that
	// has to be remembered at each of them is a rule that can be dropped at one.
	defer func() {
		if err != nil {
			err = classifyPathError(path, err)
		}
	}()

	target, err := s.entryTarget(path)
	if err != nil {
		return err
	}

	info, err := lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return remove(target)
	}
	if !recursive {
		// One entry is all it takes to know, and reading more would make the cost
		// of refusing a directory the size of the directory. See dirHasEntries.
		hasEntries, err := dirHasEntries(target)
		if err != nil {
			return err
		}
		if hasEntries {
			return fmt.Errorf("%w: %s", ErrDirNotEmpty, path)
		}
		return remove(target)
	}
	if err := removeAll(target); err != nil {
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

// stampOf reports a path's modification time in each precision the service
// speaks: milliseconds, which a JSON client can carry exactly, and nanoseconds,
// which is what the filesystem records and what the next write is compared
// against.
func stampOf(target Confined) (WriteResult, error) {
	info, err := stat(target)
	if err != nil {
		return WriteResult{}, classifyPathError(target.abs, err)
	}
	return WriteResult{
		Mtime:      info.ModTime().UnixMilli(),
		MtimeNanos: info.ModTime().UnixNano(),
	}, nil
}
