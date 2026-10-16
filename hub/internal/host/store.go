package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// Record is the durable host configuration. Credential contains only an
// authenticated encrypted envelope; it must never be projected into a DTO.
type Record struct {
	ID                 ID       `json:"id"`
	Revision           Revision `json:"revision"`
	ConnectionRevision Revision `json:"connectionRevision"`
	Name               string   `json:"name"`
	Address            string   `json:"address"`
	Port               uint16   `json:"port"`
	Username           string   `json:"username"`
	CredentialType     string   `json:"credentialType"`
	Credential         []byte   `json:"credential"`
	HostKey            []byte   `json:"hostKey"`
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

const schemaVersion = 1

// Store owns one private directory until Close. The OS releases its advisory
// lock on process exit; a leftover lock file is not a stale ownership claim.
type Store struct {
	db       *sql.DB
	lock     *os.File
	once     sync.Once
	closeErr error
}

func OpenStore(ctx context.Context, directory string) (_ *Store, err error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("host state directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, fmt.Errorf("host state directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("host state directory must be private")
	}
	lock, err := privateFile(filepath.Join(directory, "owner.lock"))
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, ErrStoreOwned
	}
	store := &Store{lock: lock}
	defer func() {
		if err != nil {
			_ = store.Close()
		}
	}()
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
	store.db = db
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, fmt.Errorf("read host state schema: %w", err)
	}
	if version > schemaVersion {
		return nil, fmt.Errorf("unsupported host state schema %d", version)
	}
	if version != 0 && version < schemaVersion {
		if err := backupState(directory, version); err != nil {
			return nil, fmt.Errorf("migration backup: %w", err)
		}
	}
	if version == 0 {
		// Never interpret an unrelated database as an empty new registry.
		var tables int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return nil, err
		}
		if tables != 0 {
			return nil, errors.New("unrecognized host state database")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `CREATE TABLE hosts (id TEXT PRIMARY KEY, revision INTEGER NOT NULL CHECK(revision > 0), record BLOB NOT NULL); PRAGMA user_version=1;`); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pending_cleanups (
		id TEXT PRIMARY KEY,
		host_id TEXT NOT NULL,
		staging_path TEXT NOT NULL,
		token TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);`); err != nil {
		return nil, err
	}

	return store, nil
}

func privateFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("host state file must be private and regular")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
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
	defer rows.Close()
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
	return record, nil
}

// Put atomically installs the next revision; expected zero creates a host.
func (s *Store) Put(ctx context.Context, record Record, expected Revision) error {
	if record.ID == "" || record.ID == LocalID || record.Revision != expected+1 || record.Revision == 0 || record.ConnectionRevision == 0 || record.ConnectionRevision > record.Revision {
		return errors.New("invalid host revision or identity")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(ctx, "INSERT INTO hosts(id,revision,record) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", record.ID, record.Revision, data)
	} else {
		result, err = tx.ExecContext(ctx, "UPDATE hosts SET revision=?,record=? WHERE id=? AND revision=?", record.Revision, data, record.ID, expected)
	}
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
	return tx.Commit()
}

func (s *Store) Delete(ctx context.Context, id ID, expected Revision) error {
	if id == LocalID {
		return ErrUnsupported
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM hosts WHERE id=? AND revision=?", id, expected)
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
	return nil
}

func backupState(directory string, version int) error {
	src := filepath.Join(directory, "hosts.db")
	dst := filepath.Join(directory, fmt.Sprintf("hosts.db.v%d.bak", version))
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

const MaxPendingCleanupsPerHost = 50

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
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM pending_cleanups WHERE host_id = ?", rec.HostID).Scan(&count); err != nil {
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

	if _, err := tx.ExecContext(ctx, `INSERT INTO pending_cleanups (id, host_id, staging_path, token, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET staging_path=excluded.staging_path, token=excluded.token, created_at=excluded.created_at`,
		rec.ID, rec.HostID, rec.StagingPath, rec.Token, rec.CreatedAt); err != nil {
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
		rows, err = s.db.QueryContext(ctx, "SELECT id, host_id, staging_path, token, created_at FROM pending_cleanups ORDER BY created_at ASC")
	} else {
		rows, err = s.db.QueryContext(ctx, "SELECT id, host_id, staging_path, token, created_at FROM pending_cleanups WHERE host_id = ? ORDER BY created_at ASC", hostID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
