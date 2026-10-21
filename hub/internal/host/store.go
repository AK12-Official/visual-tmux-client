package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// Record is the durable host configuration. Credential contains only an
// authenticated encrypted envelope; it must never be projected into a DTO.
type Record struct {
	ID                 ID             `json:"id"`
	Revision           Revision       `json:"revision"`
	ConnectionRevision Revision       `json:"connectionRevision"`
	Name               string         `json:"name"`
	ConnectionType     ConnectionType `json:"connectionType"`
	Address            string         `json:"address,omitempty"`
	Port               uint16         `json:"port,omitempty"`
	Username           string         `json:"username,omitempty"`
	CredentialType     string         `json:"credentialType,omitempty"`
	Credential         []byte         `json:"credential,omitempty"`
	HostKey            []byte         `json:"hostKey,omitempty"`
}

// PendingCleanupRecord tracks an owned staging file on a remote host pending cleanup.
type PendingCleanupRecord struct {
	ID          string `json:"id"`
	HostID      ID     `json:"hostId"`
	StagingPath string `json:"stagingPath"`
	Token       string `json:"token"`
	CreatedAt   int64  `json:"createdAt"`
}

var ErrStoreOwned = errors.New("host state is already owned by another Hub")

const (
	stateDirPerm              = 0700
	stateFilePerm             = 0600
	privatePermMask           = 0077
	schemaVersion             = 2
	schemaVersionV1           = 1
	initialRevision           = 1
	MaxPendingCleanupsPerHost = 50
)

const ddlV2Schema = `
	CREATE TABLE hosts (
		id TEXT PRIMARY KEY,
		revision INTEGER NOT NULL CHECK(revision > 0),
		connection_type TEXT NOT NULL DEFAULT 'ssh',
		record BLOB NOT NULL
	);
	CREATE UNIQUE INDEX idx_hosts_singleton_local ON hosts(connection_type) WHERE connection_type = 'local';
	CREATE TABLE settings (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		default_host_id TEXT,
		revision INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE revision_watermarks (
		id TEXT PRIMARY KEY,
		revision INTEGER NOT NULL,
		connection_revision INTEGER NOT NULL
	);
	CREATE TABLE pending_cleanups (
		id TEXT PRIMARY KEY,
		host_id TEXT NOT NULL,
		staging_path TEXT NOT NULL,
		token TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
`

// Store owns one private directory until Close. The OS releases its advisory
// lock on process exit; a leftover lock file is not a stale ownership claim.
type Store struct {
	db       *sql.DB
	lock     *os.File
	once     sync.Once
	closeErr error
}

type StoreOption func(*storeConfig)

type storeConfig struct {
	validator func(Record) error
}

func WithCredentialValidator(v func(Record) error) StoreOption {
	return func(c *storeConfig) {
		c.validator = v
	}
}

func verifyStateDirectory(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, stateDirPerm); err != nil {
		return nil, fmt.Errorf("host state directory: %w", err)
	}
	if err := os.Chmod(directory, stateDirPerm); err != nil {
		return nil, fmt.Errorf("host state directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&privatePermMask != 0 {
		return nil, errors.New("host state directory must be private")
	}
	lock, err := privateFile(filepath.Join(directory, "owner.lock"))
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close() //nolint:errcheck // lock file release
		return nil, ErrStoreOwned
	}
	return lock, nil
}

