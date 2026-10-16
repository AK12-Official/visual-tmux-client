package host

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const credentialEnvelopeVersion = 1

// Credentials is transient secret input, never an API response or log value.
type Credentials struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func (Credentials) String() string   { return "[redacted credentials]" }
func (Credentials) GoString() string { return "[redacted credentials]" }

type credentialEnvelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type Vault struct{ aead cipher.AEAD }

func NewVault(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func credentialAAD(id ID, kind string, version int) []byte {
	data, _ := json.Marshal(struct {
		ID      ID
		Kind    string
		Version int
	}{id, kind, version})
	return data
}

func (v *Vault) Seal(id ID, kind string, credential Credentials) ([]byte, error) {
	plaintext, err := json.Marshal(credential)
	if err != nil {
		return nil, errors.New("invalid credential")
	}
	defer clear(plaintext)
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	envelope := credentialEnvelope{Version: credentialEnvelopeVersion, Nonce: nonce}
	envelope.Ciphertext = v.aead.Seal(nil, nonce, plaintext, credentialAAD(id, kind, envelope.Version))
	return json.Marshal(envelope)
}

func (v *Vault) Open(id ID, kind string, data []byte) (Credentials, error) {
	var envelope credentialEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Version != credentialEnvelopeVersion || len(envelope.Nonce) != v.aead.NonceSize() {
		return Credentials{}, errors.New("invalid encrypted credential")
	}
	plaintext, err := v.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, credentialAAD(id, kind, envelope.Version))
	if err != nil {
		return Credentials{}, errors.New("credential decryption failed; verify the master key and registry backup")
	}
	defer clear(plaintext)
	var credential Credentials
	if err := json.Unmarshal(plaintext, &credential); err != nil {
		return Credentials{}, errors.New("invalid decrypted credential")
	}
	return credential, nil
}

// LoadVault never generates a replacement key when existing state needs one.
// Initialization is called only while the state store ownership lock is held.
func LoadVault(path string, existingState bool) (*Vault, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("master key must be a private regular file")
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		defer clear(key)
		return NewVault(key)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if existingState {
		return nil, errors.New("master key missing for existing host state")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create master key: %w", err)
	}
	_, writeErr := file.Write(key)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return nil, err
	}
	return NewVault(key)
}
