package knowledge

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
	rrfK           = 60
	defaultBM25W   = 0.4
	defaultVectorW = 0.6
)

// SQLiteKnowledgeBase implements KnowledgeBase using SQLite.
type SQLiteKnowledgeBase struct {
	db            *store.DB
	embedder      EmbeddingProvider
	fts5Available bool
	cfg           Config
	pipeline      *IngestPipeline
	searchCache   *KnowledgeSearchCache
}

// Config holds knowledge base configuration.
type Config struct {
	ChunkSize         int
	ChunkOverlap      int
	BM25Weight        float64
	VectorWeight      float64
	IngestDirs        []string
	EnableSearchCache bool          // Enable search result caching
	SearchCacheSize   int           // Max cached queries (default: 500)
	SearchCacheTTL    time.Duration // Cache TTL (default: 5min)
}

// New creates a new SQLiteKnowledgeBase.
func New(db *store.DB, embedder EmbeddingProvider, cfg Config) *SQLiteKnowledgeBase {
	// Set defaults
	if cfg.SearchCacheSize <= 0 {
		cfg.SearchCacheSize = 500
	}
	if cfg.SearchCacheTTL <= 0 {
		cfg.SearchCacheTTL = 5 * time.Minute
	}

	kb := &SQLiteKnowledgeBase{
		db:       db,
		embedder: embedder,
		cfg:      cfg,
	}
	kb.fts5Available = kb.detectFTS5()
	kb.pipeline = NewIngestPipeline(kb, cfg)

	// Initialize search cache if enabled
	if cfg.EnableSearchCache {
		kb.searchCache = NewKnowledgeSearchCache(cfg.SearchCacheSize, cfg.SearchCacheTTL)
		slog.Info("knowledge: search result cache enabled", "size", cfg.SearchCacheSize, "ttl", cfg.SearchCacheTTL)
	}

	return kb
}

// GetPipeline returns the ingest pipeline.
func (kb *SQLiteKnowledgeBase) GetPipeline() *IngestPipeline {
	return kb.pipeline
}

func (kb *SQLiteKnowledgeBase) detectFTS5() bool {
	_, err := kb.db.Exec(`SELECT * FROM kb_chunks_fts LIMIT 0`)
	return err == nil
}

// Search retrieves relevant chunks via hybrid vector + BM25 search with RRF fusion.
func (kb *SQLiteKnowledgeBase) Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error) {
	if query.Limit <= 0 {
		query.Limit = 10
	}

	// Check cache first
	if kb.searchCache != nil {
		if cached, ok := kb.searchCache.Get(query); ok {
			return cached, nil
		}
	}

	if len(query.Embedding) == 0 && query.Text != "" {
		emb, err := kb.embedder.Embed(ctx, query.Text)
		if err != nil {
			slog.Warn("knowledge: embed query failed", "err", err)
		} else {
			query.Embedding = emb
		}
	}

	bm25W := kb.cfg.BM25Weight
	if bm25W <= 0 {
		bm25W = defaultBM25W
	}
	vectorW := kb.cfg.VectorWeight
	if vectorW <= 0 {
		vectorW = defaultVectorW
	}

	vectorResults := kb.vectorSearch(ctx, query)
	var textResults []KnowledgeResult
	if kb.fts5Available && query.Text != "" {
		textResults = kb.fts5Search(ctx, query)
	} else if query.Text != "" {
		textResults = kb.likeSearch(ctx, query)
	}

	if len(vectorResults) == 0 && len(textResults) == 0 {
		return nil, nil
	}

	vectorRankMap := make(map[string]int, len(vectorResults))
	for i, r := range vectorResults {
		vectorRankMap[r.Chunk.ID] = i
	}
	textRankMap := make(map[string]int, len(textResults))
	for i, r := range textResults {
		textRankMap[r.Chunk.ID] = i
	}

	chunkMap := make(map[string]Chunk, len(vectorResults)+len(textResults))
	for _, r := range vectorResults {
		chunkMap[r.Chunk.ID] = r.Chunk
	}
	for _, r := range textResults {
		if _, exists := chunkMap[r.Chunk.ID]; !exists {
			chunkMap[r.Chunk.ID] = r.Chunk
		}
	}

	type scored struct {
		chunk Chunk
		score float64
	}
	var fused []scored
	for id, chunk := range chunkMap {
		vRank, hasV := vectorRankMap[id]
		tRank, hasT := textRankMap[id]
		vIdx, tIdx := -1, -1
		if hasV {
			vIdx = vRank
		}
		if hasT {
			tIdx = tRank
		}
		score := kbRRFScore(vIdx, tIdx, vectorW, bm25W)
		fused = append(fused, scored{chunk: chunk, score: score})
	}

	sort.Slice(fused, func(i, j int) bool {
		return fused[i].score > fused[j].score
	})

	limit := query.Limit
	if limit > len(fused) {
		limit = len(fused)
	}
	out := make([]KnowledgeResult, limit)
	for i := 0; i < limit; i++ {
		out[i] = KnowledgeResult{Chunk: fused[i].chunk, Score: fused[i].score}
	}

	// Cache the results
	if kb.searchCache != nil {
		kb.searchCache.Set(query, out)
	}

	return out, nil
}

