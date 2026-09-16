package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	_, err := svc.Write(context.Background(), file, strings.NewReader("replacement"), 11, &stale)
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

	if _, err := svc.Write(context.Background(), file, strings.NewReader("replacement"), 11, nil); err != nil {
		t.Fatalf("a forced overwrite must succeed: %v", err)
	}
	if got := readFile(t, file); got != "replacement" {
		t.Errorf("expected the replacement contents, got %q", got)
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

	first, err := svc.Write(context.Background(), file, strings.NewReader("one"), 3, nil)
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	// The returned value has to be the file's real modification time, not a
	// guess: the next save is only free of a spurious conflict because it is.
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if first != info.ModTime().UnixMilli() {
		t.Errorf("write reported mtime %d but the file has %d", first, info.ModTime().UnixMilli())
	}
	if _, err := svc.Write(context.Background(), file, strings.NewReader("two"), 3, &first); err != nil {
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
	_, err := svc.Write(context.Background(), file, strings.NewReader("x"), 1, &observed)
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

	_, err := svc.Write(context.Background(), file, strings.NewReader("short"), 500, nil)
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

	_, err := svc.Write(context.Background(), file, strings.NewReader("x"), 64, nil)
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
	_, err := svc.Write(context.Background(), file, &failingReader{remaining: partial}, declared, nil)
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
