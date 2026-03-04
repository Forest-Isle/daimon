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

const (
	defaultBM25Weight   = 0.4
	defaultVectorWeight = 0.6
	rrfK                = 60 // RRF constant
)

// SQLiteStore implements Store using SQLite.
// It supports hybrid vector + FTS5 search with Reciprocal Rank Fusion (RRF),
// and writes to both the legacy `memories` table and the new `memory_facts` table.
type SQLiteStore struct {
	db            *store.DB
	embedder      EmbeddingProvider
	fts5Available bool
	cfg           MemoryConfig
	vssIndexer    *VSSIndexer
	searchCache   *SearchResultCache
}

// MemoryConfig holds tunable parameters for the memory subsystem.
// It is populated from config.MemoryConfig in the gateway layer.
type MemoryConfig struct {
	FactExtraction        bool
	SimilarityThreshold   float64
	ConsolidationInterval time.Duration
	BM25Weight            float64
	VectorWeight          float64
	EnableVSS             bool          // Enable HNSW indexing via sqlite-vss
	VectorDimension       int           // Embedding dimension (default: 1536 for OpenAI)
	EnableSearchCache     bool          // Enable search result caching
	SearchCacheSize       int           // Max cached queries (default: 500)
	SearchCacheTTL        time.Duration // Cache TTL (default: 5min)
}

// NewSQLiteStore creates a SQLiteStore, probing FTS5 availability at startup.
func NewSQLiteStore(db *store.DB, embedder EmbeddingProvider, cfg MemoryConfig) *SQLiteStore {
	// Set defaults
	if cfg.VectorDimension <= 0 {
		cfg.VectorDimension = 1536 // OpenAI ada-002 default
	}
	if cfg.SearchCacheSize <= 0 {
		cfg.SearchCacheSize = 500
	}
	if cfg.SearchCacheTTL <= 0 {
		cfg.SearchCacheTTL = 5 * time.Minute
	}

	s := &SQLiteStore{db: db, embedder: embedder, cfg: cfg}
	s.fts5Available = s.detectFTS5()
	if s.fts5Available {
		slog.Info("memory: FTS5 available, hybrid search enabled")
	} else {
		slog.Warn("memory: FTS5 not available, falling back to LIKE search")
	}

	// Initialize VSS indexer if enabled
	if cfg.EnableVSS {
		s.vssIndexer = NewVSSIndexer(db, cfg.VectorDimension)
		if s.vssIndexer.available {
			// Create indexes in background
			go func() {
				ctx := context.Background()
				if err := s.vssIndexer.CreateMemoryFactsIndex(ctx); err != nil {
					slog.Warn("memory: failed to create VSS index for memory_facts", "err", err)
				}
			}()
		}
	}

	// Initialize search cache if enabled
	if cfg.EnableSearchCache {
		s.searchCache = NewSearchResultCache(cfg.SearchCacheSize, cfg.SearchCacheTTL)
		slog.Info("memory: search result cache enabled", "size", cfg.SearchCacheSize, "ttl", cfg.SearchCacheTTL)
	}

	return s
}

// detectFTS5 probes whether the memory_facts_fts virtual table is usable.
func (s *SQLiteStore) detectFTS5() bool {
	// A simple integrity-check is the canonical probe for FTS5 tables.
	_, err := s.db.Exec(`INSERT INTO memory_facts_fts(memory_facts_fts) VALUES('integrity-check')`)
	if err != nil {
		// Fallback: try a harmless read query.
		_, err2 := s.db.Exec(`SELECT * FROM memory_facts_fts LIMIT 0`)
		return err2 == nil
	}
	return true
}

// Save writes an entry to the legacy `memories` table for backward compatibility.
func (s *SQLiteStore) Save(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// Generate embedding if not provided.
	if len(entry.Embedding) == 0 && entry.Content != "" {
		emb, err := s.embedder.Embed(ctx, entry.Content)
		if err != nil {
			slog.Warn("memory: failed to generate embedding", "err", err)
		} else {
			entry.Embedding = emb
		}
	}

	metadata, _ := json.Marshal(entry.Metadata)
	embBytes := float32SliceToBytes(entry.Embedding)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, session_id, content, embedding, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		entry.ID, entry.SessionID, entry.Content, embBytes, string(metadata), entry.CreatedAt,
	)
	return err
}