func kbRRFScore(vectorRank, bm25Rank int, vectorW, bm25W float64) float64 {
	score := 0.0
	if vectorRank >= 0 {
		score += vectorW * (1.0 / float64(rrfK+vectorRank+1))
	}
	if bm25Rank >= 0 {
		score += bm25W * (1.0 / float64(rrfK+bm25Rank+1))
	}
	return score
}

func (kb *SQLiteKnowledgeBase) vectorSearch(ctx context.Context, query KnowledgeQuery) []KnowledgeResult {
	if len(query.Embedding) == 0 {
		return nil
	}
	sqlQuery := `SELECT id, source_id, source_uri, source_type, content, embedding, chunk_index, metadata, created_at
                   FROM kb_chunks WHERE embedding IS NOT NULL`
	var args []any
	if query.SourceType != "" {
		sqlQuery += ` AND source_type = ?`
		args = append(args, query.SourceType)
	}
	rows, err := kb.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type scored struct {
		chunk Chunk
		score float64
	}
	var results []scored
	for rows.Next() {
		c, embBytes, ok := scanChunk(rows)
		if !ok {
			continue
		}
		emb := bytesToFloat32(embBytes)
		if len(emb) > 0 {
			score := cosineSim(query.Embedding, emb)
			results = append(results, scored{chunk: c, score: score})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	cap := query.Limit * 3
	if cap > len(results) {
		cap = len(results)
	}
	out := make([]KnowledgeResult, cap)
	for i := range out {
		out[i] = KnowledgeResult{Chunk: results[i].chunk, Score: results[i].score}
	}
	return out
}

func (kb *SQLiteKnowledgeBase) fts5Search(ctx context.Context, query KnowledgeQuery) []KnowledgeResult {
	limit := query.Limit * 3
	rows, err := kb.db.QueryContext(ctx,
		`SELECT kc.id, kc.source_id, kc.source_uri, kc.source_type, kc.content,
                kc.embedding, kc.chunk_index, kc.metadata, kc.created_at
           FROM kb_chunks_fts fts
           JOIN kb_chunks kc ON kc.rowid = fts.rowid
          WHERE kb_chunks_fts MATCH ?
          ORDER BY fts.rank
          LIMIT ?`,
		query.Text, limit,
	)
	if err != nil {
		slog.Warn("knowledge: FTS5 search failed", "err", err)
		return kb.likeSearch(ctx, query)
	}
	defer rows.Close()
	return scanChunkResults(rows)
}

func (kb *SQLiteKnowledgeBase) likeSearch(ctx context.Context, query KnowledgeQuery) []KnowledgeResult {
	limit := query.Limit * 3
	sqlQuery := `SELECT id, source_id, source_uri, source_type, content,
                        NULL AS embedding, chunk_index, metadata, created_at
                   FROM kb_chunks
                  WHERE content LIKE ?
                  ORDER BY created_at DESC
                  LIMIT ?`
	rows, err := kb.db.QueryContext(ctx, sqlQuery, "%"+query.Text+"%", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanChunkResults(rows)
}

// Ingest delegates to the pipeline.
func (kb *SQLiteKnowledgeBase) Ingest(ctx context.Context, uri, sourceType string) error {
	return kb.pipeline.Ingest(ctx, uri, sourceType)
}

// Sources returns all ingested sources.
func (kb *SQLiteKnowledgeBase) Sources(ctx context.Context) ([]Source, error) {
	rows, err := kb.db.QueryContext(ctx,
		`SELECT id, uri, source_type, title, chunk_count, metadata, created_at, updated_at FROM kb_sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		var metadata string
		var title sql.NullString
		if err := rows.Scan(&s.ID, &s.URI, &s.SourceType, &title, &s.ChunkCount, &metadata, &s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		s.Title = title.String
		json.Unmarshal([]byte(metadata), &s.Metadata) //nolint:errcheck
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// DeleteSource removes a source and cascades to its chunks.
func (kb *SQLiteKnowledgeBase) DeleteSource(ctx context.Context, sourceID string) error {
	_, err := kb.db.ExecContext(ctx, `DELETE FROM kb_sources WHERE id = ?`, sourceID)
	return err
}

// saveSource upserts a source record. Returns the source ID.
func (kb *SQLiteKnowledgeBase) saveSource(ctx context.Context, uri, sourceType, title string) (string, error) {
	// Check if exists
	var id string
	err := kb.db.QueryRowContext(ctx, `SELECT id FROM kb_sources WHERE uri = ?`, uri).Scan(&id)
	if err == nil {
		// Update
		_, err2 := kb.db.ExecContext(ctx,
			`UPDATE kb_sources SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			title, id)
		return id, err2
	}

	id = fmt.Sprintf("src_%d", time.Now().UnixNano())
	_, err = kb.db.ExecContext(ctx,
		`INSERT INTO kb_sources (id, uri, source_type, title, chunk_count) VALUES (?, ?, ?, ?, 0)`,
		id, uri, sourceType, title)
	return id, err
}

// saveChunk stores a chunk with its embedding.
func (kb *SQLiteKnowledgeBase) saveChunk(ctx context.Context, chunk Chunk) error {
	if chunk.ID == "" {
		chunk.ID = fmt.Sprintf("chunk_%d", time.Now().UnixNano())
	}
	if chunk.CreatedAt.IsZero() {
		chunk.CreatedAt = time.Now()
	}
	var embBytes []byte
	if len(chunk.Embedding) > 0 {
		embBytes = float32ToBytes(chunk.Embedding)
	} else if chunk.Content != "" && kb.embedder != nil {
		emb, err := kb.embedder.Embed(context.Background(), chunk.Content)
		if err != nil {
			slog.Warn("knowledge: failed to embed chunk", "err", err)
		} else {
			embBytes = float32ToBytes(emb)
		}
	}
	meta, _ := json.Marshal(chunk.Metadata)
	_, err := kb.db.ExecContext(ctx,
		`INSERT INTO kb_chunks (id, source_id, source_uri, source_type, content, embedding, chunk_index, metadata, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO NOTHING`,
		chunk.ID, chunk.SourceID, chunk.SourceURI, chunk.SourceType,
		chunk.Content, embBytes, chunk.ChunkIndex, string(meta), chunk.CreatedAt,
	)

	// Invalidate search cache when new chunks are added
	if kb.searchCache != nil {
		kb.searchCache.Invalidate()
	}

	return err
}

// updateChunkCount updates the chunk_count on a source.
func (kb *SQLiteKnowledgeBase) updateChunkCount(ctx context.Context, sourceID string) {
	kb.db.ExecContext(ctx, //nolint:errcheck
		`UPDATE kb_sources SET chunk_count = (SELECT COUNT(*) FROM kb_chunks WHERE source_id = ?), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		sourceID, sourceID,
	)
}

func scanChunk(rows *sql.Rows) (Chunk, []byte, bool) {
	var c Chunk
	var embBytes []byte
	var metadata string
	var sourceID, sourceURI, sourceType sql.NullString
	err := rows.Scan(&c.ID, &sourceID, &sourceURI, &sourceType, &c.Content, &embBytes, &c.ChunkIndex, &metadata, &c.CreatedAt)
	if err != nil {
		return Chunk{}, nil, false
	}
	c.SourceID = sourceID.String
	c.SourceURI = sourceURI.String
	c.SourceType = sourceType.String
	json.Unmarshal([]byte(metadata), &c.Metadata) //nolint:errcheck
	return c, embBytes, true
}

func scanChunkResults(rows *sql.Rows) []KnowledgeResult {
	var results []KnowledgeResult
	for rows.Next() {
		c, _, ok := scanChunk(rows)
		if !ok {
			continue
		}
		results = append(results, KnowledgeResult{Chunk: c, Score: 1.0})
	}
	return results
}

func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func float32ToBytes(f []float32) []byte {
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

func bytesToFloat32(b []byte) []float32 {
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
