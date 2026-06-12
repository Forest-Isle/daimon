package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Forest-Isle/daimon/internal/util"
	"gopkg.in/yaml.v3"
)

// FileMemoryStore implements Store with Markdown files as primary storage
// and SQLite tables for indexing (FTS5 + vector embeddings).
var _ Store = (*FileMemoryStore)(nil)

type FileMemoryStore struct {
	baseDir  string
	db       *sql.DB
	embedder EmbeddingProvider
	cfg      MemoryConfig
}

// MemoryFile represents a memory stored as Markdown with YAML frontmatter.
type MemoryFile struct {
	ID           string            `yaml:"id"`
	Scope        string            `yaml:"scope"`
	UserID       string            `yaml:"user_id,omitempty"`
	SessionID    string            `yaml:"session_id,omitempty"`
	CreatedAt    time.Time         `yaml:"created_at"`
	UpdatedAt    time.Time         `yaml:"updated_at"`
	LastAccessed *time.Time        `yaml:"last_accessed_at,omitempty"`
	Strength     float64           `yaml:"strength,omitempty"`
	Type         string            `yaml:"type,omitempty"`
	Importance   int               `yaml:"importance,omitempty"`
	Emotion      string            `yaml:"emotion,omitempty"`
	Sensitivity  string            `yaml:"sensitivity,omitempty"`
	RelatedTo    string            `yaml:"related_to,omitempty"`
	PromotedFrom string            `yaml:"promoted_from,omitempty"`
	PromotedAt   *time.Time        `yaml:"promoted_at,omitempty"`
	ValidFrom    *time.Time        `yaml:"valid_from,omitempty"`
	ValidTo      *time.Time        `yaml:"valid_to,omitempty"`
	Metadata     map[string]string `yaml:"metadata,omitempty"`
	Content      string            `yaml:"-"`
}

