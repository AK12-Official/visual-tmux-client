package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Registry lazily owns the persistent store. A fresh default local-only run
// performs no writes; an explicitly selected location is checked eagerly.
type Registry struct {
	mu        sync.Mutex
	directory string
	keyPath   string
	store     *Store
	vault     *Vault
	closed    bool
}

func NewRegistry(ctx context.Context, directory, keyPath string) (*Registry, error) {
	explicit := directory != "" || keyPath != ""
	if directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			if explicit {
				return nil, err
			}
			return &Registry{}, nil
		}
		directory = filepath.Join(base, "visual-tmux-client")
	}
	if keyPath == "" {
		keyPath = filepath.Join(directory, "master.key")
	}
	r := &Registry{directory: directory, keyPath: keyPath}
	_, err := os.Lstat(filepath.Join(directory, "hosts.db"))
	if explicit || err == nil {
		if err := r.initialize(ctx); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	return r, nil
}

func (r *Registry) initialize(ctx context.Context) error {
	if r.closed {
		return errors.New("host registry is closed")
	}
	if r.store != nil {
		return nil
	}
	if r.directory == "" {
		return errors.New("default host state directory unavailable")
	}
	_, err := os.Lstat(filepath.Join(r.directory, "hosts.db"))
	existing := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Check an existing key before opening a database, so key failure cannot
	// initialize or migrate a schema. Fresh keys are created after store locking.
	var vault *Vault
	if existing {
		vault, err = LoadVault(r.keyPath, true)
		if err != nil {
			return err
		}
	}
	store, err := OpenStore(ctx, r.directory)
	if err != nil {
		return err
	}
	fail := func(err error) error { _ = store.Close(); return err }
	if !existing {
		vault, err = LoadVault(r.keyPath, false)
		if err != nil {
			return fail(err)
		}
	}
	records, err := store.List(ctx)
	if err != nil {
		return fail(err)
	}
	for _, record := range records {
		if _, err := vault.Open(record.ID, record.CredentialType, record.Credential); err != nil {
			return fail(err)
		}
	}
	r.store, r.vault = store, vault
	return nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.store != nil {
		return r.store.Close()
	}
	return nil
}

// Host is the only registry projection suitable for authenticated API replies.
// Host keys and credentials are deliberately excluded from ordinary listings.
type Host struct {
	ID                 ID       `json:"id"`
	Revision           Revision `json:"revision"`
	ConnectionRevision Revision `json:"connectionRevision"`
	Name               string   `json:"name"`
	Address            string   `json:"address,omitempty"`
	Port               uint16   `json:"port,omitempty"`
	Username           string   `json:"username,omitempty"`
	CredentialType     string   `json:"credentialType,omitempty"`
	HasCredential      bool     `json:"hasCredential"`
	Trusted            bool     `json:"trusted"`
}

func project(record Record) Host {
	return Host{ID: record.ID, Revision: record.Revision, ConnectionRevision: record.ConnectionRevision, Name: record.Name, Address: record.Address, Port: record.Port, Username: record.Username, CredentialType: record.CredentialType, HasCredential: len(record.Credential) > 0, Trusted: len(record.HostKey) > 0}
}

func (r *Registry) List(ctx context.Context) ([]Host, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("host registry is closed")
	}
	out := []Host{{ID: LocalID, Revision: LocalRevision, ConnectionRevision: LocalRevision, Name: "Local", Trusted: true}}
	if r.store == nil {
		return out, nil
	}
	records, err := r.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		out = append(out, project(record))
	}
	return out, nil
}

// HostInput provides input for registering or updating a remote host.
type HostInput struct {
	ID             ID          `json:"id"`
	Name           string      `json:"name"`
	Address        string      `json:"address"`
	Port           uint16      `json:"port"`
	Username       string      `json:"username"`
	CredentialType string      `json:"credentialType"`
	Credentials    Credentials `json:"credentials"`
	HostKey        []byte      `json:"hostKey"`
}

func validHostID(id ID) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// Get returns the redacted host projection or ErrNotFound.
func (r *Registry) Get(ctx context.Context, id ID) (Host, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Host{}, errors.New("host registry is closed")
	}
	if id == LocalID {
		return Host{ID: LocalID, Revision: LocalRevision, ConnectionRevision: LocalRevision, Name: "Local", Trusted: true}, nil
	}
	if r.store == nil {
		return Host{}, ErrNotFound
	}
	record, err := r.store.Get(ctx, id)
	if err != nil {
		return Host{}, err
	}
	return project(record), nil
}

// Delete removes a registered host and its credentials; the local host cannot be deleted.
func (r *Registry) Delete(ctx context.Context, id ID, expected Revision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("host registry is closed")
	}
	if id == LocalID {
		return ErrUnsupported
	}
	if r.store == nil {
		return ErrNotFound
	}
	rec, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if expected > 0 && rec.Revision != expected {
		return ErrStaleRevision
	}
	return r.store.Delete(ctx, id, rec.Revision)
}

// GetCredentials retrieves a host record and its decrypted credentials for connection use.
func (r *Registry) GetCredentials(ctx context.Context, id ID) (Record, Credentials, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Record{}, Credentials{}, errors.New("host registry is closed")
	}
	if id == LocalID {
		return Record{}, Credentials{}, ErrUnsupported
	}
	if r.store == nil {
		return Record{}, Credentials{}, ErrNotFound
	}
	record, err := r.store.Get(ctx, id)
	if err != nil {
		return Record{}, Credentials{}, err
	}
	if len(record.Credential) == 0 {
		return record, Credentials{}, nil
	}
	creds, err := r.vault.Open(record.ID, record.CredentialType, record.Credential)
	if err != nil {
		return Record{}, Credentials{}, err
	}
	return record, creds, nil
}

