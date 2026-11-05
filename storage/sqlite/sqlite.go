// Package sqlite implements core.Storage in a single SQLite database file.
//
// The database holds one table, kv, keyed by namespace and key. It runs in WAL
// mode with an exclusive lock, so only one process can use the file at a time.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"

	"github.com/luks3r/lukky/core"
)

const sqliteBusy = 5 // SQLITE_BUSY primary result code

// migrations[i] moves the schema from version i to i+1. Append only.
var migrations = []string{
	`CREATE TABLE kv (
		namespace TEXT NOT NULL,
		key       TEXT NOT NULL,
		value     BLOB NOT NULL,
		PRIMARY KEY (namespace, key)
	) WITHOUT ROWID`,
}

type Storage struct{ db *sql.DB }

// Open opens or creates the database at path and applies pending migrations.
// It fails after one second if another process holds the database.
func Open(ctx context.Context, path string) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Create the file with owner-only permissions before SQLite opens it.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: the path comes from application code
	if err != nil {
		return nil, err
	}
	_ = f.Close()

	// locking_mode must come before journal_mode, so WAL runs without shared memory.
	pragmas := url.Values{"_pragma": {
		"busy_timeout(1000)",
		"locking_mode(EXCLUSIVE)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
	}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	// The exclusive lock belongs to one connection, so the pool must not open more.
	db.SetMaxOpenConns(1)
	s := &Storage{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		if isBusy(err) {
			return nil, fmt.Errorf("open %s: database is in use by another process", path)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

func isBusy(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code()&0xff == sqliteBusy
}

func (s *Storage) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this build supports (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	// PRAGMA does not accept bound parameters; the value is an integer we control.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Storage) Close() error { return s.db.Close() }

func (s *Storage) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM kv WHERE namespace = ? AND key = ?", namespace, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	}
	return value, err
}

func (s *Storage) Put(ctx context.Context, namespace, key string, value []byte) error {
	if namespace == "" || key == "" {
		return errors.New("namespace and key are required")
	}
	if value == nil {
		value = []byte{}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)
		 ON CONFLICT (namespace, key) DO UPDATE SET value = excluded.value`,
		namespace, key, value)
	return err
}

func (s *Storage) Delete(ctx context.Context, namespace, key string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM kv WHERE namespace = ? AND key = ?", namespace, key)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *Storage) List(ctx context.Context, namespace string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM kv WHERE namespace = ?", namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

var _ core.Storage = (*Storage)(nil)
