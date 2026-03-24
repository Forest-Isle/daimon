package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/punkopunko/ironclaw/internal/store"
)

// VSSIndexer manages sqlite-vss HNSW indexes for fast vector search.
type VSSIndexer struct {
	db        *store.DB
	available bool
	dimension int
}

// NewVSSIndexer creates a VSS indexer and detects sqlite-vss availability.
func NewVSSIndexer(db *store.DB, dimension int) *VSSIndexer {
	v := &VSSIndexer{
		db:        db,
		dimension: dimension,
	}
	v.available = v.detectVSS()
	if v.available {
		slog.Info("memory.md: sqlite-vss available, HNSW indexing enabled")
	} else {
		slog.Warn("memory.md: sqlite-vss not available, falling back to brute-force search")
	}
	return v
}

// detectVSS checks if sqlite-vss extension is loaded.
func (v *VSSIndexer) detectVSS() bool {
	// Try to query the vss_version() function
	var version string
	err := v.db.QueryRow(`SELECT vss_version()`).Scan(&version)
	if err == nil {
		slog.Info("memory.md: sqlite-vss detected", "version", version)
		return true
	}
	return false
}

// CreateMemoryFactsIndex creates HNSW index for memory_facts table.
func (v *VSSIndexer) CreateMemoryFactsIndex(ctx context.Context) error {
	if !v.available {
		return fmt.Errorf("sqlite-vss not available")
	}

	// Create virtual table for HNSW index
	_, err := v.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_facts_vss USING vss0(
			embedding(%d)
		)
	`, v.dimension))
	if err != nil {
		return fmt.Errorf("create memory_facts_vss: %w", err)
	}

	// Populate index from existing embeddings
	_, err = v.db.ExecContext(ctx, `
		INSERT INTO memory_facts_vss(rowid, embedding)
		SELECT rowid, embedding FROM memory_facts WHERE embedding IS NOT NULL
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`)
	if err != nil {
		return fmt.Errorf("populate memory_facts_vss: %w", err)
	}

	slog.Info("memory.md: HNSW index created for memory_facts")
	return nil
}

// CreateFactEmbeddingsIndex creates HNSW index for fact_embeddings table (file-based memory).
func (v *VSSIndexer) CreateFactEmbeddingsIndex(ctx context.Context) error {
	if !v.available {
		return fmt.Errorf("sqlite-vss not available")
	}

	// Create virtual table for HNSW index
	_, err := v.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS fact_embeddings_vss USING vss0(
			embedding(%d)
		)
	`, v.dimension))
	if err != nil {
		return fmt.Errorf("create fact_embeddings_vss: %w", err)
	}

	// Populate index from existing embeddings
	_, err = v.db.ExecContext(ctx, `
		INSERT INTO fact_embeddings_vss(rowid, embedding)
		SELECT rowid, embedding FROM fact_embeddings WHERE embedding IS NOT NULL
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`)
	if err != nil {
		return fmt.Errorf("populate fact_embeddings_vss: %w", err)
	}

	slog.Info("memory.md: HNSW index created for fact_embeddings")
	return nil
}

// InsertFactEmbedding adds a fact embedding to the VSS index.
func (v *VSSIndexer) InsertFactEmbedding(ctx context.Context, factID string, embedding []float32) error {
	if !v.available {
		return nil
	}

	// Get rowid for the fact
	var rowid int64
	err := v.db.QueryRowContext(ctx, `SELECT rowid FROM fact_embeddings WHERE fact_id = ?`, factID).Scan(&rowid)
	if err != nil {
		return fmt.Errorf("failed to get rowid: %w", err)
	}

	embBytes := float32SliceToBytes(embedding)
	_, err = v.db.ExecContext(ctx, `
		INSERT INTO fact_embeddings_vss(rowid, embedding)
		VALUES (?, ?)
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`, rowid, embBytes)
	return err
}

// DeleteFactEmbedding removes a fact embedding from the VSS index.
func (v *VSSIndexer) DeleteFactEmbedding(ctx context.Context, factID string) error {
	if !v.available {
		return nil
	}

	// Get rowid for the fact
	var rowid int64
	err := v.db.QueryRowContext(ctx, `SELECT rowid FROM fact_embeddings WHERE fact_id = ?`, factID).Scan(&rowid)
	if err != nil {
		return nil // Fact doesn't exist, nothing to delete
	}

	_, err = v.db.ExecContext(ctx, `DELETE FROM fact_embeddings_vss WHERE rowid = ?`, rowid)
	return err
}

