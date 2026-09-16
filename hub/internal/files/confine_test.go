package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// A refusal must survive the wrapper the operation happened to use. os.Root
// reports a stat or an open through a PathError and a rename through a
// LinkError, and a rename is the operation a component replaced at the wrong
// moment is most likely to turn into -- so a refusal reported there as a server
// fault would be the worst place to lose it.
func TestARefusedRenameIsReportedAsARefusal(t *testing.T) {
	outside := sandbox(t)
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "contents")
	mustSymlink(t, outside, filepath.Join(root, "escape"))

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	// Both ends are inside the root as spellings, and one of them leads out of it.
	err = renameAt(set.Confine(filepath.Join(root, "a.txt")), set.Confine(filepath.Join(root, "escape", "b.txt")))
	if err == nil {
		t.Fatal("a rename through a link out of the root was allowed")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected the refusal to read as not-found rather than as a fault, got: %#v", err)
	}
	// Both ends are kept. A refusal does not say which side left the tree, so
	// collapsing the two into one path would mean guessing -- and the guess is
	// wrong for the operations whose source is the escaping side, which is the
	// case below.
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("expected the rename's own error shape, got: %#v", err)
	}
	if !strings.Contains(linkErr.New, "escape") {
		t.Errorf("expected the escaping end to be named, got old=%q new=%q", linkErr.Old, linkErr.New)
	}

	// The source can be the escaping side, and the error has to say so: the end
	// that left the tree is the one a message is about.
	mustMkdir(t, filepath.Join(root, "sub"))
	mustSymlink(t, outside, filepath.Join(root, "sub", "out"))
	mustWrite(t, filepath.Join(outside, "secret.txt"), "s")
	source := set.Confine(filepath.Join(root, "sub", "out", "secret.txt"))
	destination := set.Confine(filepath.Join(root, "sub", "moved.txt"))
	err = renameAt(source, destination)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected a source that leaves the root to be refused, got: %#v", err)
	}
	// Asserted rather than guarded on: an error that came back as a PathError here
	// would satisfy the not-found check above and slip past a conditional.
	var sourceErr *os.LinkError
	if !errors.As(err, &sourceErr) {
		t.Fatalf("expected the rename's own error shape for an escaping source, got: %#v", err)
	}
	if !strings.Contains(sourceErr.Old, "out") {
		t.Errorf("expected the source to be named, got old=%q new=%q", sourceErr.Old, sourceErr.New)
	}
	if _, statErr := os.Stat(filepath.Join(root, "a.txt")); statErr != nil {
		t.Errorf("the source was moved by a refused rename: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "secret.txt")); statErr != nil {
		t.Errorf("a refused rename touched the file outside the root: %v", statErr)
	}
}

// A rooted rename that stays inside the root is the ordinary case and must work:
// the handle only refuses what leaves the tree.
func TestARootedRenameWithinTheRootSucceeds(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "contents")

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	maxEntries, maxSize := 10, int64(1024)
	svc := NewService(Options{Roots: set, MaxFileSize: maxSize, MaxDirEntries: maxEntries, Enabled: true})
	if err := svc.Rename(context.Background(), filepath.Join(root, "a.txt"),
		filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("a rename inside the root was refused: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "b.txt")); got != "contents" {
		t.Errorf("expected the entry at its new name, got %q", got)
	}
}

// A refusal has to survive being derived from: join and dir are how every
// operation builds the path it acts on, and a derivation that dropped the
// refusal would turn a path this hub may not use into one it uses with no
// boundary at all.
func TestARefusalSurvivesDerivingAPath(t *testing.T) {
	root := sandbox(t)
	outside := sandbox(t)

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	refused := set.Confine(filepath.Join(outside, "child"))
	for _, derived := range []Confined{refused.join("grandchild"), refused.dir(), refused.dir().join("sibling")} {
		if _, err := stat(derived); !errors.Is(err, ErrPathNotAllowed) {
			t.Errorf("expected %s to stay refused, got: %v", derived.abs, err)
		}
	}
}

// A closed set refuses rather than behaving as though it had no boundary, and
// rather than taking the process down with an index past the handles it no
// longer has. Closing is a test's business, so this is the shape a test mistake
// should take.
func TestAClosedRootSetRefusesRatherThanFailingOpen(t *testing.T) {
	root := sandbox(t)
	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	set.Close()
	set.Close() // and a second one is harmless

	if _, err := stat(set.Confine(filepath.Join(root, "a.txt"))); !errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("expected a closed set to refuse, got: %v", err)
	}
}