// SaveFact writes an entry to the `memory_facts` table.
func (s *SQLiteStore) SaveFact(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("fact_%d", time.Now().UnixNano())
	}
	now := time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = now
	}
	if entry.Scope == "" {
		entry.Scope = ScopeSession
	}
	if entry.Version <= 0 {
		entry.Version = 1
	}

	// Generate embedding if not provided.
	if len(entry.Embedding) == 0 && entry.Content != "" {
		emb, err := s.embedder.Embed(ctx, entry.Content)
		if err != nil {
			slog.Warn("memory: failed to generate fact embedding", "err", err)
		} else {
			entry.Embedding = emb
		}
	}

	metadata, _ := json.Marshal(entry.Metadata)
	embBytes := float32SliceToBytes(entry.Embedding)
	category := entry.Metadata["category"]

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_facts
		     (id, session_id, user_id, scope, content, embedding, category, version, expires_at, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		entry.ID, entry.SessionID, entry.UserID, string(entry.Scope),
		entry.Content, embBytes, category, entry.Version, entry.ExpiresAt,
		string(metadata), entry.CreatedAt, entry.UpdatedAt,
	)
	if err != nil {
		return err
	}

	// Index in VSS if enabled
	if s.vssIndexer != nil && s.vssIndexer.available && len(entry.Embedding) > 0 {
		rowid, _ := result.LastInsertId()
		if rowid > 0 {
			go s.vssIndexer.IndexNewFact(context.Background(), rowid, entry.Embedding)
		}
	}

	// Invalidate search cache
	if s.searchCache != nil {
		s.searchCache.Invalidate()
	}

	return nil
}

// ListByScope returns all non-expired entries in memory_facts for a given scope and user.
func (s *SQLiteStore) ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, user_id, scope, content, embedding, version, expires_at, metadata, created_at, updated_at
		   FROM memory_facts
		  WHERE scope = ?
		    AND (user_id = ? OR user_id IS NULL OR ? = '')
		    AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		  ORDER BY updated_at DESC`,
		string(scope), userID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanFactRows(rows)
}

// UpdateFact updates the content and version of an existing fact.
func (s *SQLiteStore) UpdateFact(ctx context.Context, id string, content string, version int) error {
	now := time.Now()

	// Re-embed the updated content.
	var embBytes []byte
	var embedding []float32
	if content != "" {
		emb, err := s.embedder.Embed(ctx, content)
		if err != nil {
			slog.Warn("memory: failed to re-embed updated fact", "id", id, "err", err)
		} else {
			embBytes = float32SliceToBytes(emb)
			embedding = emb
		}
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE memory_facts SET content = ?, embedding = ?, version = ?, updated_at = ? WHERE id = ?`,
		content, embBytes, version, now, id,
	)
	if err != nil {
		return err
	}

	// Update VSS index if enabled
	if s.vssIndexer != nil && s.vssIndexer.available && len(embedding) > 0 {
		var rowid int64
		err := s.db.QueryRow(`SELECT rowid FROM memory_facts WHERE id = ?`, id).Scan(&rowid)
		if err == nil {
			go s.vssIndexer.IndexNewFact(context.Background(), rowid, embedding)
		}
	}

	// Invalidate search cache
	if s.searchCache != nil {
		s.searchCache.Invalidate()
	}

	return nil
}

