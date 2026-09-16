package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// escapesFromParentMessage is what os.Root says when a path leads out of the
// tree the Root was opened on.
//
// It cannot be recognised any other way. What os.Root returns for an escaping
// path is a bare errors.errorString with no sentinel behind it and nothing for
// errors.Is to match -- the same shape, and the same problem, as the link-walker
// message in guard.go. The test that builds a real escaping link is what catches
// the wording changing.
const escapesFromParentMessage = "path escapes from parent"

// Confined is an authorized path in the form a syscall can be given without
// re-walking the caller's spelling.
//
// It closes the second of the two check-then-act windows the guard has. Resolve
// authorizes a path by resolving it, and then the kernel resolves the *string*
// again when the operation is performed, so a directory on that path replaced by
// a symbolic link in between sends the operation wherever the link points.
// Acting through an open handle on the containing root resolves every component
// below it as part of the operation itself, and os.Root refuses rather than
// follows anything that leaves the tree.
//
// The paths that reach here are canonical, so nothing below the root is a
// symbolic link except possibly the final element -- and the operations that
// must act on a link rather than follow it (removing it, moving it) are exactly
// the ones whose final element is never followed.
type Confined struct {
	// root is the open handle on the configured root that contains this path, or
	// nil when no roots are configured. Without a boundary there is nothing for a
	// replaced component to escape from, so those hubs go on acting on plain
	// paths.
	root *os.Root
	// path is what a syscall is given: relative to root, or the canonical
	// absolute path when root is nil.
	path string
	// abs is the canonical absolute path. It is what the staging record is keyed
	// by, and what a message should name, whichever form the call is handed.
	abs string
	// err is why this path cannot be acted on at all, or nil.
	//
	// A Confined with no handle means a hub with no boundary, which is a
	// legitimate state. It must not also be the answer for a path outside a
	// configured root, or the one function that exists to keep operations inside
	// the boundary would be the one that lets them out -- so that case carries an
	// error instead, and every helper below refuses before it reaches a syscall.
	err error
}

// join returns the entry called name inside a confined directory.
//
// The refusal is carried over, deliberately. Dropping it would make one
// derivation enough to turn a path this hub may not act on into one it acts on
// with no boundary at all -- which is the fail-open the error exists to stop,
// one step further away.
func (c Confined) join(name string) Confined {
	return Confined{
		root: c.root,
		path: filepath.Join(c.path, name),
		abs:  filepath.Join(c.abs, name),
		err:  c.err,
	}
}

// dir returns the directory holding a confined entry, carrying the refusal too.
func (c Confined) dir() Confined {
	return Confined{
		root: c.root,
		path: filepath.Dir(c.path),
		abs:  filepath.Dir(c.abs),
		err:  c.err,
	}
}

// The helpers below are the only way this package reaches the filesystem. Each
// one takes the confined form and dispatches on whether a boundary is in force,
// so the two modes cannot drift: an operation cannot be written against a plain
// path by accident, because there is no plain path in scope to write it against.

// escapedWithin maps os.Root's refusal to walk out of the tree onto not-found.
//
// The mapping is safe in the only direction it can be ambiguous. Once Resolve
// has authorized a canonical path, a Root call that refuses to walk it means the
// entry that name denotes is not reachable through the boundary -- which is what
// a dangling link is too, and is the answer every caller already gives for one.
// Leaving the raw error to reach the transport would report a client's path as a
// server fault.
func escapedWithin(err error) error {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// os.Root wraps its refusal in whatever error the operation uses -- a
	// PathError for stat and open, a LinkError for a rename -- so what is
	// rebuilt is the same wrapper with the cause replaced. Missing the LinkError
	// case would leave a refused rename reported as a server fault, which is the
	// worst place for it: a rename is exactly what a component replaced at the
	// wrong moment turns into.
	//
	// The wrapper is kept rather than converted to the simpler one. A rename
	// carries two paths, and a refusal does not say which of them left the tree
	// -- so collapsing it to a single Path means choosing, and choosing wrong for
	// the operations whose source is the escaping side. Both are also what a
	// message should be able to name.
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && refusesToLeave(pathErr.Err) {
		return &fs.PathError{Op: pathErr.Op, Path: pathErr.Path, Err: fs.ErrNotExist}
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) && refusesToLeave(linkErr.Err) {
		return &os.LinkError{Op: linkErr.Op, Old: linkErr.Old, New: linkErr.New, Err: fs.ErrNotExist}
	}
	return err
}