// Save creates or updates a host record with credential replacement/retention and revision checks.
func (r *Registry) Save(ctx context.Context, input HostInput, expected Revision) (Host, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Host{}, errors.New("host registry is closed")
	}
	if err := r.initialize(ctx); err != nil {
		return Host{}, fmt.Errorf("initialize storage: %w", err)
	}
	if input.ID == LocalID || !validHostID(input.ID) {
		return Host{}, errors.New("invalid host identifier")
	}
	trimmedName := strings.TrimSpace(input.Name)
	if trimmedName == "" {
		return Host{}, errors.New("host name cannot be empty")
	}
	trimmedAddr := strings.TrimSpace(input.Address)
	if trimmedAddr == "" {
		return Host{}, errors.New("host address cannot be empty")
	}
	trimmedUser := strings.TrimSpace(input.Username)
	if trimmedUser == "" {
		return Host{}, errors.New("host username cannot be empty")
	}
	port := input.Port
	if port == 0 {
		port = 22
	}

	if expected == 0 {
		var credBytes []byte
		if input.CredentialType != "" {
			if input.CredentialType != "password" && input.CredentialType != "private_key" {
				return Host{}, errors.New("invalid credential type")
			}
			if (input.CredentialType == "password" && input.Credentials.Password == "") ||
				(input.CredentialType == "private_key" && input.Credentials.PrivateKey == "") {
				return Host{}, errors.New("credentials required for configured credential type")
			}
			var err error
			credBytes, err = r.vault.Seal(input.ID, input.CredentialType, input.Credentials)
			if err != nil {
				return Host{}, err
			}
		}
		newRecord := Record{
			ID:                 input.ID,
			Revision:           1,
			ConnectionRevision: 1,
			Name:               trimmedName,
			Address:            trimmedAddr,
			Port:               port,
			Username:           trimmedUser,
			CredentialType:     input.CredentialType,
			Credential:         credBytes,
			HostKey:            input.HostKey,
		}
		if err := r.store.Put(ctx, newRecord, 0); err != nil {
			return Host{}, err
		}
		return project(newRecord), nil
	}

	old, err := r.store.Get(ctx, input.ID)
	if err != nil {
		return Host{}, err
	}
	if old.Revision != expected {
		return Host{}, ErrStaleRevision
	}

	credType := old.CredentialType
	credBytes := old.Credential
	credChanged := false

	hasNewSecret := input.Credentials.Password != "" || input.Credentials.PrivateKey != "" || input.Credentials.Passphrase != ""
	if hasNewSecret {
		if input.CredentialType != "" {
			credType = input.CredentialType
		}
		if credType != "password" && credType != "private_key" {
			return Host{}, errors.New("invalid credential type")
		}
		if (credType == "password" && input.Credentials.Password == "") ||
			(credType == "private_key" && input.Credentials.PrivateKey == "") {
			return Host{}, errors.New("credentials required for configured credential type")
		}
		sealed, err := r.vault.Seal(input.ID, credType, input.Credentials)
		if err != nil {
			return Host{}, err
		}
		credBytes = sealed
		credChanged = true
	} else {
		// Omitted replacement credentials retain existing credentials
		if input.CredentialType != "" && input.CredentialType != old.CredentialType {
			return Host{}, errors.New("cannot change credential type without supplying replacement credentials")
		}
	}

	var hostKey []byte
	trustChanged := false
	if len(input.HostKey) > 0 {
		hostKey = input.HostKey
		trustChanged = !bytes.Equal(input.HostKey, old.HostKey)
	} else if trimmedAddr != old.Address || port != old.Port {
		// Endpoint edit invalidates trust for previous endpoint
		hostKey = nil
		trustChanged = len(old.HostKey) > 0
	} else {
		hostKey = old.HostKey
	}

	newRevision := old.Revision + 1
	newConnRevision := old.ConnectionRevision
	if trimmedAddr != old.Address || port != old.Port || trimmedUser != old.Username || credChanged || trustChanged {
		newConnRevision = newRevision
	}

	newRecord := Record{
		ID:                 input.ID,
		Revision:           newRevision,
		ConnectionRevision: newConnRevision,
		Name:               trimmedName,
		Address:            trimmedAddr,
		Port:               port,
		Username:           trimmedUser,
		CredentialType:     credType,
		Credential:         credBytes,
		HostKey:            hostKey,
	}

	if err := r.store.Put(ctx, newRecord, expected); err != nil {
		return Host{}, err
	}
	return project(newRecord), nil
}

func (r *Registry) AddPendingCleanup(ctx context.Context, rec PendingCleanupRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("host registry is closed")
	}
	if err := r.initialize(ctx); err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	return r.store.AddPendingCleanup(ctx, rec)
}

func (r *Registry) RemovePendingCleanup(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.store == nil {
		return nil
	}
	return r.store.RemovePendingCleanup(ctx, id)
}

func (r *Registry) ListPendingCleanups(ctx context.Context, hostID ID) ([]PendingCleanupRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("host registry is closed")
	}
	if r.store == nil {
		if err := r.initialize(ctx); err != nil {
			return nil, err
		}
	}
	return r.store.ListPendingCleanups(ctx, hostID)
}
