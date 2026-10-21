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
	if directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("user config dir: %w", err)
		}
		directory = filepath.Join(base, "visual-tmux-client")
	}
	if keyPath == "" {
		keyPath = filepath.Join(directory, "master.key")
	}
	r := &Registry{directory: directory, keyPath: keyPath}
	if err := r.initialize(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

const defaultLocalHostName = "Local"

func verifyStoreCredentials(ctx context.Context, store *Store, vault *Vault) error {
	records, err := store.List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if len(record.Credential) > 0 {
			if _, err := vault.Open(record.ID, record.CredentialType, record.Credential); err != nil {
				return err
			}
		}
	}
	return nil
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
	var validator func(Record) error
	if vault != nil {
		validator = func(rec Record) error {
			if len(rec.Credential) > 0 {
				_, err := vault.Open(rec.ID, rec.CredentialType, rec.Credential)
				return err
			}
			return nil
		}
	}
	store, err := OpenStore(ctx, r.directory, WithCredentialValidator(validator))
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = store.Close() //nolint:errcheck // best-effort store cleanup
		return err
	}
	if !existing {
		vault, err = LoadVault(r.keyPath, false)
		if err != nil {
			return fail(err)
		}
	}
	if err := verifyStoreCredentials(ctx, store, vault); err != nil {
		return fail(err)
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
	ID                 ID             `json:"id"`
	Revision           Revision       `json:"revision"`
	ConnectionRevision Revision       `json:"connectionRevision"`
	Name               string         `json:"name"`
	ConnectionType     ConnectionType `json:"connectionType"`
	Address            string         `json:"address,omitempty"`
	Port               uint16         `json:"port,omitempty"`
	Username           string         `json:"username,omitempty"`
	CredentialType     string         `json:"credentialType,omitempty"`
	HasCredential      bool           `json:"hasCredential"`
	Trusted            bool           `json:"trusted"`
}

func project(record Record) Host {
	connType := record.ConnectionType
	if connType == "" {
		if record.ID == LocalID {
			connType = ConnectionTypeLocal
		} else {
			connType = ConnectionTypeSSH
		}
	}
	h := Host{
		ID:                 record.ID,
		Revision:           record.Revision,
		ConnectionRevision: record.ConnectionRevision,
		Name:               record.Name,
		ConnectionType:     connType,
	}
	if connType == ConnectionTypeSSH {
		h.Address = record.Address
		h.Port = record.Port
		h.Username = record.Username
		h.CredentialType = record.CredentialType
		h.HasCredential = len(record.Credential) > 0
		h.Trusted = len(record.HostKey) > 0
	}
	return h
}

func (r *Registry) List(ctx context.Context) ([]Host, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("host registry is closed")
	}
	if r.store == nil {
		return []Host{{
			ID:                 LocalID,
			Revision:           LocalRevision,
			ConnectionRevision: LocalRevision,
			Name:               defaultLocalHostName,
			ConnectionType:     ConnectionTypeLocal,
			Trusted:            false,
		}}, nil
	}
	records, err := r.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(records))
	for _, record := range records {
		out = append(out, project(record))
	}
	return out, nil
}

// HostInput provides input for registering or updating a host.
type HostInput struct {
	ID             ID             `json:"id"`
	ConnectionType ConnectionType `json:"connectionType,omitempty"`
	Name           string         `json:"name"`
	Address        string         `json:"address,omitempty"`
	Port           uint16         `json:"port,omitempty"`
	Username       string         `json:"username,omitempty"`
	CredentialType string         `json:"credentialType,omitempty"`
	Credentials    Credentials    `json:"credentials,omitempty"`
	HostKey        []byte         `json:"hostKey,omitempty"`
	ChallengeToken string         `json:"challengeToken,omitempty"`
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
	if r.store == nil {
		if id == LocalID {
			return Host{
				ID:                 LocalID,
				Revision:           LocalRevision,
				ConnectionRevision: LocalRevision,
				Name:               defaultLocalHostName,
				ConnectionType:     ConnectionTypeLocal,
				Trusted:            false,
			}, nil
		}
		return Host{}, ErrNotFound
	}
	record, err := r.store.Get(ctx, id)
	if err != nil {
		return Host{}, err
	}
	return project(record), nil
}