// refusesToLeave reports whether err, or anything it wraps, is os.Root saying
// the path leaves its tree.
func refusesToLeave(err error) bool {
	for err != nil {
		if err.Error() == escapesFromParentMessage {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// openFile opens a confined path with the given flags.
func openFile(c Confined, flag int, perm os.FileMode) (*os.File, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.root == nil {
		return os.OpenFile(c.path, flag, perm)
	}
	file, err := c.root.OpenFile(c.path, flag, perm)
	return file, escapedWithin(err)
}

// openDir opens a confined directory for reading.
//
// O_NONBLOCK rather than a plain open: a named pipe with no writer blocks in
// open(2) until one appears, and no context interrupts that wait, so a path that
// happens to name one would hold a request goroutine indefinitely. The flag has
// no effect on reading a regular file, and what was actually opened is decided
// from the descriptor straight afterwards.
func openDir(c Confined) (*os.File, error) {
	return openFile(c, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// stat reports a confined path's metadata, following the final element.
func stat(c Confined) (os.FileInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.root == nil {
		return os.Stat(c.path)
	}
	info, err := c.root.Stat(c.path)
	return info, escapedWithin(err)
}

// lstat reports a confined entry's metadata without following it.
func lstat(c Confined) (os.FileInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.root == nil {
		return os.Lstat(c.path)
	}
	info, err := c.root.Lstat(c.path)
	return info, escapedWithin(err)
}

// dirHasEntries reports whether a confined directory holds anything, reading no
// more than it must to answer that.
//
// The question is "is there at least one entry", and the whole-directory reader
// answers it by reading all of them -- sorted, in the unconfined branch. A
// directory holding a hundred thousand entries would cost a hundred thousand
// entries' worth of memory to decide that it is not empty. Delete asks this of a
// directory the caller has not asked to recurse into, so without this the cost of
// *refusing* is set by the size of what is being refused, which is the one place
// left where an operation's cost is the disk's rather than a configured limit's.
func dirHasEntries(c Confined) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	handle, err := openDir(c)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = handle.Close() //nolint:errcheck // the count has already been read
	}()
	entries, err := handle.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return len(entries) > 0, nil
}

// mkdir creates one confined directory.
func mkdir(c Confined, perm os.FileMode) error {
	if c.err != nil {
		return c.err
	}
	if c.root == nil {
		return os.Mkdir(c.path, perm)
	}
	return escapedWithin(c.root.Mkdir(c.path, perm))
}

// remove removes one confined entry, following nothing.
func remove(c Confined) error {
	if c.err != nil {
		return c.err
	}
	if c.root == nil {
		return os.Remove(c.path)
	}
	return escapedWithin(c.root.Remove(c.path))
}

// removeAll removes a confined entry and everything under it.
func removeAll(c Confined) error {
	if c.err != nil {
		return c.err
	}
	if c.root == nil {
		return os.RemoveAll(c.path)
	}
	return escapedWithin(c.root.RemoveAll(c.path))
}

// renameAt moves one confined entry onto another.
//
// Both ends of a rename within one root go through the handle: it is the final
// element of each path that a rename acts on and never follows, and the parents
// are resolved as part of the call.
//
// A move *between* two configured roots cannot be expressed that way at all --
// os.Root only moves within its own tree -- and it is refused rather than
// attempted. Performing it on the two absolute paths would resolve both of them
// again at syscall time, which is the one thing this function exists to avoid: a
// component of either path replaced by a symbolic link in between carries the
// entry out of the boundary, or brings one in, in whichever direction the link
// was planted. The primitive that would close it is a rename taking two
// directory descriptors, which this hub has no dependency for.
//
// Refusing costs the interface nothing, and the reason is worth stating
// precisely, because the obvious version of it is wrong. A rename from the
// browser is not necessarily a rename of a sibling: the destination is built
// from the source's own directory, but the name is free text and may descend,
// and a component of that descent may be a symbolic link into another configured
// root -- which is a path the boundary allows. Only then do the two ends sit in
// different roots, and only then does this refuse. The browser reports it, and
// the move the user asked for is one that crosses the operator's boundary
// anyway.
func renameAt(from, to Confined) error {
	if from.err != nil {
		return from.err
	}
	if to.err != nil {
		return to.err
	}
	// Neither end rooted is a hub with no boundary, where there is nothing for a
	// replaced component to escape from and the move is the ordinary one it has
	// always been. Exactly one end rooted cannot arise from Confine -- it yields
	// a handle for every path when roots are configured and for none when they
	// are not -- but it falls into the refusal below rather than being assumed
	// away, because assuming it away is what would put it on plain paths.
	if from.root == nil && to.root == nil {
		return os.Rename(from.abs, to.abs)
	}
	if from.root != to.root {
		return fmt.Errorf("%w: %s and %s are in different configured roots", ErrCrossRoot, from.abs, to.abs)
	}
	return escapedWithin(from.root.Rename(from.path, to.path))
}

// openRoots opens a handle on each configured root.
//
// The handle is what every operation below the root is performed through, so a
// root that cannot be opened is a startup failure rather than a hub whose every
// file operation would then be refused. Opening it also settles a question the
// guard only assumed: os.OpenRoot refuses a path that is not a directory.
func openRoots(resolved []string) ([]*os.Root, error) {
	handles := make([]*os.Root, 0, len(resolved))
	for _, root := range resolved {
		handle, err := os.OpenRoot(root)
		if err != nil {
			for _, opened := range handles {
				_ = opened.Close() //nolint:errcheck // the failure being reported is the one that matters
			}
			return nil, err
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

// Confine expresses an authorized canonical path the way a syscall can be given
// it: the handle on the root that contains it, together with the path relative
// to that root.
//
// A hub with no roots at all yields a Confined with no handle, which is the
// ordinary case rather than a failure: without a boundary there is nothing for a
// replaced component to escape from, so those hubs go on acting on plain paths.
// A path outside every configured root yields one carrying an error instead --
// see the err field for why that must not be the same representation.
func (s *RootSet) Confine(path string) Confined {
	// Read under the lock, because Close may be running: the hub closes the set
	// when it shuts down, and a handler can still be in flight at that point. See
	// RootSet.mu.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		// Closing is the end of the hub's lifetime, and an operation that reaches
		// here has outlived it -- a handler still running after the shutdown budget
		// expired. Refusing says so; acting without the handle would be the
		// boundary quietly disappearing, and indexing past the handles it no longer
		// has would take the process down instead of answering.
		return Confined{
			path: path,
			abs:  path,
			err:  fmt.Errorf("%w: %s: the root set is closed", ErrPathNotAllowed, path),
		}
	}
	for i, root := range s.roots {
		if rel, ok := relativeTo(root, path); ok {
			return Confined{root: s.handles[i], path: rel, abs: path}
		}
	}
	if len(s.roots) > 0 {
		// A path outside every root. Resolve refuses those, so reaching here means
		// a caller went around it; this is the last place that can say so.
		return Confined{
			path: path,
			abs:  path,
			err:  fmt.Errorf("%w: %s is outside every configured root", ErrPathNotAllowed, path),
		}
	}
	return Confined{path: path, abs: path}
}

// relativeTo reports whether resolved lies inside root, and where within it.
//
// It compares path elements rather than string prefixes, so a root of
// /tmp/x/root does not appear to contain /tmp/x/root-backup -- and it is the one
// place that decides containment, so the handle a path is acted through and the
// authorization that admitted it cannot disagree.
func relativeTo(root, resolved string) (string, bool) {
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return rel, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return rel, true
}