func openDatabaseFile(directory string) (*sql.DB, error) {
	path := filepath.Join(directory, "hosts.db")
	file, err := privateFile(path)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func initializeFreshDatabase(ctx context.Context, db *sql.DB) error {
	var tables int
	q := "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'"
	if err := db.QueryRowContext(ctx, q).Scan(&tables); err != nil {
		return err
	}
	if tables != 0 {
		return errors.New("unrecognized host state database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on failure
	}()

	if _, err := tx.ExecContext(ctx, ddlV2Schema); err != nil {
		return err
	}

	localRec := Record{
		ID:                 LocalID,
		Revision:           initialRevision,
		ConnectionRevision: initialRevision,
		Name:               "Local",
		ConnectionType:     ConnectionTypeLocal,
	}
	localData, err := json.Marshal(localRec)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO hosts(id, revision, connection_type, record) VALUES (?, 1, 'local', ?)",
		LocalID, localData,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO settings(id, default_host_id, revision) VALUES (1, 'local', 1)",
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO metadata(key, value) VALUES ('initialized', '1')",
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO revision_watermarks(id, revision, connection_revision) VALUES ('local', 1, 1)",
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=2;"); err != nil {
		return err
	}
	return tx.Commit()
}

type hostRowV1 struct {
	id      ID
	rev     Revision
	connRev Revision
	data    []byte
}

func readV1Hosts(ctx context.Context, tx *sql.Tx) ([]hostRowV1, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, revision, record FROM hosts")
	if err != nil {
		return nil, fmt.Errorf("query hosts: %w", err)
	}
	defer func() {
		_ = rows.Close() //nolint:errcheck // query cleanup
	}()

	var rowsToUpdate []hostRowV1
	for rows.Next() {
		var id ID
		var rev Revision
		var rData []byte
		if err := rows.Scan(&id, &rev, &rData); err != nil {
			return nil, err
		}
		var rec Record
		if err := json.Unmarshal(rData, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal host %s: %w", id, err)
		}
		if rec.ConnectionType == "" {
			rec.ConnectionType = ConnectionTypeSSH
		}
		newData, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		rowsToUpdate = append(rowsToUpdate, hostRowV1{
			id:      id,
			rev:     rev,
			connRev: rec.ConnectionRevision,
			data:    newData,
		})
	}
	return rowsToUpdate, rows.Err()
}

func applyV1DDL(ctx context.Context, tx *sql.Tx) error {
	ddls := []string{
		"ALTER TABLE hosts ADD COLUMN connection_type TEXT NOT NULL DEFAULT 'ssh'",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_singleton_local " +
			"ON hosts(connection_type) WHERE connection_type = 'local'",
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			default_host_id TEXT,
			revision INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS revision_watermarks (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			connection_revision INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS pending_cleanups (
			id TEXT PRIMARY KEY,
			host_id TEXT NOT NULL,
			staging_path TEXT NOT NULL,
			token TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
	}
	for _, ddl := range ddls {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("execute ddl: %w", err)
		}
	}
	return nil
}

func bootstrapLocalHostV1(ctx context.Context, tx *sql.Tx) error {
	var hasLocal int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM hosts WHERE id = 'local'").Scan(&hasLocal); err != nil {
		return err
	}
	if hasLocal == 0 {
		localRec := Record{
			ID:                 LocalID,
			Revision:           initialRevision,
			ConnectionRevision: initialRevision,
			Name:               "Local",
			ConnectionType:     ConnectionTypeLocal,
		}
		localData, err := json.Marshal(localRec)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO hosts(id, revision, connection_type, record) VALUES (?, 1, 'local', ?)",
			LocalID, localData,
		); err != nil {
			return fmt.Errorf("insert bootstrap local: %w", err)
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT OR REPLACE INTO revision_watermarks(id, revision, connection_revision) VALUES ('local', 1, 1)",
		); err != nil {
			return err
		}
	}
	return nil
}

func migrateDatabaseV1ToV2(ctx context.Context, db *sql.DB, validator func(Record) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on migration failure
	}()

	if err := applyV1DDL(ctx, tx); err != nil {
		return err
	}

	rowsToUpdate, err := readV1Hosts(ctx, tx)
	if err != nil {
		return err
	}

	for _, r := range rowsToUpdate {
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE hosts SET record = ?, connection_type = 'ssh' WHERE id = ?",
			r.data, r.id,
		); err != nil {
			return fmt.Errorf("update host %s: %w", r.id, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT OR REPLACE INTO revision_watermarks(id, revision, connection_revision) VALUES (?, ?, ?)",
			r.id, r.rev, r.connRev,
		); err != nil {
			return fmt.Errorf("seed watermark %s: %w", r.id, err)
		}
	}

	if err := bootstrapLocalHostV1(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.ExecContext(
		ctx,
		"INSERT OR IGNORE INTO settings(id, default_host_id, revision) VALUES (1, 'local', 1)",
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT OR REPLACE INTO metadata(key, value) VALUES ('initialized', '1')",
	); err != nil {
		return err
	}

	if err := validateMigratedCredentials(rowsToUpdate, validator); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=2;"); err != nil {
		return err
	}
	return tx.Commit()
}

