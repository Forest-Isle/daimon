package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

// Migrator handles migration from SQLite to file-based storage.
type Migrator struct {
	db           *store.DB
	fileStore    *FileStore
	embeddingsDB *EmbeddingsDBImpl
	embedder     EmbeddingProvider
}

// NewMigrator creates a new Migrator.
func NewMigrator(db *store.DB, fileStore *FileStore, embeddingsDB *EmbeddingsDBImpl, embedder EmbeddingProvider) *Migrator {
	return &Migrator{
		db:           db,
		fileStore:    fileStore,
		embeddingsDB: embeddingsDB,
		embedder:     embedder,
	}
}

// MigrationStats holds statistics about the migration.
type MigrationStats struct {
	TotalFacts       int
	SessionFacts     int
	UserFacts        int
	GlobalFacts      int
	FilesCreated     int
	ChunksCreated    int
	EmbeddingsCopied int
	Errors           []string
	Duration         time.Duration
}

// Migrate performs the full migration from SQLite to file-based storage.
func (m *Migrator) Migrate(ctx context.Context) (*MigrationStats, error) {
	startTime := time.Now()
	stats := &MigrationStats{}

	slog.Info("migrator: starting migration from SQLite to file-based storage")

	// Step 1: Read all facts from memory_facts table
	facts, err := m.readAllFacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read facts: %w", err)
	}

	stats.TotalFacts = len(facts)
	slog.Info("migrator: read facts from SQLite", "count", len(facts))

	// Step 2: Group facts by scope
	grouped := m.groupFactsByScope(facts)
	slog.Info("migrator: grouped facts", "sessions", len(grouped.sessions), "users", len(grouped.users), "global", len(grouped.global))

	// Step 3: Write session facts
	for sessionID, sessionFacts := range grouped.sessions {
		if err := m.writeSessionFacts(ctx, sessionID, sessionFacts); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("session %s: %v", sessionID, err))
			slog.Error("migrator: failed to write session facts", "session_id", sessionID, "err", err)
		} else {
			stats.SessionFacts += len(sessionFacts)
			stats.FilesCreated++
		}
	}

	// Step 4: Write user facts
	for userID, userFacts := range grouped.users {
		if err := m.writeUserFacts(ctx, userID, userFacts); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("user %s: %v", userID, err))
			slog.Error("migrator: failed to write user facts", "user_id", userID, "err", err)
		} else {
			stats.UserFacts += len(userFacts)
			stats.FilesCreated++
		}
	}

	// Step 5: Write global facts
	if len(grouped.global) > 0 {
		if err := m.writeGlobalFacts(ctx, grouped.global); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("global: %v", err))
			slog.Error("migrator: failed to write global facts", "err", err)
		} else {
			stats.GlobalFacts = len(grouped.global)
			stats.FilesCreated++
		}
	}

	// Step 6: Copy embeddings to embeddings.db
	embeddingsCopied, err := m.copyEmbeddings(ctx, facts)
	if err != nil {
		stats.Errors = append(stats.Errors, fmt.Sprintf("embeddings: %v", err))
		slog.Error("migrator: failed to copy embeddings", "err", err)
	} else {
		stats.EmbeddingsCopied = embeddingsCopied
	}

	stats.Duration = time.Since(startTime)
	slog.Info("migrator: migration completed", "duration", stats.Duration, "files", stats.FilesCreated, "errors", len(stats.Errors))

	return stats, nil
}

// groupedFacts holds facts grouped by scope.
type groupedFacts struct {
	sessions map[string][]Entry
	users    map[string][]Entry
	global   []Entry
}

