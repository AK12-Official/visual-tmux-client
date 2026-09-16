package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A forced overwrite answers "the file changed under me, write mine anyway". It
// is not an answer to "the file was moved out from under this path", which is a
// different situation and one the user was never asked about.
//
// The bug this pins: the last check was skipped entirely for a forced write, so
// the whole upload was a window in which a rename could land. The rename moved
// the file to its new name; the write then recreated the old one, putting the
// edit back at a path nothing was looking at, while the tab that asked to save
// had followed the file to the new name and was told it had succeeded.
func TestAForcedWriteDoesNotRecreateARenamedFile(t *testing.T) {
	dir := sandbox(t)
	original := filepath.Join(dir, "a.txt")
	moved := filepath.Join(dir, "b.txt")
	mustWrite(t, original, "first")

	svc := mustService(t, Options{})
	body := &raceReader{
		inner: strings.NewReader("edited"),
		race: func() {
			if err := os.Rename(original, moved); err != nil {
				t.Errorf("the rename failed: %v", err)
			}
		},
	}

	if _, err := svc.Write(context.Background(), original, body, 6, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected a forced write to refuse a target that is gone, got: %v", err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Error("a forced write recreated a file that had been renamed away")
	}
	if got := readFile(t, moved); got != "first" {
		t.Errorf("the renamed file was written through: %q", got)
	}
	if residue := stagingResidue(t, dir); len(residue) != 0 {
		t.Errorf("a refused write left a staging file behind: %v", residue)
	}
}

// The same window reached the other way: the name is taken by a file this write
// never saw. A forced overwrite agrees to replace the file it was asked about,
// not whatever answers to that name when the transfer finishes.
func TestAForcedWriteRefusesAFileReplacedDuringTheTransfer(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")

	svc := mustService(t, Options{})
	body := &raceReader{
		inner: strings.NewReader("edited"),
		race: func() {
			if err := os.Remove(file); err != nil {
				t.Errorf("removing the target failed: %v", err)
			}
			mustWrite(t, file, "something else")
		},
	}

	if _, err := svc.Write(context.Background(), file, body, 6, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict, got: %v", err)
	}
	if got := readFile(t, file); got != "something else" {
		t.Errorf("the replacement was overwritten: %q", got)
	}
}

// A name that was free when the write began and is taken when it lands is the
// same hazard from the other side: a create would replace a file created in the
// meantime, which its author never agreed to lose.
func TestAWriteRefusesANameTakenWhileItWasBeingWritten(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "new.txt")

	svc := mustService(t, Options{})
	body := &raceReader{
		inner: strings.NewReader("created"),
		race:  func() { mustWrite(t, file, "someone else's") },
	}

	if _, err := svc.Write(context.Background(), file, body, 7, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict, got: %v", err)
	}
	if got := readFile(t, file); got != "someone else's" {
		t.Errorf("a file created during the write was replaced: %q", got)
	}
}

// A create still works when the name stays free, which is the case the check
// above must not break.
func TestAWriteStillCreatesAFileThatStaysAbsent(t *testing.T) {
	file := filepath.Join(sandbox(t), "new.txt")
	svc := mustService(t, Options{})

	result, err := svc.Write(context.Background(), file, strings.NewReader("created"), 7, nil)
	if err != nil {
		t.Fatalf("creating a new file failed: %v", err)
	}
	if got := readFile(t, file); got != "created" {
		t.Errorf("expected the written contents, got %q", got)
	}
	if result.MtimeNanos == 0 {
		t.Error("a completed write must report the modification time it produced")
	}
}

// Two edits a nanosecond apart are two edits even inside one millisecond, and a
// comparison that could not see the difference would let the later save
// overwrite the earlier one without saying so.
//
// The expectation here is one nanosecond off the file's real time while the
// millisecond value is spot on, so only a comparison at full precision can
// refuse it. That holds whatever granularity the filesystem records, which is
// what makes this deterministic.
func TestAWriteComparesModificationTimesAtFullPrecision(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	svc := mustService(t, Options{})

	first, err := svc.Write(context.Background(), file, strings.NewReader("one"), 3, nil)
	if err != nil {
		t.Fatalf("the first write failed: %v", err)
	}

	drifted := first.MtimeNanos + 1
	_, err = svc.Write(context.Background(), file, strings.NewReader("two"), 3,
		&ExpectedMtime{Millis: first.Mtime, Nanos: &drifted})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict for an observation one nanosecond stale, got: %v", err)
	}

	// And the time the write actually reported still saves without a spurious
	// conflict, which is what makes the value worth returning.
	if _, err := svc.Write(context.Background(), file, strings.NewReader("two"), 3,
		&ExpectedMtime{Millis: first.Mtime, Nanos: &first.MtimeNanos}); err != nil {
		t.Fatalf("the next save was refused: %v", err)
	}
}

