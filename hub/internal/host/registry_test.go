package host

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRegistryIsEager(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	ctx := context.Background()
	r, err := NewRegistry(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	// Verify durable state was created eagerly
	stateDir := filepath.Join(dir, "visual-tmux-client")
	entries, err := os.ReadDir(stateDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected durable state files in %s: %v, len=%d", stateDir, err, len(entries))
	}
	var hasDB, hasKey, hasLock bool
	for _, entry := range entries {
		switch entry.Name() {
		case "hosts.db":
			hasDB = true
		case "master.key":
			hasKey = true
		case "owner.lock":
			hasLock = true
		}
	}
	if !hasDB || !hasKey || !hasLock {
		t.Fatalf("missing durable files: db=%v, key=%v, lock=%v", hasDB, hasKey, hasLock)
	}

	// Verify local host is persisted
	hosts, err := r.List(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].ID != LocalID || hosts[0].ConnectionType != ConnectionTypeLocal {
		t.Fatalf("unexpected hosts list: %+v, %v", hosts, err)
	}
}

func TestUnwritableRegistryStateFails(t *testing.T) {
	parent := t.TempDir()
	unwritable := filepath.Join(parent, "readonly")
	if err := os.Mkdir(unwritable, 0500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unwritable, 0700) }() //nolint:errcheck // test cleanup

	target := filepath.Join(unwritable, "state")
	_, err := NewRegistry(context.Background(), target, "")
	if err == nil {
		t.Fatal("expected NewRegistry to fail on unwritable directory")
	}
}

func TestExplicitInvalidRegistryFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := NewRegistry(context.Background(), path, ""); err == nil {
		_ = r.Close() //nolint:errcheck // test cleanup
		t.Fatal("accepted invalid explicit path")
	}
}

