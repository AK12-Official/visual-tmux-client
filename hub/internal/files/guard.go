package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// maxPathLength bounds a caller-supplied path. It matches the limit the
	// specification states rather than any filesystem's, so an over-long path is
	// refused before it reaches a syscall.
	maxPathLength = 4096
)

// Mode is what the caller intends to do with the resolved path, which decides
// whether the path itself has to exist.
type Mode int

const (
	// ModeRead is for an operation that acts on something already there: read,
	// list, download, delete, and the source of a rename.
	ModeRead Mode = iota
	// ModeCreate is for an operation that may name something that does not exist
	// yet: write, and the destination of a create or rename.
	ModeCreate
)

// blockedPrefixes are kernel interfaces rather than files. They are refused
// before any root check, so that no root configuration can expose them -- a
// read of /proc/self/environ is a memory disclosure, not a file read.
var blockedPrefixes = []string{"/proc", "/sys", "/dev"}

// tooManyLinksMessage is what the link walker says when a link leads back to
// itself. See tooManyLinks for why a message is the only thing to match.
const tooManyLinksMessage = "EvalSymlinks: too many links"

// RootSet is the set of canonical directories file operations are confined to.
//
// An empty one means no directory containment: the boundary is whatever the hub
// process's own operating-system user can reach. That is not a weaker boundary
// than it looks -- the same bearer token that authorizes this API also
// authorizes a byte-faithful interactive terminal as that same user, so a root
// list withholds no capability from someone who holds the token. It exists for
// operators who want containment anyway, and not as the thing that keeps a
// token holder out.
type RootSet struct {
	roots []string
	// handles are the open descriptors the roots are acted through, parallel to
	// roots. Acting through a handle is what closes the window between
	// authorizing a path and performing the operation on it: see Confined.
	handles []*os.Root
}

// NewRootSet canonicalizes each configured root. A root that cannot be resolved
// is an error rather than a silent skip: a boundary nobody can be placed inside
// would refuse every operation instead of confining anything.
//
// A root standing on a kernel interface is refused for the same reason, and
// here rather than in the guard: accepting it would start a hub whose every
// request under that root is refused, which is a dead feature rather than the
// startup failure the configuration is supposed to produce.
func NewRootSet(roots []string) (*RootSet, error) {
	set := &RootSet{}
	for _, root := range roots {
		if isBlocked(filepath.Clean(root)) {
			return nil, fmt.Errorf("root %q: %s is not reachable through the file API",
				root, filepath.Clean(root))
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", root, err)
		}
		if isBlocked(resolved) {
			return nil, fmt.Errorf("root %q: %s is not reachable through the file API", root, resolved)
		}
		set.roots = append(set.roots, resolved)
	}
	handles, err := openRoots(set.roots)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	set.handles = handles
	return set, nil
}

// Close releases the handles the roots are acted through. A hub holds them for
// its lifetime; tests hold one set per case and have to give them back, because
// each is a file descriptor.
func (s *RootSet) Close() {
	for _, handle := range s.handles {
		_ = handle.Close() //nolint:errcheck // nothing can be done about a descriptor that will not close
	}
	s.handles = nil
}

// Unrestricted reports whether no roots are configured.
func (s *RootSet) Unrestricted() bool {
	return len(s.roots) == 0
}

// Roots returns the canonical roots, in configuration order. An empty result
// means the boundary is unrestricted.
func (s *RootSet) Roots() []string {
	return append([]string(nil), s.roots...)
}

// Resolve is the single choke point every filesystem operation goes through. It
// returns the canonical path to act on, or the reason the operation must not
// happen.
//
// Resolution, not the caller's spelling, is what gets authorized: containment is
// decided against the path a symlink really leads to, so a link cannot be used
// to reach outside a root. A target that does not exist is resolved through its
// nearest existing ancestor, which is what keeps a write from following a
// symlinked parent out of the boundary.
func (s *RootSet) Resolve(path string, mode Mode) (string, error) {
	if err := validatePathShape(path); err != nil {
		return "", err
	}
	if isBlocked(path) {
		return "", fmt.Errorf("%w: %s is not reachable through the file API", ErrPathNotAllowed, path)
	}

	resolved, err := canonicalize(path, mode)
	if err != nil {
		return "", err
	}
	if isBlocked(resolved) {
		return "", fmt.Errorf("%w: %s is not reachable through the file API", ErrPathNotAllowed, path)
	}

	// Containment is decided before existence, so that a path outside the
	// boundary is refused the same way whether or not it happens to exist. The
	// other order would answer "not found" for a path the caller may not name.
	if !s.Unrestricted() && !s.contains(resolved) {
		return "", fmt.Errorf("%w: %s is outside every configured root", ErrPathNotAllowed, path)
	}

	if mode == ModeRead {
		// Lstat rather than Stat: the caller named an entry, and an entry that
		// exists is enough for a read to be attempted, for a link to be renamed,
		// and for a link to be removed. Following it is the operation's own
		// business -- reading a link whose target is gone fails on its own, and
		// refusing here would leave a broken link impossible to delete.
		if _, err := os.Lstat(resolved); err != nil {
			return "", classifyPathError(path, err)
		}
	}
	return resolved, nil
}

