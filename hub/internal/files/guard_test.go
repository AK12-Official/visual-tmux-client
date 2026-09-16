package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		for _, path := range []string{"/proc/self/environ", "/proc", "/sys/kernel", "/dev/null"} {
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