// CreateKBChunksIndex creates HNSW index for kb_chunks table.
func (v *VSSIndexer) CreateKBChunksIndex(ctx context.Context) error {
	if !v.available {
		return fmt.Errorf("sqlite-vss not available")
	}

	_, err := v.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS kb_chunks_vss USING vss0(
			embedding(%d)
		)
	`, v.dimension))
	if err != nil {
		return fmt.Errorf("create kb_chunks_vss: %w", err)
	}

	_, err = v.db.ExecContext(ctx, `
		INSERT INTO kb_chunks_vss(rowid, embedding)
		SELECT rowid, embedding FROM kb_chunks WHERE embedding IS NOT NULL
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`)
	if err != nil {
		return fmt.Errorf("populate kb_chunks_vss: %w", err)
	}

	slog.Info("memory.md: HNSW index created for kb_chunks")
	return nil
}

// SearchMemoryFacts performs HNSW-accelerated vector search on memory_facts.
func (v *VSSIndexer) SearchMemoryFacts(ctx context.Context, embedding []float32, limit int) ([]SearchResult, error) {
	if !v.available {
		return nil, fmt.Errorf("sqlite-vss not available")
	}

	embBytes := float32SliceToBytes(embedding)
	rows, err := v.db.QueryContext(ctx, `
		SELECT
			mf.id, mf.session_id, mf.user_id, mf.scope, mf.content,
			mf.embedding, mf.version, mf.expires_at, mf.metadata,
			mf.created_at, mf.updated_at,
			vss.distance
		FROM memory_facts_vss vss
		JOIN memory_facts mf ON mf.rowid = vss.rowid
		WHERE vss_search(vss.embedding, ?)
		  AND (mf.expires_at IS NULL OR mf.expires_at > CURRENT_TIMESTAMP)
		ORDER BY vss.distance
		LIMIT ?
	`, embBytes, limit)
	if err != nil {
		return nil, fmt.Errorf("vss search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		e, _, distance, ok := scanFactRowWithDistance(rows)
		if !ok {
			continue
		}
		// VSS returns distance, convert to similarity score (1 - distance)
		score := 1.0 - distance
		results = append(results, SearchResult{Entry: e, Score: score})
	}
	return results, rows.Err()
}

// SearchKBChunks performs HNSW-accelerated vector search on kb_chunks.
func (v *VSSIndexer) SearchKBChunks(ctx context.Context, embedding []float32, limit int) ([]kbSearchResult, error) {
	if !v.available {
		return nil, fmt.Errorf("sqlite-vss not available")
	}

	embBytes := float32SliceToBytes(embedding)
	rows, err := v.db.QueryContext(ctx, `
		SELECT
			kc.id, kc.source_id, kc.source_uri, kc.source_type, kc.content,
			kc.embedding, kc.chunk_index, kc.metadata, kc.created_at,
			vss.distance
		FROM kb_chunks_vss vss
		JOIN kb_chunks kc ON kc.rowid = vss.rowid
		WHERE vss_search(vss.embedding, ?)
		ORDER BY vss.distance
		LIMIT ?
	`, embBytes, limit)
	if err != nil {
		return nil, fmt.Errorf("vss search: %w", err)
	}
	defer rows.Close()

	var results []kbSearchResult
	for rows.Next() {
		var r kbSearchResult
		var distance float64
		var embBytes []byte
		var metadata string
		var sourceID, sourceURI, sourceType sql.NullString

		err := rows.Scan(
			&r.ID, &sourceID, &sourceURI, &sourceType, &r.Content,
			&embBytes, &r.ChunkIndex, &metadata, &r.CreatedAt,
			&distance,
		)
		if err != nil {
			continue
		}

		r.SourceID = sourceID.String
		r.SourceURI = sourceURI.String
		r.SourceType = sourceType.String
		r.Score = 1.0 - distance // Convert distance to similarity
		results = append(results, r)
	}
	return results, rows.Err()
}

// IndexNewFact adds a new fact to the HNSW index.
func (v *VSSIndexer) IndexNewFact(ctx context.Context, rowid int64, embedding []float32) error {
	if !v.available {
		return nil // Silently skip if VSS not available
	}

	embBytes := float32SliceToBytes(embedding)
	_, err := v.db.ExecContext(ctx, `
		INSERT INTO memory_facts_vss(rowid, embedding)
		VALUES (?, ?)
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`, rowid, embBytes)
	return err
}

// IndexNewChunk adds a new chunk to the HNSW index.
func (v *VSSIndexer) IndexNewChunk(ctx context.Context, rowid int64, embedding []float32) error {
	if !v.available {
		return nil
	}

	embBytes := float32SliceToBytes(embedding)
	_, err := v.db.ExecContext(ctx, `
		INSERT INTO kb_chunks_vss(rowid, embedding)
		VALUES (?, ?)
		ON CONFLICT(rowid) DO UPDATE SET embedding = excluded.embedding
	`, rowid, embBytes)
	return err
}

// scanFactRowWithDistance scans a fact row with distance field and returns the distance separately.
func scanFactRowWithDistance(rows *sql.Rows) (Entry, []byte, float64, bool) {
	var e Entry
	var embBytes []byte
	var metadata string
	var sessionID, userID, scope sql.NullString
	var expiresAt sql.NullTime
	var distance float64

	err := rows.Scan(
		&e.ID, &sessionID, &userID, &scope,
		&e.Content, &embBytes, &e.Version, &expiresAt,
		&metadata, &e.CreatedAt, &e.UpdatedAt,
		&distance,
	)
	if err != nil {
		return Entry{}, nil, 0, false
	}

	e.SessionID = sessionID.String
	e.UserID = userID.String
	if scope.Valid {
		e.Scope = MemoryScope(scope.String)
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		e.ExpiresAt = &t
	}
	if metadata != "" {
		json.Unmarshal([]byte(metadata), &e.Metadata) //nolint:errcheck
	}
	return e, embBytes, distance, true
}

// kbSearchResult is a temporary struct for KB search results.
type kbSearchResult struct {
	ID         string
	SourceID   string
	SourceURI  string
	SourceType string
	Content    string
	ChunkIndex int
	CreatedAt  string
	Score      float64
}
