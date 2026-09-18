package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER. A modification time
// above it would be silently corrupted by a JSON client, which is why the wire
// format is milliseconds rather than nanoseconds.
const maxSafeInteger = 9007199254740991

const testBody = "hello"

func mustService(t *testing.T, opts Options) *Service {
	t.Helper()
	if opts.Roots == nil {
		set, err := NewRootSet(nil)
		if err != nil {
			t.Fatal(err)
		}
		opts.Roots = set
	}
	if opts.MaxFileSize == 0 {
		opts.MaxFileSize = 1 << 20
	}
	if opts.MaxDirEntries == 0 {
		opts.MaxDirEntries = 100
	}
	opts.Enabled = true
	return NewService(opts)
}

func mustRootedService(t *testing.T, root string) *Service {
	t.Helper()
	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	// The set holds an open descriptor per root, which is what operations below
	// the root are performed through; a test that never gives it back leaks one.
	t.Cleanup(set.Close)
	return mustService(t, Options{Roots: set})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// stagingResidue reports any leftover staging file in a directory.
func stagingResidue(t *testing.T, dir string) []string {
	t.Helper()
	children, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var residue []string
	for _, child := range children {
		if strings.HasPrefix(child.Name(), tempFilePrefix) {
			residue = append(residue, child.Name())
		}
	}
	return residue
}

// failingReader yields some bytes and then fails, standing in for a client that
// vanishes or a disk that goes away part-way through a write.
type failingReader struct {
	remaining int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, errors.New("the body stopped early")
	}
	n := min(len(p), r.remaining)
	for i := range n {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestServiceDisabledIsReportedWithoutTouchingTheFilesystem(t *testing.T) {
	set, err := NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(Options{Roots: set, MaxFileSize: 1024, MaxDirEntries: 10, Enabled: false})
	if svc.Enabled() {
		t.Fatal("expected the service to report itself disabled")
	}
}

// A service built without a root set is a service with no boundary rather than a
// broken one: the set is substituted at construction, so every method has
// something to go through and closing it has something to give back. Left nil,
// the first call would dereference it.
func TestAServiceBuiltWithoutRootsIsUnrestrictedRatherThanNil(t *testing.T) {
	dir := sandbox(t)
	mustWrite(t, filepath.Join(dir, "a.txt"), testBody)

	svc := NewService(Options{MaxFileSize: 1024, MaxDirEntries: 10, Enabled: true})
	result, err := svc.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("a service with no roots should list an ordinary directory: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "a.txt" {
		t.Errorf("expected the directory's own entry, got %v", result.Entries)
	}
	svc.Close()
	svc.Close()
}

// Closing a service reaches the handles its roots are acted through, rather than
// only marking the set unusable: an unreleased descriptor is the leak this
// exists to stop.
func TestClosingAServiceReleasesItsRootHandles(t *testing.T) {
	root := sandbox(t)
	set, err := NewRootSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	handle := set.handles[0]
	svc := NewService(Options{Roots: set, MaxFileSize: 1024, MaxDirEntries: 10, Enabled: true})

	svc.Close()

	if _, err := handle.Open("."); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("the descriptor was not released: %v", err)
	}
}

// A handle closed by a shutdown is one cause, and it has to be answered the same
// way wherever it is met. The classification is now in one place -- the return of
// Write and of Delete -- rather than at each site that can raise an error, which
// is what made the previous version of this test weak: the sites that matter sit
// inside windows of two adjacent syscalls, where no test can put a closed handle,
// so reverting any one of them left the suite green.
//
// An operation on a directory that cannot be written reaches the classifier
// deterministically instead, for both of the public methods that have one, and
// losing either changes what it answers: without it the error is the generic
// write failure, which the transport reports as a server fault rather than as a
// refusal. This test is what the review of the round that added them used to find
// that Delete's had never been applied.
func TestAnOperationOnAnUnwritableDirectoryIsAPermissionRefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory that has no write permission")
	}
	dir := sandbox(t)
	locked := filepath.Join(dir, "locked")
	mustMkdir(t, locked)
	mustWrite(t, filepath.Join(locked, "a.txt"), "first")
	// Everything the test needs is made before the directory is locked: nothing
	// can be created inside it afterwards, which is the point.
	tree := filepath.Join(locked, "tree")
	mustMkdir(t, tree)
	mustWrite(t, filepath.Join(tree, "f.txt"), "nested")
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o700) //nolint:errcheck // cleanup, and the sandbox goes away anyway
	})

	svc := mustService(t, Options{})
	_, err := svc.Write(context.Background(), filepath.Join(locked, "a.txt"),
		strings.NewReader("edited"), 6, nil, false)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("a write that cannot be staged is not a permission refusal: %v", err)
	}

	// And the delete half, on the path where its classification is easiest to
	// lose: a tree removed recursively goes through removeAll, which is a call
	// site answering for itself rather than an error that was already classified,
	// and it has to answer the same way as the single-entry path beside it.
	delErr := svc.Delete(context.Background(), tree, true)
	if !errors.Is(delErr, ErrPermissionDenied) {
		t.Fatalf("a recursive delete that cannot be performed is not a permission refusal: %v", delErr)
	}
}

func TestListOrdersDirectoriesFirstAndReportsTruncation(t *testing.T) {
	dir := sandbox(t)
	mustWrite(t, filepath.Join(dir, "b.txt"), "b")
	mustWrite(t, filepath.Join(dir, "a.txt"), "a")
	mustMkdir(t, filepath.Join(dir, "z-dir"))
	mustMkdir(t, filepath.Join(dir, "m-dir"))

	svc := mustService(t, Options{MaxDirEntries: 100})
	result, err := svc.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	var names []string
	for _, entry := range result.Entries {
		names = append(names, entry.Name)
		if entry.Name == "z-dir" && !entry.IsDir {
			t.Error("expected z-dir to be reported as a directory")
		}
	}
	want := []string{"m-dir", "z-dir", "a.txt", "b.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("expected %v, got %v", want, names)
	}
	if result.Truncated {
		t.Error("a listing within the bound must not report truncation")
	}
}