// DeleteFact removes a fact from memory_facts by ID.
func (s *SQLiteStore) DeleteFact(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_facts WHERE id = ?`, id)

	// Invalidate search cache
	if s.searchCache != nil {
		s.searchCache.Invalidate()
	}

	return err
}

// Delete removes an entry from the legacy `memories` table.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	return err
}

// Search performs a hybrid vector + FTS5 search across both tables,
// fusing scores via Reciprocal Rank Fusion (RRF).
func (s *SQLiteStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	if query.Limit <= 0 {
		query.Limit = 10
	}

	// Check cache first
	if s.searchCache != nil {
		if cached, ok := s.searchCache.Get(query); ok {
			return cached, nil
		}
	}

	// Generate query embedding.
	if len(query.Embedding) == 0 && query.Text != "" {
		emb, err := s.embedder.Embed(ctx, query.Text)
		if err != nil {
			slog.Warn("memory: embed query failed, falling back to text search", "err", err)
		} else {
			query.Embedding = emb
		}
	}

	bm25Weight := s.cfg.BM25Weight
	if bm25Weight <= 0 {
		bm25Weight = defaultBM25Weight
	}
	vectorWeight := s.cfg.VectorWeight
	if vectorWeight <= 0 {
		vectorWeight = defaultVectorWeight
	}

	// Use VSS index if available, otherwise fall back to brute-force
	var vectorResults []SearchResult
	if s.vssIndexer != nil && s.vssIndexer.available && len(query.Embedding) > 0 {
		vssResults, err := s.vssIndexer.SearchMemoryFacts(ctx, query.Embedding, query.Limit*3)
		if err != nil {
			slog.Warn("memory: VSS search failed, falling back to brute-force", "err", err)
			vectorResults = s.vectorSearchBoth(ctx, query)
		} else {
			vectorResults = vssResults
		}
	} else {
		vectorResults = s.vectorSearchBoth(ctx, query)
	}

	// Collect BM25/text candidates from both tables.
	var textResults []SearchResult
	if s.fts5Available && query.Text != "" {
		textResults = s.fts5Search(ctx, query)
	} else if query.Text != "" {
		textResults = s.likeSearchBoth(ctx, query)
	}

	// If no text search was performed and no vector results, return empty.
	if len(vectorResults) == 0 && len(textResults) == 0 {
		return nil, nil
	}

	// Build ID → rank maps.
	vectorRankMap := make(map[string]int, len(vectorResults))
	for i, r := range vectorResults {
		vectorRankMap[r.Entry.ID] = i
	}
	textRankMap := make(map[string]int, len(textResults))
	for i, r := range textResults {
		textRankMap[r.Entry.ID] = i
	}

	// Deduplicate by ID, keeping the Entry from whichever list has it.
	entryMap := make(map[string]Entry, len(vectorResults)+len(textResults))
	for _, r := range vectorResults {
		entryMap[r.Entry.ID] = r.Entry
	}
	for _, r := range textResults {
		if _, exists := entryMap[r.Entry.ID]; !exists {
			entryMap[r.Entry.ID] = r.Entry
		}
	}

	// Compute RRF scores.
	type scored struct {
		entry Entry
		score float64
	}
	var fused []scored
	for id, entry := range entryMap {
		vRank, hasVector := vectorRankMap[id]
		tRank, hasText := textRankMap[id]

		vIdx := -1
		if hasVector {
			vIdx = vRank
		}
		tIdx := -1
		if hasText {
			tIdx = tRank
		}

		score := rrfScore(vIdx, tIdx, vectorWeight, bm25Weight)
		fused = append(fused, scored{entry: entry, score: score})
	}

	sort.Slice(fused, func(i, j int) bool {
		return fused[i].score > fused[j].score
	})

	limit := query.Limit
	if limit > len(fused) {
		limit = len(fused)
	}

	out := make([]SearchResult, limit)
	for i := 0; i < limit; i++ {
		out[i] = SearchResult{Entry: fused[i].entry, Score: fused[i].score}
	}

	// Cache the results
	if s.searchCache != nil {
		s.searchCache.Set(query, out)
	}

	return out, nil
}

// rrfScore computes the Reciprocal Rank Fusion score for a single document.
// vectorRank and bm25Rank are 0-based; pass -1 if the document did not appear in that ranking.
func rrfScore(vectorRank, bm25Rank int, vectorWeight, bm25Weight float64) float64 {
	score := 0.0
	if vectorRank >= 0 {
		score += vectorWeight * (1.0 / float64(rrfK+vectorRank+1))
	}
	if bm25Rank >= 0 {
		score += bm25Weight * (1.0 / float64(rrfK+bm25Rank+1))
	}
	return score
}

// vectorSearchBoth runs brute-force cosine similarity over both `memories` and `memory_facts`.
func (s *SQLiteStore) vectorSearchBoth(ctx context.Context, query SearchQuery) []SearchResult {
	if len(query.Embedding) == 0 {
		return nil
	}

	type scored struct {
		entry Entry
		score float64
	}
	var results []scored

	// Query legacy memories table.
	legacyRows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, '' AS user_id, 'session' AS scope, content, embedding, 1 AS version,
		        NULL AS expires_at, metadata, created_at, created_at AS updated_at
		   FROM memories
		  WHERE embedding IS NOT NULL`)
	if err == nil {
		func() {
			defer legacyRows.Close()
			for legacyRows.Next() {
				e, embBytes, ok := scanFactRow(legacyRows)
				if !ok {
					continue
				}
				e.Embedding = bytesToFloat32Slice(embBytes)
				if len(e.Embedding) > 0 {
					score := cosineSimilarity(query.Embedding, e.Embedding)
					results = append(results, scored{entry: e, score: score})
				}
			}
		}()
	}

	// Query memory_facts table, applying optional scope / user filters.
	factsSQL, factsArgs := buildFactsVectorQuery(query)
	factsRows, err := s.db.QueryContext(ctx, factsSQL, factsArgs...)
	if err == nil {
		func() {
			defer factsRows.Close()
			for factsRows.Next() {
				e, embBytes, ok := scanFactRow(factsRows)
				if !ok {
					continue
				}
				e.Embedding = bytesToFloat32Slice(embBytes)
				if len(e.Embedding) > 0 {
					score := cosineSimilarity(query.Embedding, e.Embedding)
					results = append(results, scored{entry: e, score: score})
				}
			}
		}()
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Cap at a reasonable candidate set before RRF fusion.
	cap := query.Limit * 3
	if cap > len(results) {
		cap = len(results)
	}
	out := make([]SearchResult, cap)
	for i := 0; i < cap; i++ {
		out[i] = SearchResult{Entry: results[i].entry, Score: results[i].score}
	}
	return out
}

