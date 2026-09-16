package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// sandbox returns a canonical temporary directory. Resolving it up front keeps
// the expectations below honest on platforms where the OS temp root is itself
// reached through a symlink.
func sandbox(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewRootSetStoresAResolvedRoot(t *testing.T) {
	base := sandbox(t)
	target := filepath.Join(base, "real-root")
	mustMkdir(t, target)
	link := filepath.Join(base, "linked-root")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	set, err := NewRootSet([]string{link})
	if err != nil {
		t.Fatalf("NewRootSet failed: %v", err)
	}
	got := set.Roots()
	if len(got) != 1 || got[0] != target {
		t.Fatalf("expected the linked root to store %q, got %v", target, got)
	}
	if set.Unrestricted() {
		t.Error("a configured root must not report an unrestricted boundary")
	}
}

func TestNewRootSetRejectsARootItCannotResolve(t *testing.T) {
	missing := filepath.Join(sandbox(t), "absent-root")
	if _, err := NewRootSet([]string{missing}); err == nil {
		t.Fatal("expected a root that does not exist to be rejected")
	}
}

func TestNewRootSetWithNoRootsIsUnrestricted(t *testing.T) {
	set, err := NewRootSet(nil)
	if err != nil {
		t.Fatalf("NewRootSet failed: %v", err)
	}
	if !set.Unrestricted() || len(set.Roots()) != 0 {
		t.Errorf("expected an unrestricted boundary, got %v", set.Roots())
	}
}

func TestResolveRejectsMalformedPaths(t *testing.T) {
	base := sandbox(t)
	set, err := NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "relative", path: "etc/passwd"},
		{name: "traversal in the middle", path: base + "/../etc/passwd"},
		{name: "traversal as the last segment", path: base + "/.."},
		{name: "longer than the limit", path: filepath.Join(base, strings.Repeat("a", maxPathLength))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := set.Resolve(tc.path, ModeRead); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("expected an invalid-path error, got: %v", err)
			}
		})
	}

	// Two dots are only a traversal when they are the whole segment.
	if _, err := set.Resolve(filepath.Join(base, "a..b"), ModeRead); errors.Is(err, ErrInvalidPath) {
		t.Errorf("a name containing dots was mistaken for a traversal: %v", err)
	}
}

func TestResolveConstrainsToConfiguredRoots(t *testing.T) {
	base := sandbox(t)
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, root)
	mustMkdir(t, outside)
	mustWrite(t, filepath.Join(root, "inside.txt"), "inside")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	mustMkdir(t, filepath.Join(base, "root-backup"))
	mustWrite(t, filepath.Join(base, "root-backup", "other.txt"), "other")

	// A link to a file outside the root, and a link to the directory itself.
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatal(err)
	}

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		mode Mode
		want error
	}{
		{name: "inside a root", path: filepath.Join(root, "inside.txt"), mode: ModeRead},
		{name: "outside every root", path: filepath.Join(outside, "secret.txt"), mode: ModeRead,
			want: ErrPathNotAllowed},
		{name: "symlinked file escaping a root", path: filepath.Join(root, "escape-file"), mode: ModeRead,
			want: ErrPathNotAllowed},
		{name: "symlinked parent escaping a root", path: filepath.Join(root, "escape-dir", "secret.txt"),
			mode: ModeRead, want: ErrPathNotAllowed},
		{name: "sibling sharing only a prefix", path: filepath.Join(base, "root-backup", "other.txt"),
			mode: ModeRead, want: ErrPathNotAllowed},
		{name: "missing target inside a root", path: filepath.Join(root, "absent.txt"), mode: ModeRead,
			want: ErrNotFound},
		{name: "create below a parent that does not exist", path: filepath.Join(root, "new", "deep", "f.txt"),
			mode: ModeCreate},
		{name: "create below a symlinked parent that escapes",
			path: filepath.Join(root, "escape-dir", "new.txt"), mode: ModeCreate, want: ErrPathNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := set.Resolve(tc.path, tc.mode)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected the path to resolve, got: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got: %v", tc.want, err)
			}
		})
	}
}

// A path outside the boundary is refused the same way whether or not it exists.
// Reporting not-found first would let a caller map the filesystem through the
// difference between the two answers.
func TestResolveDecidesContainmentBeforeExistence(t *testing.T) {
	base := sandbox(t)
	root := filepath.Join(base, "root")
	mustMkdir(t, root)
	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	absent := filepath.Join(base, "definitely-absent.txt")
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("this test needs a path that does not exist: %v", err)
	}
	if _, err := set.Resolve(absent, ModeRead); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected a missing path outside the roots to be refused as not allowed, got: %v", err)
	}
}