func TestListTruncatesAtTheBound(t *testing.T) {
	dir := sandbox(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		mustWrite(t, filepath.Join(dir, name), "")
	}

	svc := mustService(t, Options{MaxDirEntries: 2})
	result, err := svc.List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !result.Truncated || len(result.Entries) != 2 {
		t.Errorf("expected 2 truncated entries, got %d truncated=%v", len(result.Entries), result.Truncated)
	}
}

func TestListDistinguishesTheWrongKindOfTargetFromAnEmptyDirectory(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, testBody)
	empty := filepath.Join(dir, "empty")
	mustMkdir(t, empty)

	svc := mustService(t, Options{})
	result, err := svc.List(context.Background(), empty)
	if err != nil || len(result.Entries) != 0 {
		t.Errorf("an empty directory must list successfully: %v", err)
	}

	if _, err := svc.List(context.Background(), file); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("listing a file must be refused as invalid, got: %v", err)
	}
	if _, err := svc.List(context.Background(), filepath.Join(dir, "absent")); !errors.Is(err, ErrNotFound) {
		t.Errorf("listing a missing path must be not-found, got: %v", err)
	}
}

func TestReadRefusesAnOversizedFileBeforeOpeningIt(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "large.txt")
	mustWrite(t, file, strings.Repeat("x", 64))

	svc := mustService(t, Options{MaxFileSize: 32})
	result, err := svc.Read(context.Background(), file)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected a too-large error, got: %v", err)
	}
	if result.File != nil {
		t.Error("an oversized read must not hand back a reader")
	}
}

func TestReadReportsSizeAndAJavaScriptSafeModificationTime(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, testBody)

	svc := mustService(t, Options{})
	result, err := svc.Read(context.Background(), file)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	defer func() {
		if err := result.File.Close(); err != nil {
			t.Error(err)
		}
	}()

	if result.Size != int64(len(testBody)) {
		t.Errorf("expected size %d, got %d", len(testBody), result.Size)
	}
	if result.Mtime <= 0 || result.Mtime > maxSafeInteger {
		t.Errorf("modification time %d is not usable by a JSON client", result.Mtime)
	}

	if _, err := svc.Read(context.Background(), dir); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("reading a directory must be refused, got: %v", err)
	}
}

func TestWriteRefusesAConflictAndLeavesTheFileAlone(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})

	stale := int64(1) // long before the file was written
	_, err := svc.Write(context.Background(), file, strings.NewReader("replacement"), 11,
		&ExpectedMtime{Millis: stale}, false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict, got: %v", err)
	}
	if got := readFile(t, file); got != "original" {
		t.Errorf("a conflicted write changed the file: %q", got)
	}
}

func TestWriteWithoutAnExpectedMtimeOverwrites(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})

	if _, err := svc.Write(context.Background(), file, strings.NewReader("replacement"), 11, nil, false); err != nil {
		t.Fatalf("a forced overwrite must succeed: %v", err)
	}
	if got := readFile(t, file); got != "replacement" {
		t.Errorf("expected the replacement contents, got %q", got)
	}
}

// The bug this pins: the staging file is created 0600, so renaming it over the
// target used to rewrite the target's permission bits -- editing a script
// through the file manager stripped its executable bit on every save.
func TestWriteKeepsTheTargetsPermissionBits(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "script.sh")
	mustWrite(t, file, "#!/bin/sh\n")
	if err := os.Chmod(file, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := mustService(t, Options{})

	replacement := "#!/bin/sh\necho hi\n"
	size := int64(len(replacement))
	if _, err := svc.Write(context.Background(), file, strings.NewReader(replacement), size, nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("a save reset the mode to %v, want 0755", got)
	}
}

// A write that had nothing to replace gets the same mode a file created directly
// would get -- which is the documented default reduced by the process's umask.
// Measured against a file created the ordinary way rather than assumed, because
// the umask is the environment's to choose: what matters is that a file the
// browser writes is no more permissive than one the hub would otherwise create.
func TestWriteCreatesANewFileWithTheSameModeAsADirectCreate(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})

	probe := filepath.Join(dir, "probe")
	handle, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, createFileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	probeInfo, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, "new.txt")
	size := int64(len(testBody))
	if _, err := svc.Write(context.Background(), file, strings.NewReader(testBody), size, nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), probeInfo.Mode().Perm(); got != want {
		t.Errorf("a written file is %v, but one created directly is %v", got, want)
	}
}

// The bug this pins: a client that assumes the new modification time instead of
// being told it would date the file a second behind itself and report a
// conflict on the very next save.
func TestWriteReturnsAnMtimeThatMakesTheNextSaveSucceed(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})

	first, err := svc.Write(context.Background(), file, strings.NewReader("one"), 3, nil, false)
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	// The returned value has to be the file's real modification time, not a
	// guess: the next save is only free of a spurious conflict because it is.
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mtime != info.ModTime().UnixMilli() {
		t.Errorf("write reported mtime %d but the file has %d", first.Mtime, info.ModTime().UnixMilli())
	}
	if first.MtimeNanos != info.ModTime().UnixNano() {
		t.Errorf("write reported %d ns but the file has %d", first.MtimeNanos, info.ModTime().UnixNano())
	}
	if _, err := svc.Write(context.Background(), file, strings.NewReader("two"), 3,
		&ExpectedMtime{Millis: first.Mtime, Nanos: &first.MtimeNanos}, false); err != nil {
		t.Fatalf("the second save was refused as a conflict: %v", err)
	}
	if got := readFile(t, file); got != "two" {
		t.Errorf("expected the second contents, got %q", got)
	}
}

