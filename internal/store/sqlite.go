package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	// Create migration tracking table to ensure each migration runs only once.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		name TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create _migrations table: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		// Skip already-applied migrations.
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE name = ?`, e.Name()).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", e.Name(), err)
		}
		if count > 0 {
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			// For existing databases upgraded from the old migrate() that had no
			// tracking: ALTER TABLE ADD COLUMN will fail with "duplicate column"
			// if the column already exists. Treat this as "already applied".
			if isAlreadyAppliedError(err) {
				slog.Info("migration already applied (detected from error), recording", "file", e.Name())
			} else {
				return fmt.Errorf("exec migration %s: %w", e.Name(), err)
			}
		} else {
			slog.Info("migration applied", "file", e.Name())
		}
		if _, err := db.Exec(`INSERT INTO _migrations (name) VALUES (?)`, e.Name()); err != nil {
			return fmt.Errorf("record migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// isAlreadyAppliedError returns true for errors indicating a migration was
// previously applied (e.g. duplicate column from ALTER TABLE, table already
// exists without IF NOT EXISTS).
func isAlreadyAppliedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") ||
		strings.Contains(msg, "table already exists") ||
		strings.Contains(msg, "already exists")
}