func validateMigratedCredentials(rows []hostRowV1, validator func(Record) error) error {
	if validator == nil {
		return nil
	}
	for _, r := range rows {
		var rec Record
		if err := json.Unmarshal(r.data, &rec); err == nil && len(rec.Credential) > 0 {
			if err := validator(rec); err != nil {
				return fmt.Errorf("credential decryption failed: %w", err)
			}
		}
	}
	return nil
}

func OpenStore(ctx context.Context, directory string, opts ...StoreOption) (_ *Store, err error) {
	var cfg storeConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	lock, err := verifyStateDirectory(directory)
	if err != nil {
		return nil, err
	}

	store := &Store{lock: lock}
	var createdBackup string
	defer func() {
		if err != nil {
			_ = store.Close() //nolint:errcheck // cleanup store on open error
			if createdBackup != "" {
				_ = os.Remove(createdBackup) //nolint:errcheck // remove migration backup on failure
			}
		}
	}()

	db, err := openDatabaseFile(directory)
	if err != nil {
		return nil, err
	}
	store.db = db

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, fmt.Errorf("read host state schema: %w", err)
	}
	if version > schemaVersion {
		return nil, fmt.Errorf("unsupported host state schema %d", version)
	}
	if version != 0 && version < schemaVersion {
		backupPath := filepath.Join(directory, fmt.Sprintf("hosts.db.v%d.bak", version))
		if err := backupState(directory, version); err != nil {
			return nil, fmt.Errorf("migration backup: %w", err)
		}
		createdBackup = backupPath
	}
	if version == 0 {
		if err := initializeFreshDatabase(ctx, db); err != nil {
			return nil, err
		}
		return store, nil
	}

	if version == schemaVersionV1 {
		if err := migrateDatabaseV1ToV2(ctx, db, cfg.validator); err != nil {
			return nil, err
		}
	}

	return store, nil
}

func privateFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&privatePermMask != 0) {
		return nil, errors.New("host state file must be private and regular")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, stateFilePerm)
}

