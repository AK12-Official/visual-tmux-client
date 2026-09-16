package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The property the handle exists for, and the one a string-path call cannot
// offer: the kernel resolves nothing of the caller's spelling at act time,
// because there is no spelling -- only a descriptor and a path within it.
//
// This hands the confinement a path whose components are *not* canonical, which
// is exactly the state a check-then-act window leaves behind, and shows the call
// refusing it rather than following it out of the tree.
func TestAConfinedPathIsRefusedForLeadingOutOfItsRoot(t *testing.T) {
	outside := sandbox(t)
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")

	root := sandbox(t)
	mustSymlink(t, outside, filepath.Join(root, "escape"))

	// A plain call on the same spelling follows the link, which is what makes the
	// refusal below worth anything -- it is the answer every operation gave before
	// they were performed through a handle.
	if _, err := os.Stat(filepath.Join(root, "escape", "secret.txt")); err != nil {
		t.Fatalf("the escaping link should be followable by a plain path: %v", err)
	}

	svc := mustRootedService(t, root)
	confined := svc.roots.Confine(filepath.Join(root, "escape", "secret.txt"))

	if _, err := stat(confined); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected a path through the link to be refused, got: %v", err)
	}
	// And the target is untouched, which is the whole point: the refusal happened
	// before anything was opened or moved.
	if got := readFile(t, filepath.Join(outside, "secret.txt")); got != "secret" {
		t.Errorf("the escaping path was acted on: %q", got)
	}
}

// Acting through a handle must not change what a configured boundary *allows*:
// the ordinary paths inside a root go on working, including a link that stays
// inside it.
func TestAConfinedPathInsideTheRootIsUnchanged(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "real.txt"), "contents")
	mustSymlink(t, filepath.Join(root, "real.txt"), filepath.Join(root, "alias.txt"))

	svc := mustRootedService(t, root)
	// The canonical form of a link that stays inside resolves to its target, so
	// this is the path the guard actually authorizes.
	resolved, err := svc.roots.Resolve(filepath.Join(root, "alias.txt"), ModeRead)
	if err != nil {
		t.Fatalf("resolving a link inside the root failed: %v", err)
	}
	info, err := stat(svc.roots.Confine(resolved))
	if err != nil {
		t.Fatalf("stat through the handle failed: %v", err)
	}
	if info.Size() != int64(len("contents")) {
		t.Errorf("expected the target's size, got %d", info.Size())
	}
}

// The staging record is keyed by the canonical absolute path even though the
// syscall is given a root-relative one. A listing looks the record up by the
// path *it* resolved, so a mismatch would do more than leak a name: a user would
// watch a write's staging file appear and vanish in a rooted hub.
func TestARootedListingStillHidesAStagingFile(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "a.txt"), testBody)
	svc := mustRootedService(t, root)
	ctx := context.Background()

	before, err := svc.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var during []string
	body := &raceReader{
		inner: strings.NewReader("replacement"),
		race: func() {
			inner, lerr := svc.List(ctx, root)
			if lerr != nil {
				t.Errorf("listing during a write failed: %v", lerr)
				return
			}
			during = names(inner)
		},
	}
	if _, err := svc.Write(ctx, filepath.Join(root, "a.txt"), body,
		int64(len("replacement")), nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	if during == nil {
		t.Fatal("the listing taken during the write did not run")
	}
	if got := names(before); !slices.Equal(during, got) {
		t.Errorf("a rooted listing taken during a write showed %v, want %v", during, got)
	}
}

// A root is opened as a directory handle, so a root that is a file is refused
// when the set is built rather than when the first operation reaches it.
func TestNewRootSetRefusesARootThatIsNotADirectory(t *testing.T) {
	root := sandbox(t)
	file := filepath.Join(root, "a.txt")
	mustWrite(t, file, "contents")

	if set, err := NewRootSet([]string{file}); err == nil {
		set.Close()
		t.Error("expected a root that is not a directory to be refused")
	}
}

// The escape mapping has to survive whatever os.Root wraps its refusal in: the
// error is a bare errors.errorString with no sentinel, so it is matched by text
// and this is what would fail if that text ever changed.
func TestAnEscapingPathIsReportedAsMissing(t *testing.T) {
	outside := sandbox(t)
	root := sandbox(t)
	mustSymlink(t, outside, filepath.Join(root, "escape"))

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if _, err := stat(set.Confine(filepath.Join(root, "escape"))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected the refusal to read as not-found, got: %#v", err)
	}
	// The address of the failure is kept, so a message can still name it.
	var pathErr *fs.PathError
	if _, err := stat(set.Confine(filepath.Join(root, "escape"))); !errors.As(err, &pathErr) {
		t.Errorf("expected a path error, got: %#v", err)
	}
}

// Confine is the last place that can catch a path outside every root, and it
// must not be the place that lets one through. A Confined with no handle means
// "this hub has no boundary", which is a different thing from "this path is not
// inside the boundary" -- and the second must be refused rather than quietly
// turning into the first.
func TestConfineRefusesAPathOutsideEveryRoot(t *testing.T) {
	root := sandbox(t)
	outside := sandbox(t)
	file := filepath.Join(outside, "a.txt")
	mustWrite(t, file, "contents")

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if _, err := stat(set.Confine(file)); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected a path outside the root to be refused, got: %v", err)
	}
	if err := removeAll(set.Confine(filepath.Join(outside, "whole"))); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected a destructive call to refuse it too, got: %v", err)
	}

	// A hub with no roots is the case that legitimately has no handle, and it
	// goes on acting on ordinary paths.
	unrestricted, err := NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unrestricted.Close()
	if _, err := stat(unrestricted.Confine(file)); err != nil {
		t.Errorf("an unrestricted hub must act on ordinary paths, got: %v", err)
	}
}