func TestRegistryWrongKeyPreservesState(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	r, err := NewRegistry(ctx, directory, "")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := r.vault.Seal("remote", "password", Credentials{Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	record := Record{ID: "remote", Revision: 1, ConnectionRevision: 1, CredentialType: "password", Credential: envelope}
	if err := r.store.Put(ctx, record, 0); err != nil {
		t.Fatal(err)
	}
	hosts, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(hosts)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(body)
	if strings.Contains(bodyStr, "secret") || strings.Contains(bodyStr, "ciphertext") ||
		strings.Contains(bodyStr, "nonce") {
		t.Fatal("registry list exposed credential")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "hosts.db")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "master.key")
	wrong := make([]byte, 32)
	if err := os.WriteFile(keyPath, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := NewRegistry(ctx, directory, ""); err == nil {
		_ = r.Close() //nolint:errcheck // test cleanup
		t.Fatal("accepted wrong key")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed recovery modified database")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || string(key) != string(wrong) {
		t.Fatal("failed recovery replaced key")
	}
}

func TestCredentialRetentionAndReplacement(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	r, err := NewRegistry(ctx, directory, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	// 1. Create with password
	created, err := r.Save(ctx, HostInput{
		ID:             "web-server",
		Name:           "Web 1",
		Address:        "192.168.1.10",
		Port:           22,
		Username:       "ubuntu",
		CredentialType: "password",
		Credentials:    Credentials{Password: "initial-secret"},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.ConnectionRevision != 1 ||
		!created.HasCredential || created.CredentialType != "password" {
		t.Fatalf("unexpected created host: %+v", created)
	}

	// Verify DTO json does not leak secret
	body, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "initial-secret") {
		t.Fatal("DTO contains secret")
	}

	// Verify decrypted credentials
	_, creds, err := r.GetCredentials(ctx, "web-server")
	if err != nil || creds.Password != "initial-secret" {
		t.Fatalf("retrieved credentials mismatch: %+v, %v", creds, err)
	}

	// 2. Metadata update (Name change only) without supplying credentials -> must retain
	updatedName, err := r.Save(ctx, HostInput{
		ID:             "web-server",
		Name:           "Web Production",
		Address:        "192.168.1.10",
		Port:           22,
		Username:       "ubuntu",
		CredentialType: "password",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updatedName.Revision != 2 || updatedName.ConnectionRevision != 1 {
		t.Fatalf("rename should advance Revision (2) but keep ConnectionRevision (1): %+v", updatedName)
	}
	_, creds, err = r.GetCredentials(ctx, "web-server")
	if err != nil || creds.Password != "initial-secret" {
		t.Fatalf("secret was not retained after metadata update: %+v, %v", creds, err)
	}

	// 3. Invalid update: changing type to private_key without supplying private_key -> must fail and preserve old
	_, err = r.Save(ctx, HostInput{
		ID:             "web-server",
		Name:           "Web Production",
		Address:        "192.168.1.10",
		Port:           22,
		Username:       "ubuntu",
		CredentialType: "private_key",
	}, 2)
	if err == nil {
		t.Fatal("expected error when changing credential type without replacement credentials")
	}
	// Verify old record is preserved
	h, err := r.Get(ctx, "web-server")
	if err != nil || h.Revision != 2 || h.CredentialType != "password" {
		t.Fatalf("invalid update mutated state: %+v, %v", h, err)
	}

	// 4. Replacement: supply new password -> updates credentials and advances ConnectionRevision
	updatedSecret, err := r.Save(ctx, HostInput{
		ID:             "web-server",
		Name:           "Web Production",
		Address:        "192.168.1.10",
		Port:           22,
		Username:       "ubuntu",
		CredentialType: "password",
		Credentials:    Credentials{Password: "updated-secret"},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if updatedSecret.Revision != 3 || updatedSecret.ConnectionRevision != 3 {
		t.Fatalf("credential update should advance both revisions: %+v", updatedSecret)
	}
	_, creds, err = r.GetCredentials(ctx, "web-server")
	if err != nil || creds.Password != "updated-secret" {
		t.Fatalf("updated secret not saved: %+v, %v", creds, err)
	}
}

func TestDiagnosticsContainNoSavedSecrets(t *testing.T) {
	c := Credentials{
		Password:   "sensitive_password",
		PrivateKey: "-----BEGIN PRIVATE KEY-----\nMIIEvg...",
		Passphrase: "sensitive_passphrase",
	}
	for _, formatted := range []string{
		c.String(),
		c.GoString(),
		fmt.Sprintf("%v", c),
		fmt.Sprintf("%+v", c),
		fmt.Sprintf("%#v", c),
	} {
		if strings.Contains(formatted, "sensitive_password") ||
			strings.Contains(formatted, "MIIEvg") ||
			strings.Contains(formatted, "sensitive_passphrase") {
			t.Fatalf("credentials leaked in diagnostic formatting: %q", formatted)
		}
		if !strings.Contains(formatted, "[redacted credentials]") {
			t.Fatalf("expected redacted marker, got %q", formatted)
		}
	}
}

func TestLocalMetadataEdits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	loc, err := r.Get(ctx, LocalID)
	if err != nil {
		t.Fatalf("get local: %v", err)
	}
	if loc.ConnectionType != ConnectionTypeLocal || loc.Revision != 1 || loc.ConnectionRevision != 1 {
		t.Fatalf("unexpected bootstrap local: %+v", loc)
	}

	updated, err := r.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local Workstation",
		ConnectionType: ConnectionTypeLocal,
	}, 1)
	if err != nil {
		t.Fatalf("update local name: %v", err)
	}
	if updated.Name != "Local Workstation" || updated.Revision != 2 || updated.ConnectionRevision != 1 {
		t.Fatalf("metadata update should advance Revision to 2 and keep ConnectionRevision at 1: %+v", updated)
	}
	if updated.ConnectionType != ConnectionTypeLocal {
		t.Fatalf("expected ConnectionTypeLocal, got %s", updated.ConnectionType)
	}

	got, err := r.Get(ctx, LocalID)
	if err != nil || got.Name != "Local Workstation" || got.Revision != 2 || got.ConnectionRevision != 1 {
		t.Fatalf("persisted local mismatch: %+v, %v", got, err)
	}
}

func TestLocalIrrelevantFieldRejection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	// Update local with address -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local",
		ConnectionType: ConnectionTypeLocal,
		Address:        "127.0.0.1",
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "irrelevant") {
		t.Fatalf("expected irrelevant fields error, got: %v", err)
	}

	// Update local with port -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local",
		ConnectionType: ConnectionTypeLocal,
		Port:           22,
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "irrelevant") {
		t.Fatalf("expected irrelevant fields error, got: %v", err)
	}

	// Update local with credentials -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local",
		ConnectionType: ConnectionTypeLocal,
		Credentials:    Credentials{Password: "secret"},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "irrelevant") {
		t.Fatalf("expected irrelevant fields error, got: %v", err)
	}

	// Remove local host to test create
	if err := r.Delete(ctx, LocalID, 1); err != nil {
		t.Fatalf("delete local: %v", err)
	}

	// Create local with host key -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             "my-local",
		Name:           "My Local",
		ConnectionType: ConnectionTypeLocal,
		HostKey:        []byte{1, 2, 3},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "irrelevant") {
		t.Fatalf("expected irrelevant fields error on create, got: %v", err)
	}
}

