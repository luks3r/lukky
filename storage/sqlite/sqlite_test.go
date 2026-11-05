package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luks3r/lukky/core"
	"github.com/luks3r/lukky/storage/storagetest"
)

func openTemp(t *testing.T, path string) *Storage {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) core.Storage {
		s := openTemp(t, filepath.Join(t.TempDir(), "test.db"))
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestDataSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "lukky.sqlite")
	s := openTemp(t, path)
	if err := s.Put(context.Background(), "aliases", "discord", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v", info.Mode().Perm())
	}
	s = openTemp(t, path)
	defer s.Close()
	if got, err := s.Get(context.Background(), "aliases", "discord"); err != nil || string(got) != "hello" {
		t.Fatalf("after reopen: %q %v", got, err)
	}
}

func TestSecondOpenFailsWhileInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lukky.db")
	s := openTemp(t, path)
	defer s.Close()
	if _, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("expected in-use error, got %v", err)
	}
}

func TestReopenKeepsSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lukky.sqlite")
	s := openTemp(t, path)
	_ = s.Close()
	s = openTemp(t, path) // must not re-run migrations
	defer s.Close()
	var version int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil || version != len(migrations) {
		t.Fatalf("version %d, err %v", version, err)
	}
}