func TestWriteRefusesAMissingFileWhenAnMtimeWasExpected(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	svc := mustService(t, Options{})

	observed := int64(1)
	_, err := svc.Write(context.Background(), file, strings.NewReader("x"), 1, &ExpectedMtime{Millis: observed}, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not-found, got: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("a write that expected an existing file created one")
	}
}

func TestWriteRefusesABodyThatDoesNotMatchItsDeclaredLength(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})

	_, err := svc.Write(context.Background(), file, strings.NewReader("short"), 500, nil, false)
	if !errors.Is(err, ErrInvalidBody) {
		t.Fatalf("expected an invalid-body error, got: %v", err)
	}
	if got := readFile(t, file); got != "original" {
		t.Errorf("a refused write changed the file: %q", got)
	}
	if residue := stagingResidue(t, dir); len(residue) != 0 {
		t.Errorf("a refused write left staging files behind: %v", residue)
	}
}

func TestWriteRefusesABodyOverTheSizeLimit(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	svc := mustService(t, Options{MaxFileSize: 8})

	_, err := svc.Write(context.Background(), file, strings.NewReader("x"), 64, nil, false)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected a too-large error, got: %v", err)
	}
}

// A write that fails part-way must leave the previous contents in place and no
// staging file behind: a partially written file at the target path is the
// outcome the staging dance exists to prevent, and a leftover sibling is the
// same bug one directory over. The body here is large enough that the failure
// happens well after data has started to land.
func TestWriteLeavesTheOriginalWhenTheBodyFailsMidStream(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})

	const partial, declared = 64 << 10, 128 << 10
	_, err := svc.Write(context.Background(), file, &failingReader{remaining: partial}, declared, nil, false)
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("expected a write failure, got: %v", err)
	}
	if got := readFile(t, file); got != "original" {
		t.Errorf("an interrupted write changed the file: %q", got)
	}
	if residue := stagingResidue(t, dir); len(residue) != 0 {
		t.Errorf("an interrupted write left staging files behind: %v", residue)
	}
}

// A browser cannot discover which directories the boundary permits, so when the
// session's own directory falls outside it the hub has to name one that does not
// -- and say that it did, rather than silently opening somewhere else.
func TestStartDirectorySubstitutesOnlyOutsideTheBoundary(t *testing.T) {
	base := sandbox(t)
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, root)
	mustMkdir(t, outside)

	unrestricted := mustService(t, Options{})
	rooted := mustRootedService(t, root)

	tests := []struct {
		name            string
		svc             *Service
		candidate       string
		wantPath        string
		wantSubstituted bool
	}{
		{"unrestricted keeps the candidate", unrestricted, outside, outside, false},
		{"inside the boundary keeps the candidate", rooted, root, root, false},
		{"outside the boundary is substituted", rooted, outside, root, true},
		{"a missing candidate outside the boundary is substituted", rooted,
			filepath.Join(outside, "gone"), root, true},
		// Missing is not a boundary decision, so the caller is left to deal with
		// it -- it can step up to an ancestor, which the hub cannot choose for it.
		{"a missing candidate inside the boundary is left alone", rooted,
			filepath.Join(root, "gone"), filepath.Join(root, "gone"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, substituted := tc.svc.StartDirectory(tc.candidate)
			if path != tc.wantPath || substituted != tc.wantSubstituted {
				t.Errorf("expected (%q, %v), got (%q, %v)",
					tc.wantPath, tc.wantSubstituted, path, substituted)
			}
		})
	}
}