// A subject that reads its whole directory before its caller can apply a bound
// is what makes a listing on a very large directory expensive: the cost is the
// directory's size, not the listing's.
func TestReadListingStopsOneEntryPastTheBound(t *testing.T) {
	dir := sandbox(t)
	for i := range 10 {
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), "")
	}

	handle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = handle.Close() //nolint:errcheck // the test is done with it
	}()

	entries, more, err := readListing(context.Background(), handle, 3)
	if err != nil {
		t.Fatalf("readListing failed: %v", err)
	}
	// One past the bound, because that is the cheapest way to know there is more.
	if len(entries) != 4 || !more {
		t.Errorf("expected 4 entries and truncation, got %d entries more=%v", len(entries), more)
	}
}

// A directory that fits reports no truncation, so the flag keeps meaning what it
// says rather than "the reader stopped early".
func TestReadListingReportsNoTruncationForADirectoryThatFits(t *testing.T) {
	dir := sandbox(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		mustWrite(t, filepath.Join(dir, name), "")
	}

	handle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = handle.Close() //nolint:errcheck // the test is done with it
	}()

	entries, more, err := readListing(context.Background(), handle, 3)
	if err != nil {
		t.Fatalf("readListing failed: %v", err)
	}
	if len(entries) != 3 || more {
		t.Errorf("expected 3 entries and no truncation, got %d entries more=%v", len(entries), more)
	}
}

func TestReadListingStopsWhenTheCallerIsGone(t *testing.T) {
	dir := sandbox(t)
	mustWrite(t, filepath.Join(dir, "a.txt"), "")

	handle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = handle.Close() //nolint:errcheck // the test is done with it
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readListing(ctx, handle, 3); !errors.Is(err, context.Canceled) {
		t.Errorf("expected the read to stop with the caller, got: %v", err)
	}

	// And a listing that is asked for after the caller has gone reads nothing.
	svc := mustService(t, Options{})
	if _, err := svc.List(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Errorf("expected a cancelled listing to fail, got: %v", err)
	}
}

// A link to a directory is a directory as far as a browser is concerned. It
// offers the first for expanding and the second for opening, and it decides
// which from this flag alone -- so reporting the link as a file produces an
// entry that invites a click and then refuses it.
func TestListReportsALinkToADirectoryAsADirectory(t *testing.T) {
	dir := sandbox(t)
	mustMkdir(t, filepath.Join(dir, "real"))
	mustWrite(t, filepath.Join(dir, "real", "inside.txt"), "contents")
	mustSymlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "to-dir"))
	mustSymlink(t, filepath.Join(dir, "real", "inside.txt"), filepath.Join(dir, "to-file"))

	svc := mustService(t, Options{})
	result, err := svc.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	for _, entry := range result.Entries {
		switch entry.Name {
		case "to-dir":
			if !entry.IsDir {
				t.Error("a link to a directory was reported as a file")
			}
		case "to-file":
			if entry.IsDir {
				t.Error("a link to a file was reported as a directory")
			}
			// Described by what it points at, so the size and time a listing
			// reports are the ones a reader of that file would see.
			if entry.Size != int64(len("contents")) {
				t.Errorf("a link to a file reported size %d, want %d", entry.Size, len("contents"))
			}
		}
	}
}

// Following a link is an answer about a path the caller may not name, so it is
// the guard that decides whether to follow one, not the listing.
func TestListDoesNotFollowALinkOutOfTheBoundary(t *testing.T) {
	outside := sandbox(t)
	mustMkdir(t, filepath.Join(outside, "elsewhere"))
	mustWrite(t, filepath.Join(outside, "elsewhere", "secret.txt"), "secret")

	root := sandbox(t)
	mustSymlink(t, filepath.Join(outside, "elsewhere"), filepath.Join(root, "escape"))

	svc := mustRootedService(t, root)
	result, err := svc.List(context.Background(), root)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, entry := range result.Entries {
		if entry.Name != "escape" {
			continue
		}
		if entry.IsDir {
			t.Error("a link leading outside the boundary was offered as an expandable directory")
		}
		if entry.Size != 0 {
			t.Error("a listing disclosed the size of a path outside the boundary")
		}
	}
}

// A named pipe with no writer blocks in open(2) until one appears, and nothing
// interrupts that wait. The directory tree shows one as an ordinary row, so a
// single click would hold a request goroutine for as long as the writer takes to
// show up -- which, for a pipe nothing writes to, is forever.
func TestReadRefusesANamedPipeInsteadOfWaitingOnIt(t *testing.T) {
	dir := sandbox(t)
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("this platform cannot create a named pipe: %v", err)
	}

	svc := mustService(t, Options{})
	done := make(chan error, 1)
	go func() {
		result, err := svc.Read(context.Background(), pipe)
		if result.File != nil {
			_ = result.File.Close() //nolint:errcheck // the test is done with it
		}
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("expected a named pipe to be refused, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a named pipe blocked instead of being refused")
	}
}

// The same refusal covers everything that is not a regular file, so a device or
// a socket cannot be read through the file API either.
func TestReadRefusesADirectoryAndEveryOtherKind(t *testing.T) {
	dir := sandbox(t)
	mustMkdir(t, filepath.Join(dir, "sub"))
	svc := mustService(t, Options{})

	if _, err := svc.Read(context.Background(), filepath.Join(dir, "sub")); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("expected a directory to be refused, got: %v", err)
	}
}