func TestImmutableConnectionType(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	// Attempt to change local to ssh -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local",
		ConnectionType: ConnectionTypeSSH,
		Address:        "127.0.0.1",
		Username:       "user",
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "cannot change host connection type") {
		t.Fatalf("expected cannot change connection type error, got: %v", err)
	}

	// Create SSH host
	sshHost, err := r.Save(ctx, HostInput{
		ID:             "remote-1",
		Name:           "Remote 1",
		ConnectionType: ConnectionTypeSSH,
		Address:        "192.168.1.1",
		Username:       "root",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to change ssh to local -> must fail
	_, err = r.Save(ctx, HostInput{
		ID:             sshHost.ID,
		Name:           "Remote 1",
		ConnectionType: ConnectionTypeLocal,
	}, sshHost.Revision)
	if err == nil || !strings.Contains(err.Error(), "cannot change host connection type") {
		t.Fatalf("expected cannot change connection type error, got: %v", err)
	}
}

func TestConcurrentSingletonLocalRegistration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	// While local host exists, attempting to register a second local host must fail
	_, err = r.Save(ctx, HostInput{
		ID:             "second-local",
		Name:           "Second Local",
		ConnectionType: ConnectionTypeLocal,
	}, 0)
	if err == nil || !errors.Is(err, ErrDuplicateLocal) {
		t.Fatalf("expected ErrDuplicateLocal, got: %v", err)
	}

	// Delete local host
	if err := r.Delete(ctx, LocalID, 1); err != nil {
		t.Fatal(err)
	}

	// Concurrently attempt to register two local hosts
	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		hostID := ID(fmt.Sprintf("loc-%d", i))
		go func(id ID) {
			_, err := r.Save(ctx, HostInput{
				ID:             id,
				Name:           string(id),
				ConnectionType: ConnectionTypeLocal,
			}, 0)
			errs <- err
		}(hostID)
	}

	var successes, conflicts int
	for i := 0; i < n; i++ {
		err := <-errs
		if err == nil {
			successes++
		} else if errors.Is(err, ErrDuplicateLocal) {
			conflicts++
		} else {
			t.Errorf("unexpected error from local registration: %v", err)
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful local registration, got %d (conflicts: %d)", successes, conflicts)
	}

	hosts, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var localCount int
	for _, h := range hosts {
		if h.ConnectionType == ConnectionTypeLocal {
			localCount++
		}
	}
	if localCount != 1 {
		t.Fatalf("expected exactly 1 local host in registry, got %d", localCount)
	}
}