func TestResolveUnrestrictedAcceptsAnyAbsolutePath(t *testing.T) {
	set, err := NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	// The OS temp root sits outside the home directory on both platforms this
	// project targets, which is the case the default boundary has to permit.
	base := sandbox(t)
	resolved, err := set.Resolve(base, ModeRead)
	if err != nil {
		t.Fatalf("an unrestricted boundary must accept %s, got: %v", base, err)
	}
	if resolved != base {
		t.Errorf("expected %q, got %q", base, resolved)
	}
}

func TestResolveAlwaysRefusesKernelInterfaces(t *testing.T) {
	for _, roots := range [][]string{nil, {"/"}} {
		set, err := NewRootSet(roots)
		if err != nil {
			t.Fatal(err)
		}
		// The differently-cased spellings are part of the list because the check
		// has to hold on a case-insensitive filesystem, where /DEV/null is the
		// same file as /dev/null and a case-sensitive test would let it through.
		paths := []string{
			"/proc/self/environ", "/proc", "/sys/kernel", "/dev/null",
			"/DEV/null", "/Proc/self/environ", "/SYS/kernel",
		}
		for _, path := range paths {
			if _, err := set.Resolve(path, ModeRead); !errors.Is(err, ErrPathNotAllowed) {
				t.Errorf("roots=%v path=%s: expected path_not_allowed, got: %v", roots, path, err)
			}
		}
		// A sibling whose name merely starts with a blocked prefix is ordinary.
		if _, err := set.Resolve(filepath.Join(sandbox(t), "devices"), ModeCreate); err != nil {
			t.Errorf("roots=%v: /devices was mistaken for /dev: %v", roots, err)
		}
	}
}

// A root on a kernel interface cannot be honoured, so it has to fail while the
// configuration loads rather than produce a hub whose every request is refused.
func TestNewRootSetRefusesARootOnAKernelInterface(t *testing.T) {
	for _, root := range []string{"/dev", "/dev/shm", "/proc/self", "/sys"} {
		if _, err := NewRootSet([]string{root}); err == nil {
			t.Errorf("expected %s to be refused as a root", root)
		}
	}
}

// A path that runs through a regular file is a shape the caller supplied, so it
// is a bad request rather than a server-side failure.
func TestResolveReportsAPathThroughAFileAsMalformed(t *testing.T) {
	base := sandbox(t)
	file := filepath.Join(base, "a.txt")
	mustWrite(t, file, "hello")
	set, err := NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = set.Resolve(filepath.Join(file, "child"), ModeRead)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected invalid_path for a path through a file, got: %v", err)
	}
}

// A create may not go *through* a link whose target is missing: the caller would
// be authorized here and the write would land wherever following the link leads.
// A link *at* the element is a different thing -- the name is taken, and the
// operations act on the entry rather than following it -- so refusing it would
// answer "not allowed" for a name the caller may use, where "already exists" is
// the truth.
func TestResolveRefusesToCreateThroughALinkWhoseTargetDoesNotExist(t *testing.T) {
	base := sandbox(t)
	link := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "nowhere"), link); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(base, "real"))
	mustSymlink(t, filepath.Join(base, "absent"), filepath.Join(base, "real", "broken"))

	set, err := NewRootSet([]string{base})
	if err != nil {
		t.Fatal(err)
	}

	// The element itself is an entry that exists, so every mode can name it.
	for _, mode := range []Mode{ModeRead, ModeCreate} {
		if _, err := set.Resolve(link, mode); err != nil {
			t.Errorf("mode=%v: a link at the element must resolve, got: %v", mode, err)
		}
	}

	// A link above the element leaves everything below it unreachable.
	below := filepath.Join(base, "real", "broken", "child.txt")
	if _, err := set.Resolve(below, ModeCreate); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected a create below a dangling link to be refused, got: %v", err)
	}
}

// A directory that was empty when it was checked and is not empty when it is
// removed is the same situation the explicit check reports, reached by a race
// instead. Both answer with the vocabulary's not-empty error rather than an
// internal failure.
func TestClassifyReportsANonEmptyDirectoryTheSameWayReachedByRace(t *testing.T) {
	raced := &os.PathError{Op: "remove", Path: "/srv/full", Err: syscall.ENOTEMPTY}
	if err := classifyPathError("/srv/full", raced); !errors.Is(err, ErrDirNotEmpty) {
		t.Fatalf("expected a not-empty error, got: %v", err)
	}
}