// readAllFacts reads all facts from the memory_facts table.
func (m *Migrator) readAllFacts(ctx context.Context) ([]Entry, error) {
	query := `
		SELECT id, session_id, user_id, scope, content, embedding, version, expires_at, metadata, created_at, updated_at
		FROM memory_facts
		ORDER BY created_at ASC
	`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var facts []Entry
	for rows.Next() {
		var e Entry
		var embBytes []byte
		var metadata string
		var sessionID, userID, scope sql.NullString
		var expiresAt sql.NullTime

		err := rows.Scan(
			&e.ID, &sessionID, &userID, &scope,
			&e.Content, &embBytes, &e.Version, &expiresAt,
			&metadata, &e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			slog.Warn("migrator: failed to scan row", "err", err)
			continue
		}

		e.SessionID = sessionID.String
		e.UserID = userID.String
		if scope.Valid {
			e.Scope = MemoryScope(scope.String)
		} else {
			e.Scope = ScopeGlobal
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			e.ExpiresAt = &t
		}

		// Parse embedding
		if len(embBytes) > 0 {
			var embedding []float32
			if err := json.Unmarshal(embBytes, &embedding); err == nil {
				e.Embedding = embedding
			}
		}

		// Parse metadata
		if metadata != "" {
			json.Unmarshal([]byte(metadata), &e.Metadata)
		}
		if e.Metadata == nil {
			e.Metadata = make(map[string]string)
		}

		facts = append(facts, e)
	}

	return facts, rows.Err()
}

// groupFactsByScope groups facts by scope/user_id/session_id.
func (m *Migrator) groupFactsByScope(facts []Entry) *groupedFacts {
	grouped := &groupedFacts{
		sessions: make(map[string][]Entry),
		users:    make(map[string][]Entry),
		global:   []Entry{},
	}

	for _, fact := range facts {
		switch fact.Scope {
		case ScopeSession:
			if fact.SessionID != "" {
				grouped.sessions[fact.SessionID] = append(grouped.sessions[fact.SessionID], fact)
			}
		case ScopeUser:
			if fact.UserID != "" {
				grouped.users[fact.UserID] = append(grouped.users[fact.UserID], fact)
			}
		case ScopeGlobal:
			grouped.global = append(grouped.global, fact)
		}
	}

	return grouped
}

// writeSessionFacts writes session facts to markdown files.
func (m *Migrator) writeSessionFacts(ctx context.Context, sessionID string, facts []Entry) error {
	for _, fact := range facts {
		fact.Scope = ScopeSession
		fact.SessionID = sessionID
		if err := m.fileStore.SaveFact(ctx, fact); err != nil {
			return fmt.Errorf("failed to save fact %s: %w", fact.ID, err)
		}
	}
	return nil
}

// writeUserFacts writes user facts to markdown files.
func (m *Migrator) writeUserFacts(ctx context.Context, userID string, facts []Entry) error {
	for _, fact := range facts {
		fact.Scope = ScopeUser
		fact.UserID = userID
		if err := m.fileStore.SaveFact(ctx, fact); err != nil {
			return fmt.Errorf("failed to save fact %s: %w", fact.ID, err)
		}
	}
	return nil
}

// writeGlobalFacts writes global facts to markdown files.
func (m *Migrator) writeGlobalFacts(ctx context.Context, facts []Entry) error {
	for _, fact := range facts {
		fact.Scope = ScopeGlobal
		if err := m.fileStore.SaveFact(ctx, fact); err != nil {
			return fmt.Errorf("failed to save fact %s: %w", fact.ID, err)
		}
	}
	return nil
}

// copyEmbeddings copies embeddings from memory_facts to fact_embeddings.
func (m *Migrator) copyEmbeddings(ctx context.Context, facts []Entry) (int, error) {
	copied := 0

	for _, fact := range facts {
		if len(fact.Embedding) == 0 {
			continue
		}

		// Determine file path
		filePath, err := m.getFactFilePath(fact)
		if err != nil {
			slog.Warn("migrator: failed to get file path", "fact_id", fact.ID, "err", err)
			continue
		}

		// Compute content hash
		contentHash := ComputeHash(fact.Content)

		// Save to embeddings.db
		err = m.embeddingsDB.SaveEmbedding(
			ctx,
			fact.ID,
			filePath,
			string(fact.Scope),
			fact.UserID,
			fact.SessionID,
			contentHash,
			fact.Embedding,
			fact.Version,
			fact.ExpiresAt,
		)
		if err != nil {
			slog.Warn("migrator: failed to save embedding", "fact_id", fact.ID, "err", err)
			continue
		}

		// Update FTS5 content
		if err := m.embeddingsDB.UpdateFTS5Content(ctx, fact.ID, fact.Content); err != nil {
			slog.Warn("migrator: failed to update FTS5 content", "fact_id", fact.ID, "err", err)
		}

		copied++
	}

	return copied, nil
}

