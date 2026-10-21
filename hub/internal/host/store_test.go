package host

import (
	"context"
	"database/sql"
	"encoding/json"
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
	t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck // cleanup
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
				_ = s.Close() //nolint:errcheck // cleanup
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
	defer func() { _ = s.Close() }() //nolint:errcheck // cleanup
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
		_ = s.Close() //nolint:errcheck // cleanup
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
	orig, err := os.ReadFile(filepath.Join(dir, "hosts.db"))
	if err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != string(bak) {
		t.Fatal("backup content differs from original")
	}
}

func TestStoreSchemaMigrationV1ToV2(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// 1. Create a schema v1 database fixture
	dbPath := filepath.Join(dir, "hosts.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	v1Record := map[string]any{
		"id":                 "old-ssh-1",
		"revision":           5,
		"connectionRevision": 3,
		"name":               "Production Server",
		"address":            "prod.example.com",
		"port":               2222,
		"username":           "deploy",
		"credentialType":     "password",
		"credential":         []byte("encrypted-ciphertext-bytes"),
		"hostKey":            []byte("pinned-hostkey-bytes"),
	}
	data, err := json.Marshal(v1Record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE hosts (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL CHECK(revision > 0),
			record BLOB NOT NULL
		);
		PRAGMA user_version = 1;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO hosts(id, revision, record) VALUES (?, ?, ?)", "old-ssh-1", 5, data); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Open with OpenStore which triggers migration to v2
	store, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	defer func() { _ = store.Close() }() //nolint:errcheck // cleanup

	// Verify backup file hosts.db.v1.bak was created
	bakPath := filepath.Join(dir, "hosts.db.v1.bak")
	if _, err := os.Stat(bakPath); err != nil {
		t.Fatalf("expected v1 backup file: %v", err)
	}

	// Verify PRAGMA user_version is 2
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("expected schema version 2, got %d", version)
	}

	// Verify old SSH host was retained with unchanged ID, revision, ciphertext, trust
	rec, err := store.Get(ctx, "old-ssh-1")
	if err != nil {
		t.Fatalf("get migrated host: %v", err)
	}
	if rec.ID != "old-ssh-1" || rec.Revision != 5 || rec.ConnectionRevision != 3 {
		t.Fatalf("migrated revisions mismatch: %+v", rec)
	}
	if rec.ConnectionType != ConnectionTypeSSH {
		t.Fatalf("expected ConnectionTypeSSH, got %s", rec.ConnectionType)
	}
	if string(rec.Credential) != "encrypted-ciphertext-bytes" {
		t.Fatalf("ciphertext was corrupted: %s", string(rec.Credential))
	}
	if string(rec.HostKey) != "pinned-hostkey-bytes" {
		t.Fatalf("trust hostKey was corrupted: %s", string(rec.HostKey))
	}

	// Verify watermark was seeded for old-ssh-1
	nextRev, nextConnRev, err := store.NextRevisions(ctx, "old-ssh-1")
	if err != nil {
		t.Fatal(err)
	}
	if nextRev != 6 || nextConnRev != 4 {
		t.Fatalf("expected next revisions 6/4, got %d/%d", nextRev, nextConnRev)
	}

	// Verify local record was bootstrapped
	loc, err := store.Get(ctx, LocalID)
	if err != nil {
		t.Fatalf("get bootstrap local: %v", err)
	}
	if loc.ConnectionType != ConnectionTypeLocal || loc.Revision != 1 || loc.ConnectionRevision != 1 {
		t.Fatalf("bootstrap local mismatch: %+v", loc)
	}

	// Verify default settings was bootstrapped to local
	settings, err := store.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultHostID == nil ||
		*settings.DefaultHostID != LocalID ||
		settings.Revision != 1 {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	// Verify metadata initialized marker
	var marker string
	err = store.db.QueryRowContext(
		ctx,
		"SELECT value FROM metadata WHERE key = 'initialized'",
	).Scan(&marker)
	if err != nil || marker != "1" {
		t.Fatalf("expected initialized marker '1', got %q, %v", marker, err)
	}
}

func TestStoreInterruptedAndRepeatedMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Fresh store creates schema v2 and bootstraps
	store1, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Verify 1 host (local)
	list1, err := store1.List(ctx)
	if err != nil || len(list1) != 1 {
		t.Fatalf("expected 1 host on fresh store, got %d (%v)", len(list1), err)
	}
	// Delete the local host
	if err := store1.Delete(ctx, LocalID, 1); err != nil {
		t.Fatal(err)
	}
	// Verify registry is empty and default is unset
	listAfterDel, err := store1.List(ctx)
	if err != nil || len(listAfterDel) != 0 {
		t.Fatalf("expected 0 hosts after deletion, got %d", len(listAfterDel))
	}
	settingsAfterDel, err := store1.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settingsAfterDel.DefaultHostID != nil {
		t.Fatalf("expected nil default host after deletion, got %v", *settingsAfterDel.DefaultHostID)
	}
	if err := store1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the store (repeated startup)
	store2, err := OpenStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }() //nolint:errcheck // cleanup

	// An initialized empty registry remains empty after restart and default stays unset!
	list2, err := store2.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list2) != 0 {
		t.Fatalf("initialized empty registry recreated hosts on restart: %+v", list2)
	}
	settings2, err := store2.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings2.DefaultHostID != nil {
		t.Fatalf("initialized empty registry recreated default host on restart: %v", *settings2.DefaultHostID)
	}

	// Watermark for local ID survived deletion and restart!
	nextRev, nextConnRev, err := store2.NextRevisions(ctx, LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if nextRev != 2 || nextConnRev != 2 {
		t.Fatalf("expected watermark 2/2 for deleted local host, got %d/%d", nextRev, nextConnRev)
	}
}