// The first configured root is the one a substituted start lands on, so the
// answer does not depend on which directory the session happened to be in.
func TestStartDirectoryAlwaysSubstitutesTheFirstRoot(t *testing.T) {
	base := sandbox(t)
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	mustMkdir(t, first)
	mustMkdir(t, second)

	set, err := NewRootSet([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	svc := mustService(t, Options{Roots: set})

	for _, candidate := range []string{base, filepath.Join(base, "elsewhere")} {
		path, substituted := svc.StartDirectory(candidate)
		if path != first || !substituted {
			t.Errorf("candidate %q: expected (%q, true), got (%q, %v)", candidate, first, path, substituted)
		}
	}
}

func TestCreateRefusesTheCasesItCannotServe(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	existing := filepath.Join(dir, "a.txt")
	mustWrite(t, existing, testBody)
	if err := svc.Create(ctx, existing, false); !errors.Is(err, ErrConflict) {
		t.Errorf("creating over an existing file must conflict, got: %v", err)
	}
	if got := readFile(t, existing); got != testBody {
		t.Errorf("a refused create changed the file: %q", got)
	}

	if err := svc.Create(ctx, filepath.Join(dir, "missing", "x.txt"), false); !errors.Is(err, ErrNotFound) {
		t.Errorf("creating under a missing parent must be not-found, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Error("creating under a missing parent built the intermediate directory")
	}

	created := filepath.Join(dir, "new.txt")
	if err := svc.Create(ctx, created, false); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if info, err := os.Stat(created); err != nil || info.IsDir() || info.Size() != 0 {
		t.Errorf("expected an empty file, got info=%v err=%v", info, err)
	}

	createdDir := filepath.Join(dir, "new-dir")
	if err := svc.Create(ctx, createdDir, true); err != nil {
		t.Fatalf("Create directory failed: %v", err)
	}
	if info, err := os.Stat(createdDir); err != nil || !info.IsDir() {
		t.Errorf("expected a directory, got info=%v err=%v", info, err)
	}
}

func TestRenameRefusesAnExistingDestinationAndTheHappyPath(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	source := filepath.Join(dir, "a.txt")
	occupied := filepath.Join(dir, "b.txt")
	mustWrite(t, source, "a")
	mustWrite(t, occupied, "b")

	if err := svc.Rename(ctx, source, occupied); !errors.Is(err, ErrConflict) {
		t.Errorf("renaming onto an existing entry must conflict, got: %v", err)
	}
	if readFile(t, source) != "a" || readFile(t, occupied) != "b" {
		t.Error("a refused rename moved something")
	}

	target := filepath.Join(dir, "c.txt")
	if err := svc.Rename(ctx, source, target); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if readFile(t, target) != "a" {
		t.Error("the entry did not arrive at the destination")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Error("the entry is still at its original path")
	}
}

func TestRenameOutOfTheBoundaryIsRefused(t *testing.T) {
	base := sandbox(t)
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, root)
	mustMkdir(t, outside)

	source := filepath.Join(root, "a.txt")
	mustWrite(t, source, "a")

	svc := mustRootedService(t, root)
	err := svc.Rename(context.Background(), source, filepath.Join(outside, "a.txt"))
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected a rename out of the boundary to be refused, got: %v", err)
	}
	if readFile(t, source) != "a" {
		t.Error("a refused rename moved the entry")
	}
}

func TestDeleteRefusesANonEmptyDirectoryWithoutRecursion(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	full := filepath.Join(dir, "full")
	mustMkdir(t, full)
	mustWrite(t, filepath.Join(full, "a.txt"), testBody)

	if err := svc.Delete(ctx, full, false); !errors.Is(err, ErrDirNotEmpty) {
		t.Fatalf("expected a not-empty error, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(full, "a.txt")); err != nil {
		t.Error("a refused delete removed something")
	}

	if err := svc.Delete(ctx, full, true); err != nil {
		t.Fatalf("recursive delete failed: %v", err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Error("the directory survived a recursive delete")
	}
}

func TestDeleteRemovesFilesAndEmptyDirectories(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, testBody)
	if err := svc.Delete(ctx, file, false); err != nil {
		t.Fatalf("deleting a file failed: %v", err)
	}

	empty := filepath.Join(dir, "empty")
	mustMkdir(t, empty)
	if err := svc.Delete(ctx, empty, false); err != nil {
		t.Fatalf("deleting an empty directory failed: %v", err)
	}
}

// raceReader runs a side effect once, on its first read, which is how a test
// lands a competing change inside the window a transfer occupies.
type raceReader struct {
	once  sync.Once
	race  func()
	inner io.Reader
}

func (r *raceReader) Read(p []byte) (int, error) {
	r.once.Do(r.race)
	return r.inner.Read(p)
}

// The bug this pins: the observed modification time used to be checked only
// before the body was transferred. For a large file that is the whole request,
// so a second writer landing inside that window was overwritten by the first
// writer's rename without either of them being told.
func TestWriteRefusesWhenTheTargetChangesDuringTheTransfer(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})
	ctx := context.Background()

	// Date the file well into the past so the observation below and the
	// competing write's own time cannot land in the same millisecond.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}
	observed := past.UnixMilli()

	const winner = "written by the other writer"
	const stale = "stale replacement"
	body := &raceReader{
		inner: strings.NewReader(stale),
		race: func() {
			if _, err := svc.Write(ctx, file, strings.NewReader(winner), int64(len(winner)), nil, false); err != nil {
				t.Errorf("the competing write failed: %v", err)
			}
		},
	}

	_, err := svc.Write(ctx, file, body, int64(len(stale)), &ExpectedMtime{Millis: observed}, false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected the write to lose the race, got: %v", err)
	}
	if got := readFile(t, file); got != winner {
		t.Errorf("the losing write overwrote the winner: %q", got)
	}
	if residue := stagingResidue(t, dir); len(residue) != 0 {
		t.Errorf("a refused write left a staging file behind: %v", residue)
	}
}

// A staging file is an artefact of a write in flight, so it is omitted exactly
// while that write is in flight -- and a file the user named with the same prefix
// is never omitted, because this service did not create it. Recognising one by
// its name would hide the user's own file from the only interface that lists it.
func TestListHidesAStagingFileOnlyWhileItIsBeingWritten(t *testing.T) {
	dir := sandbox(t)
	authored := filepath.Join(dir, tempFilePrefix+"123456")
	mustWrite(t, authored, "the user's own file")
	mustWrite(t, filepath.Join(dir, "a.txt"), testBody)
	svc := mustService(t, Options{})
	ctx := context.Background()

	listed, err := svc.List(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(listed, tempFilePrefix+"123456") {
		t.Errorf("a file the user named was hidden from the listing: %v", names(listed))
	}

	// A listing taken from inside the write must show exactly what the one before
	// it showed. The staging file's name is random, so the absence of anything
	// new is the observable, and it is the right one either way.
	before := names(listed)
	var during []string
	body := &raceReader{
		inner: strings.NewReader("replacement"),
		race: func() {
			inner, lerr := svc.List(ctx, dir)
			if lerr != nil {
				t.Errorf("listing during a write failed: %v", lerr)
				return
			}
			during = names(inner)
		},
	}
	if _, err := svc.Write(ctx, filepath.Join(dir, "a.txt"), body, int64(len("replacement")), nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}
	if during == nil {
		t.Fatal("the listing taken during the write did not run")
	}
	if !slices.Equal(during, before) {
		t.Errorf("a listing taken during a write showed %v, want %v", during, before)
	}
}

func names(result ListResult) []string {
	out := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		out = append(out, entry.Name)
	}
	return out
}