// NewFileMemoryStore creates a file-based memory store.
func NewFileMemoryStore(baseDir string, db *sql.DB, embedder EmbeddingProvider, cfg MemoryConfig) (*FileMemoryStore, error) {
	store := &FileMemoryStore{
		baseDir:  baseDir,
		db:       db,
		embedder: embedder,
		cfg:      cfg,
	}
	if err := store.initDirectories(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileMemoryStore) initDirectories() error {
	dirs := []string{"user", "session", "feedback", "global", "archived"}
	for _, dir := range dirs {
		path := filepath.Join(s.baseDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
	}
	return nil
}

// Save writes a memory entry as a Markdown file and syncs SQLite indexes.
func (s *FileMemoryStore) Save(ctx context.Context, entry Entry) error {
	mf := MemoryFile{
		ID:        entry.ID,
		Scope:     string(entry.Scope),
		UserID:    entry.UserID,
		SessionID: entry.SessionID,
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
		Metadata:  entry.Metadata,
		Content:   entry.Content,
	}
	if entry.Metadata != nil {
		if t, ok := entry.Metadata["type"]; ok {
			mf.Type = t
		}
		if imp, ok := entry.Metadata["importance"]; ok {
			if v, err := strconv.Atoi(imp); err == nil {
				mf.Importance = v
			}
		}
		if e, ok := entry.Metadata["emotion"]; ok {
			mf.Emotion = e
		}
		if sens, ok := entry.Metadata["sensitivity"]; ok {
			mf.Sensitivity = sens
		}
	}

	filePath := s.buildFilePath(entry.Scope, entry.ID, entry.CreatedAt)
	if err := s.writeFileAtomic(filePath, mf); err != nil {
		return err
	}
	return s.syncIndex(ctx, entry, filePath)
}

// Search performs hybrid search (BM25 FTS5 + vector cosine + RRF fusion).
func (s *FileMemoryStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	whereClause := []string{"1=1"}
	args := []interface{}{}

	if query.UserID != "" {
		whereClause = append(whereClause, "user_id = ?")
		args = append(args, query.UserID)
	}
	if query.SessionID != "" {
		whereClause = append(whereClause, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.TypeFilter != "" {
		whereClause = append(whereClause, "memory_type = ?")
		args = append(args, query.TypeFilter)
	}
	if len(query.ExcludeTypes) > 0 {
		placeholders := strings.Repeat("?,", len(query.ExcludeTypes))
		placeholders = placeholders[:len(placeholders)-1]
		whereClause = append(whereClause, fmt.Sprintf("memory_type NOT IN (%s)", placeholders))
		for _, t := range query.ExcludeTypes {
			args = append(args, t)
		}
	}
	if !query.IncludeHistorical {
		whereClause = append(whereClause, "valid_to IS NULL")
	}
	whereClause = append(whereClause, "sensitivity != 'secret'")
	if query.UserID == "" {
		whereClause = append(whereClause, "sensitivity != 'private'")
	}

	sqlQuery := fmt.Sprintf(
		"SELECT memory_id, file_path, strength FROM memory_index WHERE %s",
		strings.Join(whereClause, " AND "),
	)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query memory_index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var indexResults []idxRow
	for rows.Next() {
		var r idxRow
		if err := rows.Scan(&r.id, &r.filePath, &r.strength); err != nil {
			return nil, err
		}
		indexResults = append(indexResults, r)
	}
	if len(indexResults) == 0 {
		return []SearchResult{}, nil
	}

	// Hybrid search: BM25 + vector with RRF fusion.
	scored, err := s.hybridSearch(ctx, query, indexResults)
	if err != nil {
		return nil, err
	}

	// Trim to limit.
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}

	// Load content from Markdown files.
	results := make([]SearchResult, 0, len(scored))
	for i := range scored {
		mf, err := s.parseFile(scored[i].filePath)
		if err != nil {
			slog.Warn("memory: failed to parse result file", "path", scored[i].filePath, "err", err)
			continue
		}
		results = append(results, SearchResult{
			Entry: Entry{
				ID:        scored[i].Entry.ID,
				Scope:     MemoryScope(mf.Scope),
				UserID:    mf.UserID,
				SessionID: mf.SessionID,
				Content:   mf.Content,
				Metadata:  mf.Metadata,
				CreatedAt: mf.CreatedAt,
				UpdatedAt: mf.UpdatedAt,
			},
			Score: scored[i].Score,
		})
	}
	return results, nil
}

// idxRow holds a row from memory_index used during search.
type idxRow struct {
	id       string
	filePath string
	strength float64
}

// scoredResult pairs a result with its file path for later content loading.
type scoredResult struct {
	SearchResult
	filePath string
}

func (s *FileMemoryStore) hybridSearch(ctx context.Context, query SearchQuery, indexResults []idxRow) ([]scoredResult, error) {
	idMap := make(map[string]idxRow)
	for _, r := range indexResults {
		idMap[r.id] = r
	}

	// BM25 via FTS5.
	bm25Results := make(map[string]float64)
	if query.Text != "" {
		ftsQuery := sanitizeFTS5Query(query.Text)
		if ftsQuery != "" {
			rows, err := s.db.QueryContext(ctx, `
				SELECT memory_id, rank
				FROM memory_fts
				WHERE memory_fts MATCH ?
				ORDER BY rank
				LIMIT 100
			`, ftsQuery)
			if err != nil {
				slog.Debug("memory: FTS5 search error", "err", err, "query_preview", util.TruncateRunes(ftsQuery, 60))
			} else {
				defer func() { _ = rows.Close() }()
				for rows.Next() {
					var id string
					var rank float64
					if err := rows.Scan(&id, &rank); err == nil {
						if _, ok := idMap[id]; ok {
							bm25Results[id] = -rank // FTS5 rank is negative
						}
					}
				}
			}
		}
	}

	// Vector similarity search.
	vectorResults := make(map[string]float64)
	if len(query.Embedding) > 0 && len(idMap) > 0 {
		ids := make([]string, 0, len(idMap))
		for id := range idMap {
			ids = append(ids, id)
		}
		placeholders := make([]string, len(ids))
		batchArgs := make([]any, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			batchArgs[i] = id
		}
		batchQuery := "SELECT memory_id, embedding FROM memory_embeddings WHERE memory_id IN (" + strings.Join(placeholders, ",") + ")"
		embRows, embErr := s.db.QueryContext(ctx, batchQuery, batchArgs...)
		if embErr == nil {
			defer func() { _ = embRows.Close() }()
			for embRows.Next() {
				var id string
				var embBytes []byte
				if scanErr := embRows.Scan(&id, &embBytes); scanErr == nil {
					emb := deserializeEmbedding(embBytes)
					vectorResults[id] = cosineSimilarity(query.Embedding, emb)
				}
			}
		}
	}

	return s.rrfFusion(bm25Results, vectorResults, idMap), nil
}

func (s *FileMemoryStore) rrfFusion(bm25Results, vectorResults map[string]float64, idMap map[string]idxRow) []scoredResult {
	const k = 60.0
	scores := make(map[string]float64)

	bm25Sorted := sortByScore(bm25Results)
	for rank, id := range bm25Sorted {
		scores[id] += 1.0 / (k + float64(rank+1))
	}
	vectorSorted := sortByScore(vectorResults)
	for rank, id := range vectorSorted {
		scores[id] += 1.0 / (k + float64(rank+1))
	}

	for id, score := range scores {
		if r, ok := idMap[id]; ok {
			scores[id] = score*0.7 + r.strength*0.3
		}
	}

	finalSorted := sortByScore(scores)
	results := make([]scoredResult, 0, len(finalSorted))
	for _, id := range finalSorted {
		if r, ok := idMap[id]; ok {
			results = append(results, scoredResult{
				SearchResult: SearchResult{
					Entry: Entry{
						ID:        id,
						CreatedAt: time.Now(),
					},
					Score: scores[id],
				},
				filePath: r.filePath,
			})
		}
	}
	return results
}

func sortByScore(m map[string]float64) []string {
	type kv struct {
		key   string
		value float64
	}
	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].value > sorted[j].value })
	ids := make([]string, len(sorted))
	for i, kv := range sorted {
		ids[i] = kv.key
	}
	return ids
}