// getFactFilePath returns the file path for a fact.
func (m *Migrator) getFactFilePath(fact Entry) (string, error) {
	storageDir := m.fileStore.storageDir

	switch fact.Scope {
	case ScopeSession:
		if fact.SessionID == "" {
			return "", fmt.Errorf("session_id required")
		}
		return filepath.Join(storageDir, "session", fact.SessionID, "facts.md"), nil
	case ScopeUser:
		if fact.UserID == "" {
			return "", fmt.Errorf("user_id required")
		}
		return filepath.Join(storageDir, "user", fact.UserID, "facts.md"), nil
	case ScopeGlobal:
		return filepath.Join(storageDir, "global", "facts.md"), nil
	default:
		return "", fmt.Errorf("unknown scope: %s", fact.Scope)
	}
}

// Verify verifies the integrity of the migration.
func (m *Migrator) Verify(ctx context.Context) error {
	slog.Info("migrator: verifying migration integrity")

	// Read all facts from SQLite
	sqliteFacts, err := m.readAllFacts(ctx)
	if err != nil {
		return fmt.Errorf("failed to read SQLite facts: %w", err)
	}

	// Build fact ID map
	sqliteFactMap := make(map[string]Entry)
	for _, fact := range sqliteFacts {
		sqliteFactMap[fact.ID] = fact
	}

	// Read all facts from file storage
	var fileFacts []Entry
	for _, scope := range []MemoryScope{ScopeSession, ScopeUser, ScopeGlobal} {
		facts, err := m.fileStore.ListByScope(ctx, scope, "")
		if err != nil {
			return fmt.Errorf("failed to list file facts for scope %s: %w", scope, err)
		}
		fileFacts = append(fileFacts, facts...)
	}

	// Compare counts
	if len(sqliteFacts) != len(fileFacts) {
		return fmt.Errorf("fact count mismatch: SQLite=%d, File=%d", len(sqliteFacts), len(fileFacts))
	}

	// Verify each fact
	missingCount := 0
	mismatchCount := 0

	for _, fileFact := range fileFacts {
		sqliteFact, ok := sqliteFactMap[fileFact.ID]
		if !ok {
			slog.Warn("migrator: fact in file storage but not in SQLite", "fact_id", fileFact.ID)
			missingCount++
			continue
		}

		// Compare content
		if sqliteFact.Content != fileFact.Content {
			slog.Warn("migrator: content mismatch", "fact_id", fileFact.ID)
			mismatchCount++
		}
	}

	if missingCount > 0 || mismatchCount > 0 {
		return fmt.Errorf("verification failed: missing=%d, mismatch=%d", missingCount, mismatchCount)
	}

	slog.Info("migrator: verification passed", "total_facts", len(fileFacts))
	return nil
}

// GetStats returns statistics about the current storage.
func (m *Migrator) GetStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// SQLite stats
	var sqliteCount int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_facts`).Scan(&sqliteCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count SQLite facts: %w", err)
	}
	stats["sqlite_facts"] = sqliteCount

	// File storage stats
	var fileCount int
	for _, scope := range []MemoryScope{ScopeSession, ScopeUser, ScopeGlobal} {
		facts, err := m.fileStore.ListByScope(ctx, scope, "")
		if err != nil {
			slog.Warn("migrator: failed to list facts", "scope", scope, "err", err)
			continue
		}
		fileCount += len(facts)
	}
	stats["file_facts"] = fileCount

	// Storage directory size
	var totalSize int64
	filepath.Walk(m.fileStore.storageDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	stats["storage_size_bytes"] = totalSize
	stats["storage_size_mb"] = float64(totalSize) / (1024 * 1024)

	// Index stats
	stats["storage_dir"] = m.fileStore.storageDir

	return stats, nil
}