// contains reports whether resolved lies inside one of the roots.
func (s *RootSet) contains(resolved string) bool {
	for _, root := range s.roots {
		if _, ok := relativeTo(root, resolved); ok {
			return true
		}
	}
	return false
}

// validatePathShape rejects the spellings that cannot be authorized at all,
// before any of them reaches a syscall.
func validatePathShape(path string) error {
	if path == "" {
		return fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q is not absolute", ErrInvalidPath, path)
	}
	if len(path) > maxPathLength {
		return fmt.Errorf("%w: path exceeds %d characters", ErrInvalidPath, maxPathLength)
	}
	for _, segment := range strings.Split(path, string(os.PathSeparator)) {
		if segment == ".." {
			return fmt.Errorf("%w: %q contains a parent-directory segment", ErrInvalidPath, path)
		}
	}
	return nil
}

// canonicalize resolves a path through every symlink it traverses. A target
// that does not exist cannot be resolved itself, so the nearest existing
// ancestor is resolved instead and the remaining segments are re-appended.
func canonicalize(path string, mode Mode) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", classifyPathError(path, err)
	}
	return canonicalizeMissing(path, mode)
}

// canonicalizeMissing resolves the deepest existing ancestor of a path that
// does not exist and re-appends what is missing. It walks upwards rather than
// assuming a fixed depth, so a create several directories below the nearest
// existing one is still authorized against where it would really land.
func canonicalizeMissing(path string, mode Mode) (string, error) {
	// Cleaned first, because the walk below uses filepath.Dir: on a path with a
	// trailing separator that returns the element itself rather than its parent,
	// which would append the final name twice and turn the same entry into two
	// different spellings -- one of which names a directory that cannot exist.
	path = filepath.Clean(path)

	var missing []string
	current := path
	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Nothing on the path exists, which an absolute path cannot reach:
			// the filesystem root always does.
			return filepath.Clean(path), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent

		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			// A path that cannot be resolved is missing its target, not missing
			// itself: it may exist as a link to something that is not there. A
			// create may not go through such a link -- the caller would be
			// authorized here and the write would land wherever following it
			// leads -- so it is refused.
			//
			// Only when the link is *above* the final element. A link at the
			// element itself is an entry that exists, and the operations act on
			// the entry rather than following it: refusing it would answer
			// "not allowed" for a name the caller is allowed to use, where
			// "already exists" is the truth. The guard has nothing to authorize
			// beyond the name, because nothing follows it.
			if mode == ModeCreate && len(missing) > 1 {
				if err := checkDanglingLink(current, missing[len(missing)-1], path); err != nil {
					return "", err
				}
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", classifyPathError(current, err)
		}
	}
}

// checkDanglingLink refuses a path whose first not-yet-existing segment is in
// fact an existing symlink to something that does not exist.
func checkDanglingLink(ancestor, segment, path string) error {
	info, err := os.Lstat(filepath.Join(ancestor, segment))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s is a link whose target does not exist", ErrPathNotAllowed, path)
}

// isBlocked reports whether a path names a kernel interface, matching whole
// path elements so that /devices is not mistaken for /dev.
//
// The comparison is case-insensitive because the filesystem need not be: on a
// case-insensitive volume /DEV/null IS /dev/null, and a case-sensitive test
// would wave it through. A differently-cased spelling on a case-sensitive
// filesystem names nothing that exists, so refusing it costs nothing.
func isBlocked(path string) bool {
	lowered := strings.ToLower(path)
	for _, prefix := range blockedPrefixes {
		if lowered == prefix || strings.HasPrefix(lowered, prefix+"/") {
			return true
		}
	}
	return false
}

// classifyPathError turns a filesystem error into the service's vocabulary.
func classifyPathError(path string, err error) error {
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	case os.IsPermission(err):
		return fmt.Errorf("%w: %s", ErrPermissionDenied, path)
	// A path that runs through a file rather than a directory, one whose links
	// lead in a circle, one whose links changed while they were being read, and
	// one with a component too long for the filesystem are all shapes the caller
	// supplied and can correct. Left unclassified they reach the transport's
	// fallback and are reported as a server-side write failure, which is the
	// wrong answer to a path that cannot be used.
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.ELOOP),
		errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENAMETOOLONG),
		tooManyLinks(err):
		return fmt.Errorf("%w: %s", ErrInvalidPath, path)
	// A directory that was empty when it was checked and is not empty when it is
	// removed is the same situation the explicit check reports, reached the other
	// way: an ordinary race, not a server fault.
	case errors.Is(err, syscall.ENOTEMPTY):
		return fmt.Errorf("%w: %s", ErrDirNotEmpty, path)
	default:
		return err
	}
}

// tooManyLinks reports whether the link walker gave up on a link that leads back
// to itself.
//
// It cannot be recognised the way every other case here is. What
// filepath.EvalSymlinks returns for a loop is a plain error value with no errno
// behind it at all, so the message is the only thing there is to match -- and it
// is matched whole rather than by substring, because the text arrives inside an
// *os.PathError that embeds the path: a component literally named after the
// message would otherwise turn an unrelated ENOSPC or EIO into a caller mistake.
// Matching it whole also keeps EMLINK, whose message is the same three words,
// out of this arm. The test that builds a real loop is what would catch the
// message changing.
func tooManyLinks(err error) bool {
	return err != nil && err.Error() == tooManyLinksMessage
}
