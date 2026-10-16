package host

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialBindingAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	v, err := LoadVault(path, false)
	if err != nil {
		t.Fatal(err)
	}
	secret := Credentials{PrivateKey: "private key", Passphrase: "secret"}
	a, err := v.Seal("a", "private_key", secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Seal("a", "private_key", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, []byte(secret.Passphrase)) {
		t.Fatal("nonce reuse or plaintext disclosure")
	}
	v, err = LoadVault(path, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Open("a", "private_key", a)
	if err != nil || got != secret {
		t.Fatal("restart failed")
	}
	if _, err := v.Open("b", "private_key", a); err == nil {
		t.Fatal("cross-host credential accepted")
	}
	if _, err := v.Open("a", "password", a); err == nil {
		t.Fatal("cross-type credential accepted")
	}
	modified := bytes.Replace(a, []byte(`"version":1`), []byte(`"version":2`), 1)
	if _, err := v.Open("a", "private_key", modified); err == nil {
		t.Fatal("modified version accepted")
	}
	wrong, _ := NewVault(make([]byte, 32))
	if _, err := wrong.Open("a", "private_key", a); err == nil {
		t.Fatal("wrong key accepted")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key is not private")
	}
}

func TestMissingKeyDoesNotCreateReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := LoadVault(path, true); err == nil {
		t.Fatal("accepted missing key")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created replacement key")
	}
}