func hasEntry(result ListResult, name string) bool {
	for _, entry := range result.Entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

// A write whose directory is gone is the caller's situation, and answers the way
// the equivalent create does rather than as a server-side failure.
func TestWriteIntoAMissingDirectoryReportsNotFound(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})

	target := filepath.Join(dir, "gone", "a.txt")
	_, err := svc.Write(context.Background(), target, strings.NewReader(testBody), int64(len(testBody)), nil, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not_found for a write into a missing directory, got: %v", err)
	}
}

// The specification requires the hub to answer this rather than leaving the
// browser to guess from the file's name: a name is a guess, and a wrong guess
// either renders binary bytes as text or refuses to open a file that is text.
func TestReadClassifiesBinaryFromTheContentsNotTheName(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	cases := []struct {
		name    string
		content []byte
		binary  bool
	}{
		{"text.txt", []byte("hello\nworld\n"), false},
		{"utf8_with_an_unhelpful_name.txt", []byte("héllo 世界\n"), false},
		{"empty.txt", nil, false},
		// A NUL is decisive: no encoding the browser reads as text contains one.
		{"nul.log", []byte("hello\x00world"), true},
		// Latin-1 bytes are not UTF-8, so decoding them would produce replacement
		// characters -- and saving that text back would destroy the original.
		{"latin1.log", []byte{0x68, 0x69, 0xe9, 0x0a}, true},
		// An unhelpful name in the other direction: a text file with no extension.
		{"no-extension", []byte("plain text\n"), false},
	}

	for _, tc := range cases {
		file := filepath.Join(dir, tc.name)
		if err := os.WriteFile(file, tc.content, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := svc.Read(ctx, file)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if result.Binary != tc.binary {
			t.Errorf("%s: Binary = %v, want %v", tc.name, result.Binary, tc.binary)
		}
		// Sampling must not consume the stream the caller goes on to serve.
		got, err := io.ReadAll(result.File)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !bytes.Equal(got, tc.content) {
			t.Errorf("%s: the caller read %q, want %q", tc.name, got, tc.content)
		}
		if err := result.File.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Classification has to survive being reached one chunk at a time, so a chunk
// that ends inside a rune is not the same thing as bytes that are not UTF-8 at
// all. The naive "any valid prefix means text" rule calls {0x68, 0xff, 0xfe}
// text, and the naive per-chunk rule calls ordinary text binary at whichever
// boundary it happens to land on.
func TestTextScanToleratesOnlyATruncatedRune(t *testing.T) {
	decide := func(chunks ...string) bool {
		var scan textScan
		for _, chunk := range chunks {
			if scan.decidedBy([]byte(chunk)) {
				return true
			}
		}
		// Whatever is still held back was a rune the end of the file cut in half.
		return len(scan.carry) > 0
	}

	// A chunk that ended inside a rune is decided by what follows it.
	if decide("h\xc3", "\xa9llo") {
		t.Error("text whose two-byte rune straddled a chunk boundary was called binary")
	}
	if decide("\xe4\xb8", "\xad\xe6\x96\x87") {
		t.Error("text whose three-byte rune straddled a chunk boundary was called binary")
	}
	// Bytes that are not UTF-8 anywhere are binary, however the chunk ends.
	if !decide("h\xff\xfe") {
		t.Error("bytes that are not UTF-8 at all were called text")
	}
	if !decide("\xe4\xb8\x41") {
		t.Error("a rune with a bad continuation byte was called text")
	}
	// A rune held back at the end of the file was cut in half by the end of the
	// file, and nothing completes it.
	if !decide("\xe4\xb8") {
		t.Error("a file ending inside a rune was called text")
	}
	if decide("plain ascii") {
		t.Error("plain ASCII was called binary")
	}
	// A NUL anywhere decides, including one only a chunk boundary away.
	if !decide("text", "\x00more") {
		t.Error("a NUL after the first chunk was called text")
	}
}

// The whole file decides. A file that opens as text and turns binary further in
// is the case a bounded sample got wrong, and getting it wrong is not a display
// problem: the browser decodes the rest into replacement characters and the next
// save writes those over the bytes the file had.
func TestBinaryIsDecidedByTheWholeFileNotItsPrefix(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	file := filepath.Join(dir, "log.txt")

	// Comfortably longer than any sample, with the binary part past the point a
	// prefix would have stopped.
	body := strings.Repeat("a", scanChunk*2) + "\x00binary tail"
	mustWrite(t, file, body)

	result, err := svc.Read(context.Background(), file)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	defer func() {
		_ = result.File.Close() //nolint:errcheck // the test is done with it
	}()
	if !result.Binary {
		t.Error("a file whose binary part came after the first chunk was offered as text")
	}
}

// A multi-byte character sitting exactly on a chunk boundary is the case a
// scanner that judged chunks independently would fail on, and its position is a
// buffer size rather than anything about the file.
func TestATextFileIsNotClassifiedByWhereItsChunkBoundariesFall(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	file := filepath.Join(dir, "notes.md")

	// Every character is three bytes, so the boundary lands mid-character
	// whenever scanChunk is not a multiple of three.
	mustWrite(t, file, strings.Repeat("文", scanChunk))
	for _, offset := range []int{1, 2} {
		target := filepath.Join(dir, fmt.Sprintf("shifted-%d.md", offset))
		mustWrite(t, target, strings.Repeat("x", offset)+strings.Repeat("文", scanChunk))

		result, err := svc.Read(context.Background(), target)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if result.Binary {
			t.Errorf("valid text was called binary when its runes straddled a chunk boundary")
		}
		_ = result.File.Close() //nolint:errcheck // the test is done with it
	}
}

// mustSymlink creates a link and fails the test if the platform refuses.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// The bug this pins: authorization resolves symlinks, so acting on the resolved
// path deleted what a link pointed at and left the link dangling -- the caller
// asked to remove one entry and lost a different one.
func TestDeleteRemovesASymlinkItselfAndNotItsTarget(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	target := filepath.Join(dir, "target.txt")
	mustWrite(t, target, testBody)
	link := filepath.Join(dir, "link.txt")
	mustSymlink(t, target, link)

	if err := svc.Delete(ctx, link, false); err != nil {
		t.Fatalf("deleting a symlink failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the link survived a delete that named it")
	}
	if readFile(t, target) != testBody {
		t.Error("deleting a link destroyed the file it pointed at")
	}
}

// The destructive variant: a recursive delete must not walk through the link
// into a directory tree the caller did not name.
func TestDeleteRemovesASymlinkedDirectoryWithoutEmptyingIt(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	realDir := filepath.Join(dir, "real")
	mustMkdir(t, realDir)
	inside := filepath.Join(realDir, "keep.txt")
	mustWrite(t, inside, testBody)

	link := filepath.Join(dir, "link")
	mustSymlink(t, realDir, link)

	if err := svc.Delete(ctx, link, true); err != nil {
		t.Fatalf("deleting a symlinked directory failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the link survived a delete that named it")
	}
	if readFile(t, inside) != testBody {
		t.Error("a recursive delete emptied a directory through a link")
	}
}

func TestRenameMovesASymlinkItselfAndNotItsTarget(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	target := filepath.Join(dir, "target.txt")
	mustWrite(t, target, testBody)
	link := filepath.Join(dir, "link.txt")
	mustSymlink(t, target, link)
	moved := filepath.Join(dir, "moved.txt")

	if err := svc.Rename(ctx, link, moved); err != nil {
		t.Fatalf("renaming a symlink failed: %v", err)
	}
	if _, err := os.Lstat(moved); err != nil {
		t.Fatalf("the link was not moved: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the link survived a rename that named it")
	}
	if readFile(t, target) != testBody {
		t.Error("renaming a link moved the file it pointed at")
	}
}

// Acting on the caller's spelling must not weaken the boundary: containment is
// still decided on where the path really leads.
func TestDeleteRefusesASymlinkThatLeavesTheBoundary(t *testing.T) {
	root := sandbox(t)
	outside := sandbox(t)
	svc := mustRootedService(t, root)

	precious := filepath.Join(outside, "precious.txt")
	mustWrite(t, precious, testBody)
	mustSymlink(t, outside, filepath.Join(root, "escape"))

	viaLink := filepath.Join(root, "escape", "precious.txt")
	err := svc.Delete(context.Background(), viaLink, false)
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected a path out of the boundary to be refused, got: %v", err)
	}
	if readFile(t, precious) != testBody {
		t.Error("a refused delete removed a file outside the boundary")
	}
}

// The path is normalized before the entry is named, so a redundant slash or dot
// segment names the same entry rather than being read as a directive. A trailing
// slash on a link therefore means the link, like every other spelling of it --
// there is no form in which the slash makes the operation follow it.
func TestDeleteAcceptsARedundantButWellFormedPath(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	for _, path := range []string{dir + "//a.txt", dir + "/./a.txt", dir + "/a.txt/"} {
		mustWrite(t, filepath.Join(dir, "a.txt"), testBody)
		if err := svc.Delete(ctx, path, false); err != nil {
			t.Errorf("deleting %q failed: %v", path, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
			t.Errorf("deleting %q left the file behind", path)
		}
	}
}

// The entry is named, never followed, however the path is spelled. Before the
// operation was expressed relative to the resolved parent, a trailing slash made
// the kernel follow the link and empty the directory it pointed at.
func TestDeleteWithATrailingSlashRemovesTheLinkNotItsTarget(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	ctx := context.Background()

	target := filepath.Join(dir, "target")
	mustMkdir(t, target)
	inside := filepath.Join(target, "keep.txt")
	mustWrite(t, inside, testBody)
	link := filepath.Join(dir, "link")
	mustSymlink(t, target, link)

	if err := svc.Delete(ctx, link+"/", true); err != nil {
		t.Fatalf("deleting a link with a trailing slash failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the link survived")
	}
	if readFile(t, inside) != testBody {
		t.Error("a trailing slash made the delete follow the link")
	}
}

// A broken link is still an entry the user can see, and the only way to remove
// it through the manager is to remove the link -- which does not follow it.
func TestDeleteRemovesADanglingLink(t *testing.T) {
	dir := sandbox(t)
	svc := mustService(t, Options{})
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), link, false); err != nil {
		t.Fatalf("deleting a dangling link failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the dangling link survived a delete that named it")
	}
}

// The entry belongs to the directory that holds it, so a link leading outside the
// boundary is still an entry inside it -- and removing that entry cannot touch
// what it pointed at. Deciding containment on the link's target instead would
// leave such a link impossible to remove through the manager.
func TestDeleteAndRenameRemoveALinkThatLeavesTheBoundary(t *testing.T) {
	root := sandbox(t)
	outside := sandbox(t)
	svc := mustRootedService(t, root)
	ctx := context.Background()

	precious := filepath.Join(outside, "precious.txt")
	mustWrite(t, precious, testBody)

	doomed := filepath.Join(root, "escape")
	mustSymlink(t, precious, doomed)
	if err := svc.Delete(ctx, doomed, false); err != nil {
		t.Fatalf("deleting a link out of the boundary failed: %v", err)
	}
	if readFile(t, precious) != testBody {
		t.Error("deleting a link removed what it pointed at")
	}

	moved := filepath.Join(root, "escape2")
	mustSymlink(t, precious, moved)
	if err := svc.Rename(ctx, moved, filepath.Join(root, "renamed")); err != nil {
		t.Fatalf("renaming a link out of the boundary failed: %v", err)
	}
	if readFile(t, precious) != testBody {
		t.Error("renaming a link moved what it pointed at")
	}
}

// The permission bits are kept and the special bits are not. The staging file is
// owned by the hub's user, so the replacement is too: carrying setuid across
// would not preserve a capability, it would hand one to whoever just wrote the
// file. A shell redirect does the same thing.
func TestWriteKeepsPermissionBitsAndDropsTheSpecialOnes(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "tool")
	mustWrite(t, file, "#!/bin/sh\n")
	if err := os.Chmod(file, 0o755|os.ModeSetuid); err != nil {
		t.Skipf("setuid is not available here: %v", err)
	}
	// Without this the test would pass vacuously wherever the bit silently does
	// not stick: it would be asserting that a bit which was never set is gone.
	set, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if set.Mode()&os.ModeSetuid == 0 {
		t.Skipf("this filesystem does not keep a setuid bit: %v", set.Mode())
	}
	svc := mustService(t, Options{})

	replacement := "#!/bin/sh\necho hi\n"
	size := int64(len(replacement))
	if _, err := svc.Write(context.Background(), file, strings.NewReader(replacement), size, nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("a save reset the permission bits to %v, want 0755", got)
	}
	if info.Mode()&os.ModeSetuid != 0 {
		t.Errorf("a save carried setuid onto the file it created: %v", info.Mode())
	}
}

