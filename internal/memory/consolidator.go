package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Consolidator promotes session-scoped facts to user scope after a session ends.
// It runs periodically in the background.
type Consolidator struct {
	store    Store
	db       *sql.DB
	baseDir  string
	interval time.Duration
	done     chan struct{}
}

// NewConsolidator creates a Consolidator with the given interval.
func NewConsolidator(store Store, db *sql.DB, baseDir string, interval time.Duration) *Consolidator {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &Consolidator{
		store:    store,
		db:       db,
		baseDir:  baseDir,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start begins the background consolidation loop.
func (c *Consolidator) Start(ctx context.Context) {
	go c.loop(ctx)
}

// Stop signals the consolidation loop to exit.
func (c *Consolidator) Stop() {
	close(c.done)
}

func (c *Consolidator) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.consolidate(ctx); err != nil {
				slog.Warn("consolidator: consolidation failed", "err", err)
			}
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// consolidate promotes high-value session facts to user scope.
func (c *Consolidator) consolidate(ctx context.Context) error {
	slog.Info("consolidator: running session->user consolidation")

	if c.baseDir != "" {
		return c.consolidateFiles(ctx)
	}

	// Fallback to database consolidation
	facts, err := c.store.ListByScope(ctx, ScopeSession, "")
	if err != nil {
		return fmt.Errorf("list session facts: %w", err)
	}

	promoted := 0
	for _, fact := range facts {
		if time.Since(fact.CreatedAt) < c.interval {
			continue
		}
		if fact.UserID == "" {
			continue
		}

		promotedFact := fact
		promotedFact.ID = fmt.Sprintf("fact_promoted_%d", time.Now().UnixNano())
		promotedFact.Scope = ScopeUser
		promotedFact.Version = 1
		now := time.Now()
		promotedFact.CreatedAt = now
		promotedFact.UpdatedAt = now
		if promotedFact.Metadata == nil {
			promotedFact.Metadata = make(map[string]string)
		}
		promotedFact.Metadata["promoted_from"] = fact.ID
		promotedFact.Metadata["promoted_at"] = now.Format(time.RFC3339)

		if err := c.store.SaveFact(ctx, promotedFact); err != nil {
			slog.Warn("consolidator: failed to promote fact", "id", fact.ID, "err", err)
			continue
		}
		promoted++
	}

	slog.Info("consolidator: done", "promoted", promoted, "total_session_facts", len(facts))
	return nil
}

// consolidateFiles promotes session files to user scope by moving files.
func (c *Consolidator) consolidateFiles(ctx context.Context) error {
	sessionDir := filepath.Join(c.baseDir, "session")
	userDir := filepath.Join(c.baseDir, "user")

	files, err := filepath.Glob(filepath.Join(sessionDir, "*.md"))
	if err != nil {
		return err
	}

	promoted := 0
	for _, filePath := range files {
		info, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		// Check if file is older than 24h
		if time.Since(info.ModTime()) < c.interval {
			continue
		}

		// Parse file to check strength and user_id
		mf, err := c.parseFile(filePath)
		if err != nil {
			continue
		}

		if mf.UserID == "" {
			continue
		}

		// Check strength using forgetting curve
		if mf.Strength < 0.5 {
			continue
		}

		// Move file from session/ to user/
		newPath := filepath.Join(userDir, filepath.Base(filePath))
		if err := os.Rename(filePath, newPath); err != nil {
			slog.Warn("consolidator: failed to move file", "path", filePath, "err", err)
			continue
		}

		// Update frontmatter
		mf.Scope = "user"
		now := time.Now()
		mf.PromotedFrom = filePath
		mf.PromotedAt = &now

		if err := c.writeFile(newPath, mf); err != nil {
			slog.Warn("consolidator: failed to update frontmatter", "path", newPath, "err", err)
			continue
		}

		// Update memory_index
		_, err = c.db.ExecContext(ctx, `
			UPDATE memory_index
			SET file_path = ?, scope = 'user'
			WHERE memory_id = ?
		`, newPath, mf.ID)
		if err != nil {
			slog.Warn("consolidator: failed to update index", "id", mf.ID, "err", err)
		}

		promoted++
	}

	slog.Info("consolidator: done", "promoted", promoted, "total_files", len(files))
	return nil
}

func (c *Consolidator) parseFile(path string) (*MemoryFile, error) {
	// Reuse parseFile from file_store
	fs := &FileMemoryStore{baseDir: c.baseDir}
	return fs.parseFile(path)
}

func (c *Consolidator) writeFile(path string, mf *MemoryFile) error {
	// Reuse writeFileAtomic from file_store
	fs := &FileMemoryStore{baseDir: c.baseDir}
	return fs.writeFileAtomic(path, *mf)
}
}
