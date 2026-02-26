package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

// SQLiteStore implements Store using SQLite.
// For MVP, it uses brute-force cosine similarity.
// Future: integrate sqlite-vec for proper ANN search.
type SQLiteStore struct {
	db       *store.DB
	embedder EmbeddingProvider
}

func NewSQLiteStore(db *store.DB, embedder EmbeddingProvider) *SQLiteStore {
	return &SQLiteStore{db: db, embedder: embedder}
}

func (s *SQLiteStore) Save(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}

	// Generate embedding if not provided
	if len(entry.Embedding) == 0 && entry.Content != "" {
		emb, err := s.embedder.Embed(ctx, entry.Content)
		if err != nil {
			slog.Warn("failed to generate embedding", "err", err)
		} else {
			entry.Embedding = emb
		}
	}

	metadata, _ := json.Marshal(entry.Metadata)
	embBytes := float32SliceToBytes(entry.Embedding)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, session_id, content, embedding, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.SessionID, entry.Content, embBytes, string(metadata), entry.CreatedAt)
	return err
}

func (s *SQLiteStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	if query.Limit <= 0 {
		query.Limit = 10
	}

	// Generate query embedding
	if len(query.Embedding) == 0 && query.Text != "" {
		emb, err := s.embedder.Embed(ctx, query.Text)
		if err != nil {
			return nil, fmt.Errorf("embed query: %w", err)
		}
		query.Embedding = emb
	}

	// If no embedding available, fall back to text search
	if len(query.Embedding) == 0 {
		return s.textSearch(ctx, query)
	}

	return s.vectorSearch(ctx, query)
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) textSearch(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	sqlQuery := `SELECT id, session_id, content, metadata, created_at FROM memories WHERE content LIKE ? ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, sqlQuery, "%"+query.Text+"%", query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanResults(rows)
}

func (s *SQLiteStore) vectorSearch(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	// Brute-force: load all embeddings and compute cosine similarity
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, content, embedding, metadata, created_at FROM memories WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		entry Entry
		score float64
	}

	var results []scored
	for rows.Next() {
		var e Entry
		var embBytes []byte
		var metadata string
		var sessionID sql.NullString

		if err := rows.Scan(&e.ID, &sessionID, &e.Content, &embBytes, &metadata, &e.CreatedAt); err != nil {
			continue
		}
		e.SessionID = sessionID.String
		json.Unmarshal([]byte(metadata), &e.Metadata)
		e.Embedding = bytesToFloat32Slice(embBytes)

		if len(e.Embedding) > 0 {
			score := cosineSimilarity(query.Embedding, e.Embedding)
			results = append(results, scored{entry: e, score: score})
		}
	}

	// Sort by score descending
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	limit := query.Limit
	if limit > len(results) {
		limit = len(results)
	}

	out := make([]SearchResult, limit)
	for i := 0; i < limit; i++ {
		out[i] = SearchResult{Entry: results[i].entry, Score: results[i].score}
	}
	return out, nil
}

func scanResults(rows *sql.Rows) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var e Entry
		var metadata string
		var sessionID sql.NullString
		if err := rows.Scan(&e.ID, &sessionID, &e.Content, &metadata, &e.CreatedAt); err != nil {
			continue
		}
		e.SessionID = sessionID.String
		json.Unmarshal([]byte(metadata), &e.Metadata)
		results = append(results, SearchResult{Entry: e, Score: 1.0})
	}
	return results, rows.Err()
}

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
