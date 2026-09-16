package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
}

// NewRootSet canonicalizes each configured root. A root that cannot be resolved
// is an error rather than a silent skip: a boundary nobody can be placed inside
// would refuse every operation instead of confining anything.
func NewRootSet(roots []string) (*RootSet, error) {
	set := &RootSet{}
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", root, err)
		}
		set.roots = append(set.roots, resolved)
	}
	return set, nil
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

	resolved, err := canonicalize(path)
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
		if _, err := os.Stat(resolved); err != nil {
			return "", classifyPathError(path, err)
		}
	}
	return resolved, nil
}

// contains reports whether resolved lies inside one of the roots. It compares
// path elements rather than string prefixes, so a root of /tmp/x/root does not
// appear to contain /tmp/x/root-backup.
func (s *RootSet) contains(resolved string) bool {
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
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
func canonicalize(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", classifyPathError(path, err)
	}
	return canonicalizeMissing(path)
}

// canonicalizeMissing resolves the deepest existing ancestor of a path that
// does not exist and re-appends what is missing. It walks upwards rather than
// assuming a fixed depth, so a create several directories below the nearest
// existing one is still authorized against where it would really land.
func canonicalizeMissing(path string) (string, error) {
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

// isBlocked reports whether a path names a kernel interface, matching whole
// path elements so that /devices is not mistaken for /dev.
func isBlocked(path string) bool {
	for _, prefix := range blockedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
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
	default:
		return err
	}
}
