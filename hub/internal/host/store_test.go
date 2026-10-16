package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStoreTransactionsAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := Record{ID: "remote", Revision: 1, ConnectionRevision: 1, Name: "original"}
	if err := s.Put(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	updated := r
	updated.Revision = 2
	updated.Name = "updated"
	if err := s.Put(ctx, updated, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, updated, 1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale update: %v", err)
	}
	if err := s.Delete(ctx, r.ID, 1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale delete: %v", err)
	}
	// Abort a real transaction after a write, then verify the durable value.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM hosts"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.Name != "updated" || got.Revision != 2 {
		t.Fatalf("durable record: %+v, %v", got, err)
	}
	if err := s.Delete(ctx, r.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestStoreExclusiveOwnership(t *testing.T) {
	if directory := os.Getenv("VTC_TEST_OWNED_STORE"); directory != "" {
		s, err := OpenStore(context.Background(), directory)
		if !errors.Is(err, ErrStoreOwned) {
			if s != nil {
				_ = s.Close()
			}
			t.Fatalf("second process opened owned store: %v", err)
		}
		return
	}
	directory := t.TempDir()
	s, err := OpenStore(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreExclusiveOwnership$")
	cmd.Env = append(os.Environ(), "VTC_TEST_OWNED_STORE="+directory)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ownership: %s: %v", out, err)
	}
}

func TestStoreRefusesUnknownSchemaWithoutChangingIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hosts.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStore(ctx, dir); err == nil {
		_ = s.Close()
		t.Fatal("accepted unknown schema")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("unsupported database was modified")
	}
}

func TestStoreMigrationBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{ID: "host-1", Revision: 1, ConnectionRevision: 1, Name: "Test Host"}
	if err := s.Put(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Trigger backupState directly for testing safe migration backup
	if err := backupState(dir, 1); err != nil {
		t.Fatal(err)
	}
	bakPath := filepath.Join(dir, "hosts.db.v1.bak")
	info, err := os.Stat(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup file permissions should be 0600, got %o", info.Mode().Perm())
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "hosts.db"))
	bak, _ := os.ReadFile(bakPath)
	if string(orig) != string(bak) {
		t.Fatal("backup content differs from original")
	}
}