func TestPersistentHostLifecycleAndSettings(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// 1. Initial registry: local host exists, default setting points to local, revision 1
	r1, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := r1.List(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].ID != LocalID {
		t.Fatalf("unexpected initial hosts: %+v, %v", hosts, err)
	}
	settings, err := r1.GetSettings(ctx)
	if err != nil || settings.DefaultHostID == nil || *settings.DefaultHostID != LocalID || settings.Revision != 1 {
		t.Fatalf("unexpected initial settings: %+v, %v", settings, err)
	}

	// 2. Rename local host
	updatedLocal, err := r1.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Local Workstation",
		ConnectionType: ConnectionTypeLocal,
	}, 1)
	if err != nil {
		t.Fatalf("update local name: %v", err)
	}
	if updatedLocal.Name != "Local Workstation" || updatedLocal.Revision != 2 || updatedLocal.ConnectionRevision != 1 {
		t.Fatalf("unexpected updated local: %+v", updatedLocal)
	}

	// 3. Restart: verify renamed local and default survive
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}

	r2, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	loc, err := r2.Get(ctx, LocalID)
	if err != nil || loc.Name != "Local Workstation" || loc.Revision != 2 || loc.ConnectionRevision != 1 {
		t.Fatalf("local host after restart: %+v, %v", loc, err)
	}
	settings, err = r2.GetSettings(ctx)
	if err != nil || settings.DefaultHostID == nil || *settings.DefaultHostID != LocalID || settings.Revision != 1 {
		t.Fatalf("settings after restart: %+v, %v", settings, err)
	}

	// 4. Register SSH host and switch default host
	sshHost, err := r2.Save(ctx, HostInput{
		ID:             "remote-srv",
		Name:           "Production Server",
		ConnectionType: ConnectionTypeSSH,
		Address:        "192.168.1.50",
		Username:       "deploy",
	}, 0)
	if err != nil {
		t.Fatalf("save ssh host: %v", err)
	}

	// Stale settings update must be rejected
	_, err = r2.UpdateDefaultHost(ctx, &sshHost.ID, 999)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision for stale revision, got: %v", err)
	}
	// Missing expected revision (0) must be rejected
	_, err = r2.UpdateDefaultHost(ctx, &sshHost.ID, 0)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision for revision 0, got: %v", err)
	}
	// Non-existent host cannot be set as default
	nonExistent := ID("non-existent")
	_, err = r2.UpdateDefaultHost(ctx, &nonExistent, settings.Revision)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent default host, got: %v", err)
	}

	// Valid update to SSH host default
	settings, err = r2.UpdateDefaultHost(ctx, &sshHost.ID, settings.Revision)
	if err != nil {
		t.Fatalf("update default host to ssh: %v", err)
	}
	if settings.DefaultHostID == nil || *settings.DefaultHostID != "remote-srv" || settings.Revision != 2 {
		t.Fatalf("unexpected settings after update: %+v", settings)
	}

	// 5. Restart: verify remote default survives
	if err := r2.Close(); err != nil {
		t.Fatal(err)
	}

	r3, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close() //nolint:errcheck // test cleanup
	settings, err = r3.GetSettings(ctx)
	if err != nil || settings.DefaultHostID == nil ||
		*settings.DefaultHostID != "remote-srv" || settings.Revision != 2 {
		t.Fatalf("settings after restart with remote default: %+v, %v", settings, err)
	}

	// 6. Delete the default host: default must be cleared atomically
	if err := r3.Delete(ctx, "remote-srv", sshHost.Revision); err != nil {
		t.Fatalf("delete remote-srv: %v", err)
	}
	settings, err = r3.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultHostID != nil {
		t.Fatalf("expected nil default host after deleting default host, got: %v", *settings.DefaultHostID)
	}
	if settings.Revision != 3 {
		t.Fatalf("expected settings revision 3 after clearing default, got: %d", settings.Revision)
	}

	// 7. Delete local host: registry is now empty
	if err := r3.Delete(ctx, LocalID, 2); err != nil {
		t.Fatalf("delete local: %v", err)
	}
	hosts, err = r3.List(ctx)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty registry, got: %+v, %v", hosts, err)
	}

	// 8. Restart empty registry: remains empty, local is NOT recreated!
	if err := r3.Close(); err != nil {
		t.Fatal(err)
	}

	r4, err := NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	hosts, err = r4.List(ctx)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("expected empty registry after restart, got: %+v, %v", hosts, err)
	}
	settings, err = r4.GetSettings(ctx)
	if err != nil || settings.DefaultHostID != nil {
		t.Fatalf("expected nil default host on empty registry, got: %+v, %v", settings, err)
	}

	// 9. Re-register local host with ID "local": revisions must strictly exceed previous watermark
	reLocal, err := r4.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Re-registered Local",
		ConnectionType: ConnectionTypeLocal,
	}, 0)
	if err != nil {
		t.Fatalf("re-register local: %v", err)
	}
	if reLocal.Revision <= 2 || reLocal.ConnectionRevision <= 1 {
		t.Fatalf("re-registered revisions must exceed watermark (rev>2, connRev>1): %+v", reLocal)
	}
	if reLocal.ConnectionType != ConnectionTypeLocal {
		t.Fatalf("expected ConnectionTypeLocal, got: %s", reLocal.ConnectionType)
	}

	// Stale edit on re-registered host is rejected
	_, err = r4.Save(ctx, HostInput{
		ID:             LocalID,
		Name:           "Stale Rename",
		ConnectionType: ConnectionTypeLocal,
	}, 1)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision, got: %v", err)
	}

	// Setting default to re-registered host
	settings, err = r4.UpdateDefaultHost(ctx, &reLocal.ID, settings.Revision)
	if err != nil {
		t.Fatalf("update default host to re-registered local: %v", err)
	}
	if settings.DefaultHostID == nil || *settings.DefaultHostID != LocalID || settings.Revision != 4 {
		t.Fatalf("unexpected settings after re-registering local: %+v", settings)
	}

	// Clearing default explicitly
	settings, err = r4.UpdateDefaultHost(ctx, nil, settings.Revision)
	if err != nil {
		t.Fatalf("clear default host: %v", err)
	}
	if settings.DefaultHostID != nil || settings.Revision != 5 {
		t.Fatalf("unexpected settings after explicit clear: %+v", settings)
	}

	if err := r4.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryWrongKeyPreventsV1Migration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hosts.db")
	vault, err := NewVault(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := vault.Seal("remote", "password", Credentials{Password: "fixture-only"})
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		ID:                 "remote",
		Revision:           1,
		ConnectionRevision: 1,
		Name:               "fixture",
		Address:            "example.invalid",
		Port:               22,
		Username:           "u",
		CredentialType:     "password",
		Credential:         encrypted,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema := "CREATE TABLE hosts(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,record BLOB NOT NULL); " +
		"PRAGMA user_version=1;"
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO hosts VALUES(?,?,?)", "remote", 1, data); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()             //nolint:errcheck // test cleanup
	_ = os.Chmod(dbPath, 0600) //nolint:errcheck // test setup

	// Write wrong 32-byte key
	if err = os.WriteFile(filepath.Join(dir, "master.key"), make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry(context.Background(), dir, "")
	if err == nil {
		_ = reg.Close() //nolint:errcheck // test cleanup
		t.Fatal("expected wrong key to fail NewRegistry")
	}

	// Verify database is still user_version=1 and uncorrupted
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("expected user_version to remain 1 after wrong key failure, got %d", version)
	}
}