// buildFactsVectorQuery constructs the SELECT for memory_facts with optional scope/user filters.
func buildFactsVectorQuery(query SearchQuery) (string, []any) {
	base := `SELECT id, session_id, user_id, scope, content, embedding, version,
	                expires_at, metadata, created_at, updated_at
	           FROM memory_facts
	          WHERE embedding IS NOT NULL
	            AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`

	var args []any
	if query.UserID != "" {
		base += ` AND (user_id = ? OR scope = 'global')`
		args = append(args, query.UserID)
	}
	if len(query.Scopes) > 0 {
		placeholders := make([]byte, 0, len(query.Scopes)*2)
		for i, sc := range query.Scopes {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args = append(args, string(sc))
		}
		base += ` AND scope IN (` + string(placeholders) + `)`
	}
	if query.SessionID != "" {
		base += ` AND (session_id = ? OR scope != 'session')`
		args = append(args, query.SessionID)
	}
	return base, args
}

// fts5Search uses BM25 ranking from the memory_facts_fts virtual table.
func (s *SQLiteStore) fts5Search(ctx context.Context, query SearchQuery) []SearchResult {
	// FTS5 MATCH with BM25 ordering (lower rank = better in FTS5).
	limit := query.Limit * 3
	rows, err := s.db.QueryContext(ctx,
		`SELECT mf.id, mf.session_id, mf.user_id, mf.scope, mf.content,
		        mf.embedding, mf.version, mf.expires_at, mf.metadata,
		        mf.created_at, mf.updated_at
		   FROM memory_facts_fts fts
		   JOIN memory_facts mf ON mf.rowid = fts.rowid
		  WHERE memory_facts_fts MATCH ?
		    AND (mf.expires_at IS NULL OR mf.expires_at > CURRENT_TIMESTAMP)
		  ORDER BY fts.rank
		  LIMIT ?`,
		query.Text, limit,
	)
	if err != nil {
		slog.Warn("memory: FTS5 search failed", "err", err)
		return s.likeSearchBoth(ctx, query)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		e, _, ok := scanFactRow(rows)
		if !ok {
			continue
		}
		results = append(results, SearchResult{Entry: e, Score: 1.0})
	}
	return results
}

// likeSearchBoth falls back to LIKE searches on both tables when FTS5 is unavailable.
func (s *SQLiteStore) likeSearchBoth(ctx context.Context, query SearchQuery) []SearchResult {
	pattern := "%" + query.Text + "%"
	limit := query.Limit * 3

	var results []SearchResult

	// Legacy memories table.
	legacyRows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, '' AS user_id, 'session' AS scope, content,
		        NULL AS embedding, 1 AS version, NULL AS expires_at, metadata,
		        created_at, created_at AS updated_at
		   FROM memories
		  WHERE content LIKE ?
		  ORDER BY created_at DESC
		  LIMIT ?`,
		pattern, limit,
	)
	if err == nil {
		func() {
			defer legacyRows.Close()
			for legacyRows.Next() {
				e, _, ok := scanFactRow(legacyRows)
				if !ok {
					continue
				}
				results = append(results, SearchResult{Entry: e, Score: 1.0})
			}
		}()
	}

	// memory_facts table.
	factsRows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, user_id, scope, content, NULL AS embedding, version,
		        expires_at, metadata, created_at, updated_at
		   FROM memory_facts
		  WHERE content LIKE ?
		    AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		  ORDER BY updated_at DESC
		  LIMIT ?`,
		pattern, limit,
	)
	if err == nil {
		func() {
			defer factsRows.Close()
			for factsRows.Next() {
				e, _, ok := scanFactRow(factsRows)
				if !ok {
					continue
				}
				results = append(results, SearchResult{Entry: e, Score: 1.0})
			}
		}()
	}

	return results
}

// scanFactRow scans a single row that has the unified column layout:
// id, session_id, user_id, scope, content, embedding, version, expires_at, metadata, created_at, updated_at
func scanFactRow(rows *sql.Rows) (Entry, []byte, bool) {
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
		return Entry{}, nil, false
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
	return e, embBytes, true
}

// scanFactRows scans all rows from a memory_facts query.
func scanFactRows(rows *sql.Rows) ([]Entry, error) {
	var entries []Entry
	for rows.Next() {
		e, _, ok := scanFactRow(rows)
		if !ok {
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// cosineSimilarity computes the cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// float32SliceToBytes encodes a []float32 to little-endian bytes.
func float32SliceToBytes(f []float32) []byte {
	if len(f) == 0 {
		return nil
	}
	buf := make([]byte, len(f)*4)
	for i, v := range f {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// bytesToFloat32Slice decodes little-endian bytes to []float32.
func bytesToFloat32Slice(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	f := make([]float32, len(b)/4)
	for i := range f {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		f[i] = math.Float32frombits(bits)
	}
	return f
}
