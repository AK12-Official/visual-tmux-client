package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRegistryIsLazy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	r, err := NewRegistry(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	hosts, err := r.List(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].ID != LocalID {
		t.Fatalf("local host: %+v, %v", hosts, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("local-only registry created state")
	}
}

func TestExplicitInvalidRegistryFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := NewRegistry(context.Background(), path, ""); err == nil {
		_ = r.Close()
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
	body, _ := json.Marshal(hosts)
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), "ciphertext") || strings.Contains(string(body), "nonce") {
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
		_ = r.Close()
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
	defer r.Close()

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
	if created.Revision != 1 || created.ConnectionRevision != 1 || !created.HasCredential || created.CredentialType != "password" {
		t.Fatalf("unexpected created host: %+v", created)
	}

	// Verify DTO json does not leak secret
	body, _ := json.Marshal(created)
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