// Delete removes a registered host and its credentials.
func (r *Registry) Delete(ctx context.Context, id ID, expected Revision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("host registry is closed")
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
	if r.store == nil {
		return Record{}, Credentials{}, ErrNotFound
	}
	record, err := r.store.Get(ctx, id)
	if err != nil {
		return Record{}, Credentials{}, err
	}
	if record.ConnectionType == ConnectionTypeLocal {
		return Record{}, Credentials{}, ErrUnsupported
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

func hasIrrelevantLocalFields(input HostInput) bool {
	if input.Address != "" || input.Port != 0 || input.Username != "" || input.CredentialType != "" {
		return true
	}
	if input.Credentials.Password != "" || input.Credentials.PrivateKey != "" || input.Credentials.Passphrase != "" {
		return true
	}
	return len(input.HostKey) > 0 || input.ChallengeToken != ""
}

func hasSecretFields(creds Credentials) bool {
	return creds.Password != "" || creds.PrivateKey != "" || creds.Passphrase != ""
}

func (r *Registry) sealInputCredential(id ID, credType string, creds Credentials) ([]byte, error) {
	if credType == "" {
		return nil, nil
	}
	if credType != CredentialTypePassword && credType != CredentialTypePrivateKey {
		return nil, errors.New("invalid credential type")
	}
	if (credType == CredentialTypePassword && creds.Password == "") ||
		(credType == CredentialTypePrivateKey && creds.PrivateKey == "") {
		return nil, errors.New("credentials required for configured credential type")
	}
	return r.vault.Seal(id, credType, creds)
}

func (r *Registry) saveNew(ctx context.Context, input HostInput, trimmedName string) (Host, error) {
	connType := input.ConnectionType
	if connType == "" {
		if input.Address != "" || input.CredentialType != "" {
			connType = ConnectionTypeSSH
		} else {
			connType = ConnectionTypeLocal
		}
	}
	if connType != ConnectionTypeLocal && connType != ConnectionTypeSSH {
		return Host{}, errors.New("invalid connection type")
	}

	rev, connRev, err := r.store.NextRevisions(ctx, input.ID)
	if err != nil {
		return Host{}, err
	}

	if connType == ConnectionTypeLocal {
		if hasIrrelevantLocalFields(input) {
			return Host{}, errors.New("irrelevant fields for local host")
		}
		newRecord := Record{
			ID:                 input.ID,
			Revision:           rev,
			ConnectionRevision: connRev,
			Name:               trimmedName,
			ConnectionType:     ConnectionTypeLocal,
		}
		if err := r.store.Put(ctx, newRecord, 0); err != nil {
			return Host{}, err
		}
		return project(newRecord), nil
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
	credBytes, err := r.sealInputCredential(input.ID, input.CredentialType, input.Credentials)
	if err != nil {
		return Host{}, err
	}
	newRecord := Record{
		ID:                 input.ID,
		Revision:           rev,
		ConnectionRevision: connRev,
		Name:               trimmedName,
		ConnectionType:     ConnectionTypeSSH,
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

func (r *Registry) saveUpdate(
	ctx context.Context, input HostInput, trimmedName string, expected Revision,
) (Host, error) {
	old, err := r.store.Get(ctx, input.ID)
	if err != nil {
		return Host{}, err
	}
	if old.Revision != expected {
		return Host{}, ErrStaleRevision
	}
	oldConnType := old.ConnectionType
	if oldConnType == "" {
		if old.ID == LocalID {
			oldConnType = ConnectionTypeLocal
		} else {
			oldConnType = ConnectionTypeSSH
		}
	}
	if input.ConnectionType != "" && input.ConnectionType != oldConnType {
		return Host{}, errors.New("cannot change host connection type")
	}

	if oldConnType == ConnectionTypeLocal {
		if hasIrrelevantLocalFields(input) {
			return Host{}, errors.New("irrelevant fields for local host")
		}
		newRecord := Record{
			ID:                 input.ID,
			Revision:           old.Revision + 1,
			ConnectionRevision: old.ConnectionRevision,
			Name:               trimmedName,
			ConnectionType:     ConnectionTypeLocal,
		}
		if err := r.store.Put(ctx, newRecord, expected); err != nil {
			return Host{}, err
		}
		return project(newRecord), nil
	}

	return r.saveUpdateSSH(ctx, input, trimmedName, old, expected)
}

func (r *Registry) resolveCredentialUpdate(
	input HostInput, old Record,
) (credType string, credBytes []byte, credChanged bool, err error) {
	credType = old.CredentialType
	credBytes = old.Credential

	if hasSecretFields(input.Credentials) {
		if input.CredentialType != "" {
			credType = input.CredentialType
		}
		sealed, sealErr := r.sealInputCredential(input.ID, credType, input.Credentials)
		if sealErr != nil {
			return "", nil, false, sealErr
		}
		return credType, sealed, true, nil
	}
	if input.CredentialType != "" && input.CredentialType != old.CredentialType {
		return "", nil, false, errors.New("cannot change credential type without supplying replacement credentials")
	}
	return credType, credBytes, false, nil
}

func resolveHostKeyUpdate(inputKey, oldKey []byte, endpointChanged bool) ([]byte, bool) {
	if len(inputKey) > 0 {
		return inputKey, !bytes.Equal(inputKey, oldKey)
	}
	if endpointChanged {
		return nil, len(oldKey) > 0
	}
	return oldKey, false
}

func (r *Registry) saveUpdateSSH(
	ctx context.Context, input HostInput, trimmedName string, old Record, expected Revision,
) (Host, error) {
	trimmedAddr := strings.TrimSpace(input.Address)
	if trimmedAddr == "" {
		trimmedAddr = old.Address
	}
	trimmedUser := strings.TrimSpace(input.Username)
	if trimmedUser == "" {
		trimmedUser = old.Username
	}
	port := input.Port
	if port == 0 {
		port = old.Port
	}
	if port == 0 {
		port = 22
	}

	credType, credBytes, credChanged, err := r.resolveCredentialUpdate(input, old)
	if err != nil {
		return Host{}, err
	}

	endpointChanged := trimmedAddr != old.Address || port != old.Port
	hostKey, trustChanged := resolveHostKeyUpdate(input.HostKey, old.HostKey, endpointChanged)

	newRevision := old.Revision + 1
	newConnRevision := old.ConnectionRevision
	if endpointChanged || trimmedUser != old.Username || credChanged || trustChanged {
		newConnRevision = newRevision
	}

	newRecord := Record{
		ID:                 input.ID,
		Revision:           newRevision,
		ConnectionRevision: newConnRevision,
		Name:               trimmedName,
		ConnectionType:     ConnectionTypeSSH,
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
	if !validHostID(input.ID) {
		return Host{}, errors.New("invalid host identifier")
	}
	trimmedName := strings.TrimSpace(input.Name)
	if trimmedName == "" {
		return Host{}, errors.New("host name cannot be empty")
	}

	if expected == 0 {
		return r.saveNew(ctx, input, trimmedName)
	}
	return r.saveUpdate(ctx, input, trimmedName, expected)
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

func (r *Registry) GetSettings(ctx context.Context) (Settings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Settings{}, errors.New("host registry is closed")
	}
	if err := r.initialize(ctx); err != nil {
		return Settings{}, err
	}
	return r.store.GetSettings(ctx)
}

func (r *Registry) UpdateDefaultHost(ctx context.Context, defaultHostID *ID, expected Revision) (Settings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Settings{}, errors.New("host registry is closed")
	}
	if err := r.initialize(ctx); err != nil {
		return Settings{}, err
	}
	return r.store.UpdateDefaultHost(ctx, defaultHostID, expected)
}
