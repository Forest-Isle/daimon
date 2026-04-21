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

	"gopkg.in/yaml.v3"
)

// FileMemoryStore implements Store with Markdown files as primary storage.
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
	Type         string            `yaml:"type,omitempty"`        // episodic, semantic, procedural, reflection, summary, profile
	Importance   int               `yaml:"importance,omitempty"`  // 1-10
	Emotion      string            `yaml:"emotion,omitempty"`     // positive, negative, neutral
	Sensitivity  string            `yaml:"sensitivity,omitempty"` // public, private, secret
	RelatedTo    string            `yaml:"related_to,omitempty"`
	PromotedFrom string            `yaml:"promoted_from,omitempty"`
	PromotedAt   *time.Time        `yaml:"promoted_at,omitempty"`
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
	if err := store.checkIndexStaleness(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileMemoryStore) checkIndexStaleness() error {
	ctx := context.Background()

	// Get newest file mtime
	var newestMtime time.Time
	scopes := []string{"user", "session", "feedback", "global"}
	for _, scope := range scopes {
		scopeDir := filepath.Join(s.baseDir, scope)
		files, err := filepath.Glob(filepath.Join(scopeDir, "*.md"))
		if err != nil {
			continue
		}
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			if info.ModTime().After(newestMtime) {
				newestMtime = info.ModTime()
			}
		}
	}

	// Get index last update time
	var indexTimeStr sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MAX(updated_at) FROM memory_index`).Scan(&indexTimeStr)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check index time: %w", err)
	}

	// Parse timestamp if present
	var indexTime time.Time
	if indexTimeStr.Valid {
		// SQLite TIMESTAMP format: "YYYY-MM-DD HH:MM:SS+TZ"
		indexTime, err = time.Parse("2006-01-02 15:04:05-07:00", indexTimeStr.String)
		if err != nil {
			return fmt.Errorf("parse index time: %w", err)
		}
	}

	// Rebuild if index is stale (>24h older than newest file) or empty
	if !indexTimeStr.Valid || newestMtime.Sub(indexTime) > 24*time.Hour {
		return s.RebuildIndex(ctx)
	}

	return nil
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

	// Populate MemoryFile fields from entry metadata
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

// indexResult holds a row from memory_index used during search.
type indexResult struct {
	id       string
	filePath string
	strength float64
}

func (s *FileMemoryStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	// Step 1: Parse MEMORY.md for quick filtering
	idx := NewMemoryIndex(s.baseDir)
	indexEntries, err := idx.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse MEMORY.md: %w", err)
	}

	// Filter by scope if specified
	var candidateIDs []string
	if len(query.Scopes) > 0 {
		for _, scope := range query.Scopes {
			entries := indexEntries[string(scope)]
			for _, entry := range entries {
				// Extract ID from file path
				base := filepath.Base(entry.FilePath)
				parts := strings.Split(base, "_")
				if len(parts) >= 3 {
					id := strings.TrimSuffix(parts[2], ".md")
					candidateIDs = append(candidateIDs, id)
				}
			}
		}
	}

	// Step 2: Query memory_index for metadata filtering
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
	if len(candidateIDs) > 0 {
		placeholders := strings.Repeat("?,", len(candidateIDs))
		placeholders = placeholders[:len(placeholders)-1]
		whereClause = append(whereClause, fmt.Sprintf("memory_id IN (%s)", placeholders))
		for _, id := range candidateIDs {
			args = append(args, id)
		}
	}

	// TypeFilter: filter by memory type (e.g., "summary")
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

	// Sensitivity filtering: exclude secret memories from automated searches
	whereClause = append(whereClause, "sensitivity != 'secret'")

	// If no user filter, also exclude private memories
	if query.UserID == "" {
		whereClause = append(whereClause, "sensitivity != 'private'")
	}

	sqlQuery := fmt.Sprintf("SELECT memory_id, file_path, strength FROM memory_index WHERE %s", strings.Join(whereClause, " AND "))
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query memory_index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var indexResults []indexResult
	for rows.Next() {
		var r indexResult
		if err := rows.Scan(&r.id, &r.filePath, &r.strength); err != nil {
			return nil, err
		}
		indexResults = append(indexResults, r)
	}

	if len(indexResults) == 0 {
		return []SearchResult{}, nil
	}

	// Step 3: Perform hybrid search (BM25 + vector) with RRF fusion
	results, err := s.hybridSearch(ctx, query, indexResults)
	if err != nil {
		return nil, err
	}

	// Step 4: Read Markdown files for top-k results
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if len(results) > limit {
		results = results[:limit]
	}

	for i := range results {
		filePath := string(results[i].Entry.Scope)

		mf, err := s.parseFile(filePath)
		if err != nil {
			continue
		}
		results[i].Entry.Content = mf.Content
		results[i].Entry.Metadata = mf.Metadata
		results[i].Entry.Scope = MemoryScope(mf.Scope)
		results[i].Entry.UserID = mf.UserID
		results[i].Entry.SessionID = mf.SessionID
		results[i].Entry.CreatedAt = mf.CreatedAt
		results[i].Entry.UpdatedAt = mf.UpdatedAt

		// Track access
		if err := s.trackAccess(ctx, results[i].Entry.ID, filePath, mf); err != nil {
			_ = err // Log but don't fail
		}
	}

	return results, nil
}

func (s *FileMemoryStore) trackAccess(ctx context.Context, id, filePath string, mf *MemoryFile) error {
	now := time.Now()
	mf.LastAccessed = &now

	// Update file frontmatter
	if err := s.writeFileAtomic(filePath, *mf); err != nil {
		return err
	}

	// Update memory_index strength cache
	_, err := s.db.ExecContext(ctx, `UPDATE memory_index SET strength = ? WHERE memory_id = ?`, mf.Strength, id)
	return err
}

func (s *FileMemoryStore) hybridSearch(ctx context.Context, query SearchQuery, indexResults []indexResult) ([]SearchResult, error) {
	idMap := make(map[string]indexResult)
	for _, r := range indexResults {
		idMap[r.id] = r
	}

	// BM25 search via FTS5
	bm25Results := make(map[string]float64)
	if query.Text != "" {
		rows, err := s.db.QueryContext(ctx, `
			SELECT memory_id, rank
			FROM memory_fts
			WHERE memory_fts MATCH ?
			ORDER BY rank
			LIMIT 100
		`, query.Text)
		if err == nil {
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

	// Vector search
	vectorResults := make(map[string]float64)
	if len(query.Embedding) > 0 {
		// Simple cosine similarity (placeholder for HNSW)
		for id := range idMap {
			var embBytes []byte
			err := s.db.QueryRowContext(ctx, `SELECT embedding FROM memory_embeddings WHERE memory_id = ?`, id).Scan(&embBytes)
			if err == nil {
				emb := deserializeEmbedding(embBytes)
				sim := cosineSimilarity(query.Embedding, emb)
				vectorResults[id] = sim
			}
		}
	}

	// RRF fusion
	results := s.rrfFusion(bm25Results, vectorResults, idMap)
	return results, nil
}

func deserializeEmbedding(buf []byte) []float32 {
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		bits := uint32(buf[i*4]) | uint32(buf[i*4+1])<<8 | uint32(buf[i*4+2])<<16 | uint32(buf[i*4+3])<<24
		vec[i] = float32(bits) / 1e6
	}
	return vec
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

func (s *FileMemoryStore) rrfFusion(bm25Results, vectorResults map[string]float64, idMap map[string]indexResult) []SearchResult {
	const k = 60.0
	scores := make(map[string]float64)

	// RRF for BM25
	bm25Sorted := sortByScore(bm25Results)
	for rank, id := range bm25Sorted {
		scores[id] += 1.0 / (k + float64(rank+1))
	}

	// RRF for vector
	vectorSorted := sortByScore(vectorResults)
	for rank, id := range vectorSorted {
		scores[id] += 1.0 / (k + float64(rank+1))
	}

	// Apply strength weighting: final_score = relevance × 0.7 + strength × 0.3
	for id, score := range scores {
		if r, ok := idMap[id]; ok {
			scores[id] = score*0.7 + r.strength*0.3
		}
	}

	// Sort by final score
	finalSorted := sortByScore(scores)
	results := make([]SearchResult, 0, len(finalSorted))
	for _, id := range finalSorted {
		if r, ok := idMap[id]; ok {
			results = append(results, SearchResult{
				Entry: Entry{
					ID:        id,
					Scope:     MemoryScope(r.filePath),
					CreatedAt: time.Now(),
				},
				Score: scores[id],
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
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].value > sorted[j].value
	})
	ids := make([]string, len(sorted))
	for i, kv := range sorted {
		ids[i] = kv.key
	}
	return ids
}

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

func (s *FileMemoryStore) Update(ctx context.Context, id string, content string, version int) error {
	// Look up the file path from the index.
	var filePath string
	err := s.db.QueryRowContext(ctx, `SELECT file_path FROM memory_index WHERE memory_id = ?`, id).Scan(&filePath)
	if err != nil {
		return fmt.Errorf("find memory file: %w", err)
	}

	// Read the current file.
	mf, err := s.parseFile(filePath)
	if err != nil {
		return fmt.Errorf("parse memory file: %w", err)
	}

	// Optimistic lock: caller must supply the current version.
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

	// Archive the old file.
	archivedDir := filepath.Join(s.baseDir, "archived")
	if err := os.MkdirAll(archivedDir, 0755); err != nil {
		return fmt.Errorf("create archived dir: %w", err)
	}
	archivedPath := filepath.Join(archivedDir, filepath.Base(filePath))
	if err := os.Rename(filePath, archivedPath); err != nil {
		return fmt.Errorf("archive old file: %w", err)
	}

	// Write updated file with incremented version.
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

	// Update the SQLite index.
	_, err = s.db.ExecContext(ctx, `
		UPDATE memory_index
		SET file_path = ?, updated_at = ?
		WHERE memory_id = ?
	`, newFilePath, mf.UpdatedAt, id)
	if err != nil {
		return fmt.Errorf("update memory_index: %w", err)
	}

	// Refresh the FTS index (FTS5 has no UPSERT, so delete + insert).
	_, _ = s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, id)
	_, err = s.db.ExecContext(ctx, `INSERT INTO memory_fts (memory_id, content) VALUES (?, ?)`, id, content)
	if err != nil {
		return fmt.Errorf("update memory_fts: %w", err)
	}

	// Re-embed if an embedder is configured.
	if s.embedder != nil {
		if embedding, embErr := s.embedder.Embed(ctx, content); embErr == nil {
			embBytes := serializeEmbedding(embedding)
			_, _ = s.db.ExecContext(ctx, `
				INSERT INTO memory_embeddings (memory_id, embedding, dimension)
				VALUES (?, ?, ?)
				ON CONFLICT(memory_id) DO UPDATE SET embedding = excluded.embedding
			`, id, embBytes, len(embedding))
		}
	}

	return nil
}

func (s *FileMemoryStore) Delete(ctx context.Context, id string) error {
	// Find file path from index
	var filePath string
	err := s.db.QueryRowContext(ctx, `SELECT file_path FROM memory_index WHERE memory_id = ?`, id).Scan(&filePath)
	if err != nil {
		return fmt.Errorf("find memory file: %w", err)
	}

	// Move file to archived/
	archivedPath := filepath.Join(s.baseDir, "archived", filepath.Base(filePath))
	if err := os.Rename(filePath, archivedPath); err != nil {
		return fmt.Errorf("archive file: %w", err)
	}

	// Remove from all index tables
	_, err = s.db.ExecContext(ctx, `DELETE FROM memory_index WHERE memory_id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete from memory_index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete from memory_fts: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE memory_id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete from memory_embeddings: %w", err)
	}

	return nil
}