// The path acted on is the resolved parent plus the entry name, so a swap of a
// *link* on it cannot redirect the operation. This flips the parent link between
// a directory inside and one outside the boundary while deleting through it, and
// asserts that nothing outside is ever touched.
//
// This is a probabilistic guard, not a proof. Measured against the behaviour it
// pins -- acting on the caller's spelling -- it fails in roughly two runs out of
// three, so a green run is evidence, not certainty, and a loaded CI machine will
// do worse. It is also narrower than the threat: it swaps a link that is already
// there, and says nothing about a real directory being replaced by one, which is
// the residual race `entryTarget` documents. Read it the way this repository
// reads ParentGuard: detection, never proof.
func TestDeleteThroughAFlappingLinkNeverReachesOutsideTheBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("races two goroutines; skipped in short mode")
	}
	root := sandbox(t)
	outside := sandbox(t)
	svc := mustRootedService(t, root)
	ctx := context.Background()

	inside := filepath.Join(root, "real")
	mustMkdir(t, inside)
	outsideVictim := filepath.Join(outside, "victim")
	mustMkdir(t, outsideVictim)
	mustWrite(t, filepath.Join(outsideVictim, "precious.txt"), testBody)

	swap := filepath.Join(root, "swap")
	mustSymlink(t, inside, swap)

	stop := make(chan struct{})
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		next := swap + ".next"
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			target := inside
			if i%2 == 1 {
				target = outside
			}
			if err := os.Remove(next); err != nil && !os.IsNotExist(err) {
				return
			}
			if err := os.Symlink(target, next); err != nil {
				return
			}
			if err := os.Rename(next, swap); err != nil {
				return
			}
		}
	}()

	for range 2000 {
		mustMkdir(t, filepath.Join(inside, "victim"))
		mustWrite(t, filepath.Join(inside, "victim", "precious.txt"), testBody)
		// Refused while the link points outside, not-found if the swap landed
		// between resolving the parent and looking the entry up, and invalid when
		// the kernel read the link as something else at that instant. Anything
		// else is a failure mode this test did not expect.
		err := svc.Delete(ctx, filepath.Join(swap, "victim"), true)
		if err != nil && !errors.Is(err, ErrPathNotAllowed) && !errors.Is(err, ErrNotFound) &&
			!errors.Is(err, ErrInvalidPath) {
			t.Fatalf("unexpected delete failure: %v", err)
		}
	}

	close(stop)
	<-swapped

	if readFile(t, filepath.Join(outsideVictim, "precious.txt")) != testBody {
		t.Fatal("a delete through a swapped link destroyed a file outside the boundary")
	}
}