// ListByScope returns entries filtered by scope and optional userID.
func (s *FileMemoryStore) ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error) {
	scopeDir := filepath.Join(s.baseDir, string(scope))
	files, err := filepath.Glob(filepath.Join(scopeDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", scopeDir, err)
	}

	var entries []Entry
	for _, filePath := range files {
		mf, err := s.parseFile(filePath)
		if err != nil {
			slog.Warn("ListByScope: failed to parse memory file", "path", filePath, "err", err)
			continue
		}
		if userID != "" && mf.UserID != userID {
			continue
		}
		entries = append(entries, Entry{
			ID:        mf.ID,
			Scope:     MemoryScope(mf.Scope),
			UserID:    mf.UserID,
			SessionID: mf.SessionID,
			Content:   mf.Content,
			Metadata:  mf.Metadata,
			CreatedAt: mf.CreatedAt,
			UpdatedAt: mf.UpdatedAt,
		})
	}
	return entries, nil
}

// Update replaces the content of an existing memory entry with optimistic locking.
func (s *FileMemoryStore) Update(ctx context.Context, id string, content string, version int) error {
	var filePath string
	err := s.db.QueryRowContext(ctx, `SELECT file_path FROM memory_index WHERE memory_id = ?`, id).Scan(&filePath)
	if err != nil {
		return fmt.Errorf("find memory file: %w", err)
	}

	mf, err := s.parseFile(filePath)
	if err != nil {
		return fmt.Errorf("parse memory file: %w", err)
	}

	currentVersion := 1
	if mf.Metadata != nil {
		if v, ok := mf.Metadata["version"]; ok {
			if parsed, parseErr := strconv.Atoi(v); parseErr == nil {
				currentVersion = parsed
			}
		}
	}
	if currentVersion != version {
		return fmt.Errorf("version conflict: file is at version %d, caller expected %d", currentVersion, version)
	}

	// Archive old file.
	archivedDir := filepath.Join(s.baseDir, "archived")
	if err := os.MkdirAll(archivedDir, 0755); err != nil {
		return fmt.Errorf("create archived dir: %w", err)
	}
	archivedPath := filepath.Join(archivedDir, filepath.Base(filePath))
	if err := os.Rename(filePath, archivedPath); err != nil {
		return fmt.Errorf("archive old file: %w", err)
	}

	// Write updated file.
	newVersion := version + 1
	if mf.Metadata == nil {
		mf.Metadata = make(map[string]string)
	}
	mf.Metadata["version"] = strconv.Itoa(newVersion)
	mf.Content = content
	mf.UpdatedAt = time.Now()

	newFilePath := s.buildFilePath(MemoryScope(mf.Scope), mf.ID, mf.CreatedAt)
	if err := s.writeFileAtomic(newFilePath, *mf); err != nil {
		return fmt.Errorf("write updated file: %w", err)
	}

	// Update SQLite index.
	_, err = s.db.ExecContext(ctx, `
		UPDATE memory_index
		SET file_path = ?, updated_at = ?
		WHERE memory_id = ?
	`, newFilePath, mf.UpdatedAt, id)
	if err != nil {
		return fmt.Errorf("update memory_index: %w", err)
	}

	// Refresh FTS index.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, id); err != nil {
		slog.Warn("memory: FTS index update failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_fts (memory_id, content) VALUES (?, ?)`, id, content); err != nil {
		return fmt.Errorf("update memory_fts: %w", err)
	}

	// Re-embed.
	if s.embedder != nil {
		if embedding, embErr := s.embedder.Embed(ctx, content); embErr == nil {
			embBytes := serializeEmbedding(embedding)
			if _, execErr := s.db.ExecContext(ctx, `
				INSERT INTO memory_embeddings (memory_id, embedding, dimension)
				VALUES (?, ?, ?)
				ON CONFLICT(memory_id) DO UPDATE SET embedding = excluded.embedding
			`, id, embBytes, len(embedding)); execErr != nil {
				slog.Warn("memory: embedding index update failed", "err", execErr)
			}
		}
	}

	return nil
}

// Delete archives a memory entry and removes it from all indexes.
func (s *FileMemoryStore) Delete(ctx context.Context, id string) error {
	var filePath string
	err := s.db.QueryRowContext(ctx, `SELECT file_path FROM memory_index WHERE memory_id = ?`, id).Scan(&filePath)
	if err != nil {
		return fmt.Errorf("find memory file: %w", err)
	}

	archivedPath := filepath.Join(s.baseDir, "archived", filepath.Base(filePath))
	if err := os.Rename(filePath, archivedPath); err != nil {
		return fmt.Errorf("archive file: %w", err)
	}

	for _, table := range []string{"memory_index", "memory_fts", "memory_embeddings"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE memory_id = ?`, id); err != nil {
			return fmt.Errorf("delete from %s: %w", table, err)
		}
	}
	return nil
}

// SoftInvalidate marks a memory fact as superseded without deleting it.
func (s *FileMemoryStore) SoftInvalidate(ctx context.Context, id string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE memory_index SET valid_to = ?, updated_at = ? WHERE memory_id = ? AND valid_to IS NULL`,
		now, now, id)
	if err != nil {
		return fmt.Errorf("soft invalidate %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("soft invalidate %s: already invalidated or not found", id)
	}
	slog.Info("memory: fact soft-invalidated", "id", id)
	return nil
}

// RebuildIndex scans all memory files and rebuilds the SQLite index tables.
func (s *FileMemoryStore) RebuildIndex(ctx context.Context) error {
	for _, table := range []string{"memory_index", "memory_fts", "memory_embeddings"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	scopes := []string{"user", "session", "feedback", "global"}
	for _, scope := range scopes {
		scopeDir := filepath.Join(s.baseDir, scope)
		files, err := filepath.Glob(filepath.Join(scopeDir, "*.md"))
		if err != nil {
			continue
		}
		for _, filePath := range files {
			mf, err := s.parseFile(filePath)
			if err != nil {
				continue
			}
			entry := Entry{
				ID:        mf.ID,
				Scope:     MemoryScope(mf.Scope),
				UserID:    mf.UserID,
				SessionID: mf.SessionID,
				Content:   mf.Content,
				CreatedAt: mf.CreatedAt,
				UpdatedAt: mf.UpdatedAt,
			}
			if s.embedder != nil {
				embedding, err := s.embedder.Embed(ctx, mf.Content)
				if err == nil {
					entry.Embedding = embedding
				}
			}
			if err := s.syncIndex(ctx, entry, filePath); err != nil {
				return fmt.Errorf("sync index for %s: %w", filePath, err)
			}
		}
	}
	return nil
}

// --- Internal helpers ---

func (s *FileMemoryStore) buildFilePath(scope MemoryScope, id string, createdAt time.Time) string {
	dateStr := createdAt.Format("20060102")
	filename := fmt.Sprintf("memory_%s_%s.md", dateStr, id)
	return filepath.Join(s.baseDir, string(scope), filename)
}

func (s *FileMemoryStore) writeFileAtomic(path string, mf MemoryFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", path, err)
	}
	tmpPath := path + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString("---\n"); err != nil {
		return err
	}
	enc := yaml.NewEncoder(f)
	if err := enc.Encode(mf); err != nil {
		return err
	}
	_ = enc.Close()

	if _, err := f.WriteString("---\n\n"); err != nil {
		return err
	}
	if _, err := f.WriteString(mf.Content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	return os.Rename(tmpPath, path)
}

func (s *FileMemoryStore) parseFile(path string) (*MemoryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid frontmatter format")
	}
	var mf MemoryFile
	if err := yaml.Unmarshal([]byte(parts[1]), &mf); err != nil {
		return nil, err
	}
	mf.Content = strings.TrimSpace(parts[2])
	return &mf, nil
}

func (s *FileMemoryStore) syncIndex(ctx context.Context, entry Entry, filePath string) error {
	memType := "semantic"
	emotion := "neutral"
	sensitivity := "public"
	if entry.Metadata != nil {
		if t, ok := entry.Metadata["type"]; ok && t != "" {
			memType = t
		}
		if e, ok := entry.Metadata["emotion"]; ok && e != "" {
			emotion = e
		}
		if sens, ok := entry.Metadata["sensitivity"]; ok && sens != "" {
			sensitivity = sens
		}
	}

	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_index (memory_id, file_path, scope, user_id, session_id, created_at, updated_at, strength, memory_type, emotion, sensitivity, valid_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_id) DO UPDATE SET
			file_path = excluded.file_path,
			updated_at = excluded.updated_at,
			strength = excluded.strength,
			memory_type = excluded.memory_type,
			emotion = excluded.emotion,
			sensitivity = excluded.sensitivity,
			valid_from = COALESCE(memory_index.valid_from, excluded.valid_from)
	`, entry.ID, filePath, entry.Scope, entry.UserID, entry.SessionID, entry.CreatedAt, entry.UpdatedAt, 1.0, memType, emotion, sensitivity, now)
	if err != nil {
		return fmt.Errorf("update memory_index: %w", err)
	}

	// FTS5: no UPSERT, use DELETE+INSERT.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, entry.ID); err != nil {
		slog.Warn("memory: FTS index update failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_fts (memory_id, content) VALUES (?, ?)`, entry.ID, entry.Content); err != nil {
		return fmt.Errorf("update memory_fts: %w", err)
	}

	// Embedding index.
	if s.embedder != nil && len(entry.Embedding) > 0 {
		embBytes := serializeEmbedding(entry.Embedding)
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO memory_embeddings (memory_id, embedding, dimension)
			VALUES (?, ?, ?)
			ON CONFLICT(memory_id) DO UPDATE SET embedding = excluded.embedding
		`, entry.ID, embBytes, len(entry.Embedding)); err != nil {
			return fmt.Errorf("update memory_embeddings: %w", err)
		}
	}
	return nil
}

// --- Utility functions ---

func sanitizeFTS5Query(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	clean := make([]string, 0, len(words))
	for _, w := range words {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		if len(w) >= 2 {
			clean = append(clean, w)
		}
	}
	return strings.Join(clean, " ")
}

func deserializeEmbedding(buf []byte) []float32 {
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		bits := uint32(buf[i*4]) | uint32(buf[i*4+1])<<8 | uint32(buf[i*4+2])<<16 | uint32(buf[i*4+3])<<24
		vec[i] = float32(bits) / 1e6
	}
	return vec
}

func serializeEmbedding(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := uint32(0)
		if v != 0 {
			bits = uint32(v * 1e6)
		}
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