func (s *FileMemoryStore) buildFilePath(scope MemoryScope, id string, createdAt time.Time) string {
	category := "memory"
	dateStr := createdAt.Format("20060102")
	filename := fmt.Sprintf("%s_%s_%s.md", category, dateStr, id)
	return filepath.Join(s.baseDir, string(scope), filename)
}

func (s *FileMemoryStore) writeFileAtomic(path string, mf MemoryFile) error {
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
	_ = f.Close()

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
	// Update MEMORY.md index
	idx := NewMemoryIndex(s.baseDir)
	title := fmt.Sprintf("%s_%s", entry.ID, entry.CreatedAt.Format("20060102"))
	summary := entry.Content
	if len(summary) > 80 {
		summary = summary[:77] + "..."
	}
	if err := idx.AddEntry(string(entry.Scope), filePath, title, summary); err != nil {
		return fmt.Errorf("update MEMORY.md: %w", err)
	}

	// Update memory_index table
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
		if s, ok := entry.Metadata["sensitivity"]; ok && s != "" {
			sensitivity = s
		}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_index (memory_id, file_path, scope, user_id, session_id, created_at, updated_at, strength, memory_type, emotion, sensitivity)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(memory_id) DO UPDATE SET
			file_path = excluded.file_path,
			updated_at = excluded.updated_at,
			strength = excluded.strength,
			memory_type = excluded.memory_type,
			emotion = excluded.emotion,
			sensitivity = excluded.sensitivity
	`, entry.ID, filePath, entry.Scope, entry.UserID, entry.SessionID, entry.CreatedAt, entry.UpdatedAt, 1.0, memType, emotion, sensitivity)
	if err != nil {
		return fmt.Errorf("update memory_index: %w", err)
	}

	// Update memory_fts table (FTS5 doesn't support UPSERT, use DELETE+INSERT)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, entry.ID)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_fts (memory_id, content) VALUES (?, ?)
	`, entry.ID, entry.Content)
	if err != nil {
		return fmt.Errorf("update memory_fts: %w", err)
	}

	// Update memory_embeddings table (async via embedder)
	if s.embedder != nil && len(entry.Embedding) > 0 {
		embBytes := serializeEmbedding(entry.Embedding)
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO memory_embeddings (memory_id, embedding, dimension)
			VALUES (?, ?, ?)
			ON CONFLICT(memory_id) DO UPDATE SET embedding = excluded.embedding
		`, entry.ID, embBytes, len(entry.Embedding))
		if err != nil {
			return fmt.Errorf("update memory_embeddings: %w", err)
		}
	}

	return nil
}

// RebuildIndex scans all memory files and rebuilds the SQLite index.
func (s *FileMemoryStore) RebuildIndex(ctx context.Context) error {
	// Clear existing index
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_index`); err != nil {
		return fmt.Errorf("clear memory_index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_fts`); err != nil {
		return fmt.Errorf("clear memory_fts: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_embeddings`); err != nil {
		return fmt.Errorf("clear memory_embeddings: %w", err)
	}

	// Scan all scope directories
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

			// Generate embedding if needed
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

	// Rebuild MEMORY.md
	idx := NewMemoryIndex(s.baseDir)
	return idx.Rebuild()
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
