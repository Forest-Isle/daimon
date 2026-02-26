package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite single-writer

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	slog.Info("database opened", "path", path)
	return &DB{db}, nil
}

func migrate(db *sql.DB) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			return fmt.Errorf("exec migration %s: %w", e.Name(), err)
		}
		slog.Info("migration applied", "file", e.Name())
	}
	return nil
}