// The shape of the caller's own spelling is checked before it is normalized, so
// `..` is refused here as the other operations refuse it. Collapsing it instead
// would make the same string name one entry for a delete and a different one for
// a read -- and neither would be the entry the kernel would have named.
func TestDeleteAndRenameRefuseAParentSegmentRatherThanCollapsingIt(t *testing.T) {
	dir := sandbox(t)
	victim := filepath.Join(dir, "victim.txt")
	mustWrite(t, victim, testBody)
	svc := mustService(t, Options{})
	ctx := context.Background()

	// Built by concatenation: filepath.Join would clean the segment away, and
	// the point is the spelling the caller sent.
	traversal := dir + "/sub/../victim.txt"
	if err := svc.Delete(ctx, traversal, false); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected a parent segment to be refused, got: %v", err)
	}
	if err := svc.Rename(ctx, traversal, filepath.Join(dir, "moved.txt")); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected a parent segment to be refused, got: %v", err)
	}
	if readFile(t, victim) != testBody {
		t.Error("a refused operation disturbed the entry the traversal named")
	}
}

// The filesystem root is the one path with nothing above it. No listing offers it
// as an entry, so a caller that named it can only have done so by accident -- and
// the answer cannot be to hand the kernel a recursive delete of everything.
//
// Asserted on the resolution rather than through Delete: a test whose failure
// mode is deleting the machine it runs on is not a test worth having.
func TestEntryTargetRefusesTheFilesystemRoot(t *testing.T) {
	svc := mustService(t, Options{})
	for _, path := range []string{"/", "//"} {
		if _, err := svc.entryTarget(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("entryTarget(%q) must be refused, got: %v", path, err)
		}
	}
}