// A move between two configured roots is the one operation a handle cannot
// express, and performing it on the two absolute paths is what a component
// replaced in between could redirect -- out of the boundary in one direction or
// into it in the other. It is refused instead, and this pins both halves: the
// error the caller gets, and that nothing moved.
func TestAMoveBetweenTwoRootsIsRefused(t *testing.T) {
	base := sandbox(t)
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	mustMkdir(t, first)
	mustMkdir(t, second)

	source := filepath.Join(first, "a.txt")
	destination := filepath.Join(second, "a.txt")
	mustWrite(t, source, "contents")

	set, err := NewRootSet([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	maxEntries, maxSize := 10, int64(1024)
	svc := NewService(Options{Roots: set, MaxFileSize: maxSize, MaxDirEntries: maxEntries, Enabled: true})

	err = svc.Rename(context.Background(), source, destination)
	if !errors.Is(err, ErrCrossRoot) {
		t.Fatalf("expected a move between two roots to be refused, got: %v", err)
	}
	// Not the not-allowed error: both paths are inside the boundary the caller
	// may name, and saying one of them is outside would be false.
	if errors.Is(err, ErrPathNotAllowed) {
		t.Errorf("the refusal reported a path outside the boundary: %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Error("a refused cross-root move created the destination")
	}
	if got := readFile(t, source); got != "contents" {
		t.Errorf("a refused cross-root move moved the source: %q", got)
	}
}

// Close is the end of the hub's lifetime, and the hub calls it while a request
// may still be running: Shutdown's HTTP phase is bounded by a context, so it can
// return -- and the force-close that follows can close connections -- with a
// handler still going. Confine reads the two fields Close writes, so without the
// lock this is a data race whose loser indexes a slice that has already been
// emptied, which is a panic rather than a refusal.
//
// Either answer is correct here and the test asserts only that one of them was
// given: a handle handed out before the close is usable until the close reaches
// it, and afterwards the set refuses. What is not acceptable is acting with no
// boundary, or taking the process down.
func TestClosingARootSetRacesItsReadersWithoutPanicking(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "contents")

	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	maxEntries, maxSize := 10, int64(1024)
	svc := NewService(Options{Roots: set, MaxFileSize: maxSize, MaxDirEntries: maxEntries, Enabled: true})

	start := make(chan struct{})
	var wg sync.WaitGroup

	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.List(context.Background(), root); err != nil &&
				!errors.Is(err, ErrPathNotAllowed) {
				t.Errorf("a listing that raced the close reported: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		set.Close()
	}()

	close(start)
	wg.Wait()
}

// The rooted branch of every operation is a different code path from the
// unrestricted one -- a handle rather than a plain path -- and the migration
// rewrote all of them at once. These are the paths a coverage gap would hide, so
// they are exercised end to end rather than through the helpers.
func TestRootedReadServesTheFileThroughItsHandle(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "contents")

	svc := mustRootedService(t, root)
	result, err := svc.Read(context.Background(), filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("reading a rooted file failed: %v", err)
	}
	defer func() {
		_ = result.File.Close() //nolint:errcheck // the test is done with it
	}()

	body, err := io.ReadAll(result.File)
	if err != nil {
		t.Fatalf("reading the descriptor failed: %v", err)
	}
	if string(body) != "contents" || result.Size != int64(len("contents")) || result.Binary {
		t.Errorf("expected the file's own bytes, got %q size=%d binary=%v", body, result.Size, result.Binary)
	}
}

func TestRootedWriteCreatesAndThenReplacesWithItsOwnStamp(t *testing.T) {
	root := sandbox(t)
	svc := mustRootedService(t, root)
	ctx := context.Background()
	file := filepath.Join(root, "written.txt")

	created, err := svc.Write(ctx, file, strings.NewReader("one"), 3, nil)
	if err != nil {
		t.Fatalf("creating a rooted file failed: %v", err)
	}
	// The stamp the write returned is what the next save is compared against,
	// which is the property the whole optimistic flow rests on.
	replaced, err := svc.Write(ctx, file, strings.NewReader("two"), 3,
		&ExpectedMtime{Millis: created.Mtime, Nanos: &created.MtimeNanos})
	if err != nil {
		t.Fatalf("the consecutive save was refused: %v", err)
	}
	if replaced.MtimeNanos == 0 {
		t.Error("a rooted write reported no modification time")
	}
	if got := readFile(t, file); got != "two" {
		t.Errorf("expected the replacement contents, got %q", got)
	}

	// And a stale observation against a rooted file is refused rather than
	// overwriting it.
	stale := created.MtimeNanos - 1
	if _, err := svc.Write(ctx, file, strings.NewReader("three"), 5,
		&ExpectedMtime{Millis: created.Mtime, Nanos: &stale}); !errors.Is(err, ErrConflict) {
		t.Errorf("expected a conflict, got: %v", err)
	}
}

func TestRootedCreateAndDeleteActInsideTheRoot(t *testing.T) {
	root := sandbox(t)
	mustWrite(t, filepath.Join(root, "keep.txt"), "contents")
	svc := mustRootedService(t, root)
	ctx := context.Background()

	if err := svc.Create(ctx, filepath.Join(root, "new.txt"), false); err != nil {
		t.Fatalf("creating a rooted file failed: %v", err)
	}
	if err := svc.Create(ctx, filepath.Join(root, "newdir"), true); err != nil {
		t.Fatalf("creating a rooted directory failed: %v", err)
	}
	if err := svc.Create(ctx, filepath.Join(root, "new.txt"), false); !errors.Is(err, ErrConflict) {
		t.Errorf("expected a taken name to be refused, got: %v", err)
	}

	// A directory with something in it goes only when that was asked for.
	mustWrite(t, filepath.Join(root, "newdir", "inside.txt"), "x")
	if err := svc.Delete(ctx, filepath.Join(root, "newdir"), false); !errors.Is(err, ErrDirNotEmpty) {
		t.Errorf("expected a non-empty directory to be refused, got: %v", err)
	}
	if err := svc.Delete(ctx, filepath.Join(root, "newdir"), true); err != nil {
		t.Fatalf("recursive delete inside the root failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "newdir")); !os.IsNotExist(err) {
		t.Error("the directory survived a recursive delete")
	}
	if got := readFile(t, filepath.Join(root, "keep.txt")); got != "contents" {
		t.Errorf("a neighbouring file was touched: %q", got)
	}
}