// A redundant separator is a spelling, not a different entry. Using filepath.Dir
// on a path with a trailing separator returns the element itself, which appended
// the final name twice -- so the same entry answered differently depending on
// whether it was written with a slash, and a name that did not exist resolved to
// a path under itself.
func TestResolveTreatsATrailingSeparatorAsTheSameEntry(t *testing.T) {
	base := sandbox(t)
	link := filepath.Join(base, "dangling")
	mustSymlink(t, filepath.Join(base, "nowhere"), link)
	set, err := NewRootSet([]string{base})
	if err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{link, link + "/", link + "//", link + "/."} {
		if _, err := set.Resolve(spelling, ModeCreate); err != nil {
			t.Errorf("%q must resolve like the entry it names, got: %v", spelling, err)
		}
	}

	got, err := set.Resolve(filepath.Join(base, "newdir")+"/", ModeCreate)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if want := filepath.Join(base, "newdir"); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
}

// Every classification is pinned, in both directions: a caller-shaped failure
// must reach the caller with a code they can act on, and a server fault must not
// be dressed up as one of theirs.
func TestClassifyPathErrorSeparatesCallerMistakesFromServerFaults(t *testing.T) {
	caller := []struct {
		errno syscall.Errno
		want  error
	}{
		{syscall.ENOENT, ErrNotFound},
		{syscall.EACCES, ErrPermissionDenied},
		{syscall.ENOTDIR, ErrInvalidPath},
		{syscall.ELOOP, ErrInvalidPath},
		{syscall.EINVAL, ErrInvalidPath},
		{syscall.ENAMETOOLONG, ErrInvalidPath},
		{syscall.ENOTEMPTY, ErrDirNotEmpty},
	}
	for _, tc := range caller {
		err := classifyPathError("/srv/x", &os.PathError{Op: "open", Path: "/srv/x", Err: tc.errno})
		if !errors.Is(err, tc.want) {
			t.Errorf("%v: got %v, want it to wrap %v", tc.errno, err, tc.want)
		}
	}

	for _, errno := range []syscall.Errno{syscall.EIO, syscall.ENOSPC, syscall.EROFS, syscall.EXDEV} {
		err := classifyPathError("/srv/x", &os.PathError{Op: "write", Path: "/srv/x", Err: errno})
		for _, wrong := range []error{ErrNotFound, ErrInvalidPath, ErrPermissionDenied, ErrConflict} {
			if errors.Is(err, wrong) {
				t.Errorf("%v: a server fault was reported as %v", errno, wrong)
			}
		}
	}
}

// The message is matched whole rather than by substring, so a path that merely
// contains its words cannot turn an unrelated server fault into a caller mistake.
// A component can be named anything, and the realistic trigger is a disk filling
// up underneath one.
func TestClassifyPathErrorIsNotFooledByAPathNamedAfterTheMessage(t *testing.T) {
	tricky := "/srv/too many links/file"
	err := classifyPathError(tricky, &os.PathError{Op: "write", Path: tricky, Err: syscall.ENOSPC})
	if errors.Is(err, ErrInvalidPath) {
		t.Error("a server fault under a path named after the message was reported as a caller mistake")
	}

	// The whole message, not the message anywhere in the text. A component can be
	// named exactly this too, and the path is the only part of a PathError the
	// caller chooses -- so the match has to be whole for the same reason.
	tricky = "/srv/" + tooManyLinksMessage + "/file"
	err = classifyPathError(tricky, &os.PathError{Op: "write", Path: tricky, Err: syscall.ENOSPC})
	if errors.Is(err, ErrInvalidPath) {
		t.Error("a server fault under a path named after the whole message was reported as a caller mistake")
	}
}

// A link that leads back to itself is a path the caller cannot use. It is the one
// case here that cannot be recognised by its errno -- the link walker reports it
// as a plain error rather than a *os.PathError -- so this builds a real loop,
// which is both the behaviour that matters and the thing that would catch the
// message it is recognised by changing.
func TestResolveReportsACircularLinkAsMalformed(t *testing.T) {
	base := sandbox(t)
	first := filepath.Join(base, "a")
	second := filepath.Join(base, "b")
	if err := os.Symlink(second, first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, second); err != nil {
		t.Fatal(err)
	}

	set, err := NewRootSet([]string{base})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, filepath.Join(first, "child.txt")} {
		if _, err := set.Resolve(path, ModeRead); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: expected a circular link to be reported as malformed, got: %v", path, err)
		}
	}
}
