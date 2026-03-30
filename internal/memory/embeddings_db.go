package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

// EmbeddingsDBImpl implements EmbeddingsDB interface for managing embeddings in SQLite.
type EmbeddingsDBImpl struct {
	db            *store.DB
	fts5Available bool
	cfg           MemoryConfig
}

// NewEmbeddingsDB creates a new EmbeddingsDBImpl.
func NewEmbeddingsDB(db *store.DB, cfg MemoryConfig) *EmbeddingsDBImpl {
	edb := &EmbeddingsDBImpl{
		db:  db,
		cfg: cfg,
	}

	// Detect FTS5 availability
	edb.fts5Available = edb.detectFTS5()
	if edb.fts5Available {
		slog.Info("embeddings_db: FTS5 available")
	} else {
		slog.Warn("embeddings_db: FTS5 not available, falling back to LIKE search")
	}

	return edb
}

// detectFTS5 probes whether FTS5 is available.
func (edb *EmbeddingsDBImpl) detectFTS5() bool {
	_, err := edb.db.Exec(`SELECT * FROM fact_embeddings_fts LIMIT 0`)
	return err == nil
}

// SaveEmbedding saves an embedding to the database.
func (edb *EmbeddingsDBImpl) SaveEmbedding(
	ctx context.Context,
	factID, filePath, scope, userID, sessionID, contentHash string,
	embedding []float32,
	version int,
	expiresAt *time.Time,
) error {
	// Serialize embedding to JSON
	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding: %w", err)
	}

	// Upsert into fact_embeddings
	query := `
		INSERT INTO fact_embeddings (
			fact_id, file_path, scope, user_id, session_id,
			content_hash, embedding, version, expires_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(fact_id) DO UPDATE SET
			file_path = excluded.file_path,
			scope = excluded.scope,
			user_id = excluded.user_id,
			session_id = excluded.session_id,
			content_hash = excluded.content_hash,
			embedding = excluded.embedding,
			version = excluded.version,
			expires_at = excluded.expires_at,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err = edb.db.ExecContext(ctx, query,
		factID, filePath, scope, userID, sessionID,
		contentHash, embeddingJSON, version, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save embedding: %w", err)
	}

	return nil
}

// DeleteEmbedding deletes an embedding from the database.
func (edb *EmbeddingsDBImpl) DeleteEmbedding(ctx context.Context, factID string) error {
	query := `DELETE FROM fact_embeddings WHERE fact_id = ?`
	_, err := edb.db.ExecContext(ctx, query, factID)
	if err != nil {
		return fmt.Errorf("failed to delete embedding: %w", err)
	}

	return nil
}

// VectorSearch performs brute-force cosine similarity search.
func (edb *EmbeddingsDBImpl) VectorSearch(ctx context.Context, queryEmbedding []float32, limit int, scopes []MemoryScope, userID, sessionID string) ([]VectorSearchResult, error) {
	return edb.bruteForceVectorSearch(ctx, queryEmbedding, limit, scopes, userID, sessionID)
}

// bruteForceVectorSearch performs brute-force cosine similarity search.
func (edb *EmbeddingsDBImpl) bruteForceVectorSearch(ctx context.Context, queryEmbedding []float32, limit int, scopes []MemoryScope, userID, sessionID string) ([]VectorSearchResult, error) {
	scopeFilter := buildScopeFilter(scopes, userID, sessionID)

	query := `
		SELECT fact_id, file_path, scope, user_id, session_id, embedding
		FROM fact_embeddings
		WHERE 1=1
		` + scopeFilter

	rows, err := edb.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}
	defer rows.Close()

	var results []VectorSearchResult
	for rows.Next() {
		var r VectorSearchResult
		var embeddingJSON []byte

		if err := rows.Scan(&r.FactID, &r.FilePath, &r.Scope, &r.UserID, &r.SessionID, &embeddingJSON); err != nil {
			return nil, err
		}

		var embedding []float32
		if err := json.Unmarshal(embeddingJSON, &embedding); err != nil {
			slog.Warn("embeddings_db: failed to unmarshal embedding", "fact_id", r.FactID, "err", err)
			continue
		}

		// Compute cosine similarity
		r.Score = embeddingCosineSimilarity(queryEmbedding, embedding)
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// FTS5Search performs BM25 full-text search.
func (edb *EmbeddingsDBImpl) FTS5Search(ctx context.Context, query string, limit int, scopes []MemoryScope, userID, sessionID string) ([]FTS5SearchResult, error) {
	if !edb.fts5Available {
		return edb.fallbackLikeSearch(ctx, query, limit, scopes, userID, sessionID)
	}

	scopeFilter := buildScopeFilter(scopes, userID, sessionID)

	// FTS5 search with BM25 ranking
	sqlQuery := `
		SELECT fe.fact_id, fe.file_path, fe.scope, fe.user_id, fe.session_id,
		       fts.rank
		FROM fact_embeddings_fts fts
		JOIN fact_embeddings fe ON fts.rowid = fe.rowid
		WHERE fts.content MATCH ?
		` + scopeFilter + `
		ORDER BY fts.rank
		LIMIT ?
	`

	rows, err := edb.db.QueryContext(ctx, sqlQuery, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS5 search failed: %w", err)
	}
	defer rows.Close()

	var results []FTS5SearchResult
	for rows.Next() {
		var r FTS5SearchResult
		var rank float64

		if err := rows.Scan(&r.FactID, &r.FilePath, &r.Scope, &r.UserID, &r.SessionID, &rank); err != nil {
			return nil, err
		}

		// Convert FTS5 rank (negative) to positive score
		r.Score = -rank
		results = append(results, r)
	}

	return results, rows.Err()
}

// fallbackLikeSearch is a fallback when FTS5 is not available.
func (edb *EmbeddingsDBImpl) fallbackLikeSearch(ctx context.Context, query string, limit int, scopes []MemoryScope, userID, sessionID string) ([]FTS5SearchResult, error) {
	scopeFilter := buildScopeFilter(scopes, userID, sessionID)

	sqlQuery := `
		SELECT fact_id, file_path, scope, user_id, session_id
		FROM fact_embeddings
		WHERE 1=1
		` + scopeFilter + `
		LIMIT ?
	`

	rows, err := edb.db.QueryContext(ctx, sqlQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("LIKE search failed: %w", err)
	}
	defer rows.Close()

	var results []FTS5SearchResult
	for rows.Next() {
		var r FTS5SearchResult
		if err := rows.Scan(&r.FactID, &r.FilePath, &r.Scope, &r.UserID, &r.SessionID); err != nil {
			return nil, err
		}
		r.Score = 1.0 // Default score
		results = append(results, r)
	}

	return results, rows.Err()
}

// UpdateFTS5Content updates the FTS5 index with content from markdown files.
func (edb *EmbeddingsDBImpl) UpdateFTS5Content(ctx context.Context, factID, content string) error {
	if !edb.fts5Available {
		return nil // Skip if FTS5 not available
	}

	// Get rowid for the fact
	var rowid int64
	err := edb.db.QueryRowContext(ctx, `SELECT rowid FROM fact_embeddings WHERE fact_id = ?`, factID).Scan(&rowid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // Fact doesn't exist yet
		}
		return fmt.Errorf("failed to get rowid: %w", err)
	}

	// Update FTS5 content
	_, err = edb.db.ExecContext(ctx, `
		INSERT INTO fact_embeddings_fts(rowid, fact_id, content)
		VALUES (?, ?, ?)
		ON CONFLICT(rowid) DO UPDATE SET content = excluded.content
	`, rowid, factID, content)

	if err != nil {
		return fmt.Errorf("failed to update FTS5 content: %w", err)
	}

	return nil
}

// VectorSearchResult represents a vector search result.
type VectorSearchResult struct {
	FactID    string
	FilePath  string
	Scope     string
	UserID    string
	SessionID string
	Score     float64
	Distance  float64
}

// FTS5SearchResult represents an FTS5 search result.
type FTS5SearchResult struct {
	FactID    string
	FilePath  string
	Scope     string
	UserID    string
	SessionID string
	Score     float64
}

// buildScopeFilter builds SQL WHERE clause for scope filtering.
func buildScopeFilter(scopes []MemoryScope, userID, sessionID string) string {
	if len(scopes) == 0 {
		return ""
	}

	filter := " AND ("
	for i, scope := range scopes {
		if i > 0 {
			filter += " OR "
		}
		filter += fmt.Sprintf("fe.scope = '%s'", scope)

		// Add user/session filters
		if scope == ScopeUser && userID != "" {
			filter += fmt.Sprintf(" AND fe.user_id = '%s'", userID)
		}
		if scope == ScopeSession && sessionID != "" {
			filter += fmt.Sprintf(" AND fe.session_id = '%s'", sessionID)
		}
	}
	filter += ")"

	return filter
}

// embeddingCosineSimilarity computes cosine similarity between two vectors.
func embeddingCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