func (s *Store) Close() error {
	s.once.Do(func() {
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}

func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT record FROM hosts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close() //nolint:errcheck // rows cleanup
	}()
	out := []Record{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, errors.New("corrupt host record")
		}
		if record.ConnectionType == "" {
			if record.ID == LocalID {
				record.ConnectionType = ConnectionTypeLocal
			} else {
				record.ConnectionType = ConnectionTypeSSH
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id ID) (Record, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT record FROM hosts WHERE id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, errors.New("corrupt host record")
	}
	if record.ConnectionType == "" {
		if record.ID == LocalID {
			record.ConnectionType = ConnectionTypeLocal
		} else {
			record.ConnectionType = ConnectionTypeSSH
		}
	}
	return record, nil
}

func validatePutRecord(record Record, expected Revision) error {
	if record.ID == "" || record.Revision == 0 ||
		record.ConnectionRevision == 0 ||
		record.ConnectionRevision > record.Revision {
		return errors.New("invalid host revision or identity")
	}
	if expected > 0 && record.Revision != expected+1 {
		return errors.New("invalid host revision or identity")
	}
	if record.ConnectionType != ConnectionTypeLocal && record.ConnectionType != ConnectionTypeSSH {
		return errors.New("invalid connection type")
	}
	return nil
}

// Put atomically installs the next revision; expected zero creates a host.
func (s *Store) Put(ctx context.Context, record Record, expected Revision) error {
	if record.ConnectionType == "" {
		if record.ID == LocalID {
			record.ConnectionType = ConnectionTypeLocal
		} else {
			record.ConnectionType = ConnectionTypeSSH
		}
	}
	if err := validatePutRecord(record, expected); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on put failure
	}()

	if err := s.upsertHostRecord(ctx, tx, record, expected, data); err != nil {
		return err
	}

	watermarkQuery := `INSERT INTO revision_watermarks(id, revision, connection_revision) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			revision = MAX(revision, excluded.revision),
			connection_revision = MAX(connection_revision, excluded.connection_revision)`
	if _, err := tx.ExecContext(
		ctx, watermarkQuery,
		record.ID, record.Revision, record.ConnectionRevision,
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) upsertHostRecord(
	ctx context.Context,
	tx *sql.Tx,
	record Record,
	expected Revision,
	data []byte,
) error {
	if record.ConnectionType == ConnectionTypeLocal && expected == 0 {
		var count int
		if err := tx.QueryRowContext(
			ctx,
			"SELECT count(*) FROM hosts WHERE connection_type = 'local'",
		).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrDuplicateLocal
		}
	}

	var result sql.Result
	var err error
	if expected == 0 {
		result, err = tx.ExecContext(
			ctx,
			"INSERT INTO hosts(id,revision,connection_type,record) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING",
			record.ID, record.Revision, record.ConnectionType, data,
		)
	} else {
		result, err = tx.ExecContext(
			ctx,
			"UPDATE hosts SET revision=?,connection_type=?,record=? WHERE id=? AND revision=?",
			record.Revision, record.ConnectionType, data, record.ID, expected,
		)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errors.New("local host already registered")
		}
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id ID, expected Revision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on delete failure
	}()

	var data []byte
	err = tx.QueryRowContext(ctx, "SELECT record FROM hosts WHERE id=? AND revision=?", id, expected).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleRevision
	}
	if err != nil {
		return err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err == nil {
		watermarkQuery := `INSERT INTO revision_watermarks(id, revision, connection_revision) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				revision = MAX(revision, excluded.revision),
				connection_revision = MAX(connection_revision, excluded.connection_revision)`
		if _, err := tx.ExecContext(
			ctx, watermarkQuery,
			record.ID, record.Revision, record.ConnectionRevision,
		); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(ctx, "DELETE FROM hosts WHERE id=? AND revision=?", id, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}

	// Clearing default host atomically if removed host was default
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE settings SET default_host_id = NULL, revision = revision + 1 WHERE default_host_id = ?",
		id,
	); err != nil {
		return err
	}

	return tx.Commit()
}

type Settings struct {
	DefaultHostID *ID      `json:"defaultHostId"`
	Revision      Revision `json:"revision"`
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var defaultHost sql.NullString
	var rev Revision
	err := s.db.QueryRowContext(
		ctx,
		"SELECT default_host_id, revision FROM settings WHERE id = 1",
	).Scan(&defaultHost, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{Revision: 1}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var idPtr *ID
	if defaultHost.Valid && defaultHost.String != "" {
		idVal := ID(defaultHost.String)
		idPtr = &idVal
	}
	return Settings{
		DefaultHostID: idPtr,
		Revision:      rev,
	}, nil
}

func (s *Store) UpdateDefaultHost(ctx context.Context, defaultHostID *ID, expected Revision) (Settings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on update failure
	}()

	var currentRev Revision
	err = tx.QueryRowContext(ctx, "SELECT revision FROM settings WHERE id = 1").Scan(&currentRev)
	if errors.Is(err, sql.ErrNoRows) {
		currentRev = 1
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO settings (id, default_host_id, revision) VALUES (1, NULL, 1)",
		); err != nil {
			return Settings{}, err
		}
	} else if err != nil {
		return Settings{}, err
	}

	if expected <= 0 || currentRev != expected {
		return Settings{}, ErrStaleRevision
	}

	var targetStr sql.NullString
	if defaultHostID != nil && *defaultHostID != "" {
		var exists int
		if err := tx.QueryRowContext(
			ctx,
			"SELECT count(*) FROM hosts WHERE id = ?",
			*defaultHostID,
		).Scan(&exists); err != nil {
			return Settings{}, err
		}
		if exists == 0 {
			return Settings{}, ErrNotFound
		}
		targetStr = sql.NullString{String: string(*defaultHostID), Valid: true}
	}

	newRev := currentRev + 1
	res, err := tx.ExecContext(
		ctx,
		"UPDATE settings SET default_host_id = ?, revision = ? WHERE id = 1 AND revision = ?",
		targetStr, newRev, currentRev,
	)
	if err != nil {
		return Settings{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Settings{}, err
	}
	if n != 1 {
		return Settings{}, ErrStaleRevision
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, err
	}
	return Settings{
		DefaultHostID: defaultHostID,
		Revision:      newRev,
	}, nil
}