// The blocked interfaces are refused before any root check, so no configuration
// can expose them -- and that has to hold for the entry itself, not only for the
// paths under it. The parent of /dev is an ordinary directory, so authorizing the
// parent alone left the interface's own name reachable as an entry: a recursive
// delete of /dev would have reached the kernel.
//
// Asserted on the resolution rather than through Delete, for the obvious reason.
func TestEntryTargetRefusesAKernelInterfaceAsAnEntry(t *testing.T) {
	for _, roots := range [][]string{nil, {"/"}} {
		set, err := NewRootSet(roots)
		if err != nil {
			t.Fatal(err)
		}
		svc := mustService(t, Options{Roots: set})
		for _, path := range []string{"/dev", "/DEV", "/proc", "/sys"} {
			if _, err := svc.entryTarget(path); !errors.Is(err, ErrPathNotAllowed) {
				t.Errorf("roots=%v: entryTarget(%q) must be refused, got: %v", roots, path, err)
			}
		}
	}
}

// The record of staging files is what keeps them out of listings, so it has to
// be emptied on every path a write can take. A leak would be unbounded growth,
// and a hidden file for good if a name were ever reused.
func TestTheStagingRecordIsEmptiedOnEveryPath(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	svc := mustService(t, Options{})
	ctx := context.Background()

	recorded := func() int {
		svc.stagingMu.Lock()
		defer svc.stagingMu.Unlock()
		return len(svc.staging)
	}
	if got := recorded(); got != 0 {
		t.Fatalf("the record starts with %d entries", got)
	}

	// A write that commits.
	if _, err := svc.Write(ctx, file, strings.NewReader("new"), 3, nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := recorded(); got != 0 {
		t.Errorf("after a committed write the record holds %d entries", got)
	}

	// A body that does not match its declared length.
	if _, err := svc.Write(ctx, file, strings.NewReader("new"), 99, nil, false); !errors.Is(err, ErrInvalidBody) {
		t.Fatalf("expected an invalid body, got: %v", err)
	}
	if got := recorded(); got != 0 {
		t.Errorf("after a refused body the record holds %d entries", got)
	}

	// A write that loses a race *during* the transfer: the file is dated into the
	// past so the first check passes, and a competing write lands while the body
	// is being read. The re-check is what refuses -- by which point a staging file
	// exists, which is the path this case is here to cover. The pre-transfer
	// check would refuse before anything was staged, and cover nothing.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}
	observed := past.UnixMilli()
	const winner = "winner"
	body := &raceReader{
		inner: strings.NewReader("stale"),
		race: func() {
			if _, err := svc.Write(ctx, file, strings.NewReader(winner), int64(len(winner)), nil, false); err != nil {
				t.Errorf("the competing write failed: %v", err)
			}
		},
	}
	if _, err := svc.Write(ctx, file, body, 5, &ExpectedMtime{Millis: observed}, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a conflict, got: %v", err)
	}
	if got := recorded(); got != 0 {
		t.Errorf("after a conflicted write the record holds %d entries", got)
	}
	if residue := stagingResidue(t, dir); len(residue) != 0 {
		t.Errorf("a staging file was left on disk: %v", residue)
	}
}

// The staging file is created with the private staging mode reduced by the
// process's umask, like any other file. Measured against a probe created the same
// way rather than asserted as a literal, because the umask is the environment's
// to choose.
func TestAStagingFileIsPrivateUntilItIsComplete(t *testing.T) {
	dir := sandbox(t)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "original")
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := mustService(t, Options{})

	probe := filepath.Join(dir, "probe")
	handle, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stagingMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	probeInfo, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	want := probeInfo.Mode().Perm()

	var observed []os.FileMode
	body := &raceReader{
		inner: strings.NewReader("replacement"),
		race: func() {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Errorf("listing the directory failed: %v", err)
				return
			}
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), tempFilePrefix) {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					continue
				}
				observed = append(observed, info.Mode().Perm())
			}
		},
	}
	if _, err := svc.Write(context.Background(), file, body, int64(len("replacement")), nil, false); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(observed) == 0 {
		t.Fatal("no staging file was visible during the write")
	}
	for _, mode := range observed {
		if mode != want {
			t.Errorf("an incomplete staging file was %v, want %v", mode, want)
		}
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("the replaced file is %v, want 0644", got)
	}
}

// A name held by a link whose target is gone is still a name that is taken, and
// "already exists" is what the caller asked about. Refusing it as not-allowed
// would be both wrong and confusing -- it names a boundary that is not involved.
func TestCreateReportsANameHeldByADanglingLinkAsExisting(t *testing.T) {
	dir := sandbox(t)
	link := filepath.Join(dir, "notes.md")
	mustSymlink(t, filepath.Join(dir, "gone.md"), link)
	svc := mustService(t, Options{})

	err := svc.Create(context.Background(), link, false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected the name to be reported as taken, got: %v", err)
	}
}

// A name held by a link whose target is gone is not a name to write over. The
// rename that commits a write would replace the link and destroy where it
// pointed, without saying so -- and the caller asked to write a file, not to
// remove a link.
func TestWriteRefusesALinkWhoseTargetDoesNotExist(t *testing.T) {
	dir := sandbox(t)
	link := filepath.Join(dir, "notes.md")
	mustSymlink(t, filepath.Join(dir, "gone.md"), link)
	svc := mustService(t, Options{})

	size := int64(len(testBody))
	_, err := svc.Write(context.Background(), link, strings.NewReader(testBody), size, nil, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected the link's missing target to be reported, got: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("the link was removed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the write replaced the link instead of refusing")
	}
}