func (s *Store) NextRevisions(ctx context.Context, id ID) (Revision, Revision, error) {
	var maxRev, maxConnRev sql.NullInt64
	q := "SELECT revision, connection_revision FROM revision_watermarks WHERE id = ?"
	err := s.db.QueryRowContext(ctx, q, id).Scan(&maxRev, &maxConnRev)
	if errors.Is(err, sql.ErrNoRows) || (!maxRev.Valid || maxRev.Int64 == 0) {
		return 1, 1, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return Revision(maxRev.Int64 + 1), Revision(maxConnRev.Int64 + 1), nil
}

func backupState(directory string, version int) error {
	src := filepath.Join(directory, "hosts.db")
	dst := filepath.Join(directory, fmt.Sprintf("hosts.db.v%d.bak", version))
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, stateFilePerm)
}

func (s *Store) AddPendingCleanup(ctx context.Context, rec PendingCleanupRecord) error {
	if rec.ID == "" || rec.HostID == "" || rec.StagingPath == "" || rec.Token == "" {
		return errors.New("invalid pending cleanup record")
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck // rollback on add cleanup failure
	}()

	var count int
	if err := tx.QueryRowContext(
		ctx,
		"SELECT count(*) FROM pending_cleanups WHERE host_id = ?",
		rec.HostID,
	).Scan(&count); err != nil {
		return err
	}
	if count >= MaxPendingCleanupsPerHost {
		// Evict oldest entries exceeding the bound
		toEvict := count - MaxPendingCleanupsPerHost + 1
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_cleanups WHERE id IN (
			SELECT id FROM pending_cleanups WHERE host_id = ? ORDER BY created_at ASC LIMIT ?
		)`, rec.HostID, toEvict); err != nil {
			return err
		}
	}

	cleanupUpsert := `INSERT INTO pending_cleanups (id, host_id, staging_path, token, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			staging_path=excluded.staging_path,
			token=excluded.token,
			created_at=excluded.created_at`
	if _, err := tx.ExecContext(
		ctx, cleanupUpsert,
		rec.ID, rec.HostID, rec.StagingPath, rec.Token, rec.CreatedAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RemovePendingCleanup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM pending_cleanups WHERE id = ?", id)
	return err
}

func (s *Store) ListPendingCleanups(ctx context.Context, hostID ID) ([]PendingCleanupRecord, error) {
	var rows *sql.Rows
	var err error
	if hostID == "" {
		rows, err = s.db.QueryContext(
			ctx,
			"SELECT id, host_id, staging_path, token, created_at FROM pending_cleanups ORDER BY created_at ASC",
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			"SELECT id, host_id, staging_path, token, created_at FROM pending_cleanups "+
				"WHERE host_id = ? ORDER BY created_at ASC",
			hostID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close() //nolint:errcheck // rows cleanup
	}()

	var out []PendingCleanupRecord
	for rows.Next() {
		var rec PendingCleanupRecord
		if err := rows.Scan(&rec.ID, &rec.HostID, &rec.StagingPath, &rec.Token, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
