package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FileStore implements Store using file-based storage with markdown files.
type FileStore struct {
	storageDir      string
	indexManager    *IndexManager
	metadataManager *MetadataManager
	txLog           *TransactionLog
	parser          *MarkdownParser
	chunker         *Chunker
	embedder        EmbeddingProvider
	embeddingsDB    *EmbeddingsDBImpl
	searchCache     *SearchResultCache
	cfg             MemoryConfig
	mu              sync.RWMutex
}

// NewFileStore creates a new FileStore.
func NewFileStore(storageDir string, embeddingsDB *EmbeddingsDBImpl, embedder EmbeddingProvider, cfg MemoryConfig) *FileStore {
	// Ensure storage directory exists
	os.MkdirAll(storageDir, 0755)
	os.MkdirAll(filepath.Join(storageDir, "session"), 0755)
	os.MkdirAll(filepath.Join(storageDir, "user"), 0755)
	os.MkdirAll(filepath.Join(storageDir, "global"), 0755)

	indexManager := NewIndexManager(storageDir)
	if err := indexManager.Load(); err != nil {
		slog.Error("file_store: failed to load index", "err", err)
	}

	metadataManager := NewMetadataManager(storageDir)

	flushInterval := 5 * time.Second
	txLog := NewTransactionLog(storageDir, embedder, embeddingsDB, flushInterval)

	// Initialize search cache if enabled
	var searchCache *SearchResultCache
	if cfg.EnableSearchCache {
		searchCache = NewSearchResultCache(cfg.SearchCacheSize, cfg.SearchCacheTTL)
		slog.Info("file_store: search result cache enabled", "size", cfg.SearchCacheSize, "ttl", cfg.SearchCacheTTL)
	}

	return &FileStore{
		storageDir:      storageDir,
		indexManager:    indexManager,
		metadataManager: metadataManager,
		txLog:           txLog,
		parser:          NewMarkdownParser(),
		chunker:         NewChunker(),
		embedder:        embedder,
		embeddingsDB:    embeddingsDB,
		searchCache:     searchCache,
		cfg:             cfg,
	}
}

// StartBackgroundProcessor starts the background task for processing transaction logs.
func (fs *FileStore) StartBackgroundProcessor(ctx context.Context) {
	fs.txLog.StartBackgroundProcessor(ctx)
}

// Stop stops the background processor.
func (fs *FileStore) Stop() {
	fs.txLog.Stop()
}

// Save saves a memory entry (legacy compatibility).
func (fs *FileStore) Save(ctx context.Context, entry Entry) error {
	return fs.SaveFact(ctx, entry)
}

// SaveFact saves a fact to the appropriate markdown file.
func (fs *FileStore) SaveFact(ctx context.Context, entry Entry) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Generate fact ID if not provided
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("fact_%s", uuid.New().String()[:8])
	}

	// Determine file path based on scope
	filePath, err := fs.getFilePath(entry.Scope, entry.UserID, entry.SessionID)
	if err != nil {
		return fmt.Errorf("failed to get file path: %w", err)
	}

	// Load existing document or create new one
	doc, err := fs.loadOrCreateDocument(filePath, entry.Scope, entry.UserID, entry.SessionID)
	if err != nil {
		return fmt.Errorf("failed to load document: %w", err)
	}

	// Add new fact
	now := time.Now()
	fact := MarkdownFact{
		ID:        entry.ID,
		Category:  entry.Metadata["category"],
		Version:   entry.Version,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: entry.ExpiresAt,
		Content:   entry.Content,
	}

	doc.Facts = append(doc.Facts, fact)
	doc.Frontmatter.UpdatedAt = now
	doc.Frontmatter.Version++

	// Check if chunking is needed
	data, err := fs.parser.Serialize(doc)
	if err != nil {
		return fmt.Errorf("failed to serialize document: %w", err)
	}

	shouldChunk := fs.chunker.ShouldChunk(len(doc.Facts), len(data))

	if shouldChunk {
		// Perform chunking
		if err := fs.chunkDocument(doc, entry.Scope, entry.UserID, entry.SessionID); err != nil {
			return fmt.Errorf("failed to chunk document: %w", err)
		}
	} else {
		// Write single file
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}

		// Update index
		factIDs := make([]string, len(doc.Facts))
		for i, f := range doc.Facts {
			factIDs[i] = f.ID
		}
		hash := ComputeHash(string(data))

		if err := fs.updateIndex(entry.Scope, entry.UserID, entry.SessionID, filePath, hash, factIDs); err != nil {
			slog.Error("file_store: failed to update index", "err", err)
		}
	}

	// Append to transaction log for async embedding generation
	logEntry := LogEntry{
		Timestamp: now,
		Operation: LogOpAdd,
		FactID:    entry.ID,
		FilePath:  filePath,
		Content:   entry.Content,
		Hash:      ComputeHash(entry.Content),
	}

	if err := fs.txLog.Append(logEntry); err != nil {
		slog.Error("file_store: failed to append to transaction log", "err", err)
	}

	return nil
}

// UpdateFact updates an existing fact.
func (fs *FileStore) UpdateFact(ctx context.Context, id string, content string, version int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Find the fact across all scopes
	filePath, factIndex, doc, err := fs.findFact(id)
	if err != nil {
		return fmt.Errorf("fact not found: %w", err)
	}

	// Update fact
	now := time.Now()
	doc.Facts[factIndex].Content = content
	doc.Facts[factIndex].UpdatedAt = now
	doc.Facts[factIndex].Version = version
	doc.Frontmatter.UpdatedAt = now
	doc.Frontmatter.Version++

	// Serialize and write
	data, err := fs.parser.Serialize(doc)
	if err != nil {
		return fmt.Errorf("failed to serialize document: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Update index
	factIDs := make([]string, len(doc.Facts))
	for i, f := range doc.Facts {
		factIDs[i] = f.ID
	}
	hash := ComputeHash(string(data))

	scope := doc.Frontmatter.Scope
	userID := doc.Frontmatter.UserID
	sessionID := doc.Frontmatter.SessionID

	if err := fs.updateIndex(MemoryScope(scope), userID, sessionID, filePath, hash, factIDs); err != nil {
		slog.Error("file_store: failed to update index", "err", err)
	}

	// Append to transaction log
	logEntry := LogEntry{
		Timestamp: now,
		Operation: LogOpUpdate,
		FactID:    id,
		FilePath:  filePath,
		Content:   content,
		Hash:      ComputeHash(content),
	}

	if err := fs.txLog.Append(logEntry); err != nil {
		slog.Error("file_store: failed to append to transaction log", "err", err)
	}

	return nil
}

// DeleteFact deletes a fact.
func (fs *FileStore) DeleteFact(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Find the fact
	filePath, factIndex, doc, err := fs.findFact(id)
	if err != nil {
		return fmt.Errorf("fact not found: %w", err)
	}

	// Remove fact
	doc.Facts = append(doc.Facts[:factIndex], doc.Facts[factIndex+1:]...)
	doc.Frontmatter.UpdatedAt = time.Now()
	doc.Frontmatter.Version++

	// Serialize and write
	data, err := fs.parser.Serialize(doc)
	if err != nil {
		return fmt.Errorf("failed to serialize document: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Update index
	factIDs := make([]string, len(doc.Facts))
	for i, f := range doc.Facts {
		factIDs[i] = f.ID
	}
	hash := ComputeHash(string(data))

	scope := doc.Frontmatter.Scope
	userID := doc.Frontmatter.UserID
	sessionID := doc.Frontmatter.SessionID

	if err := fs.updateIndex(MemoryScope(scope), userID, sessionID, filePath, hash, factIDs); err != nil {
		slog.Error("file_store: failed to update index", "err", err)
	}

	// Append to transaction log
	logEntry := LogEntry{
		Timestamp: time.Now(),
		Operation: LogOpDelete,
		FactID:    id,
		FilePath:  filePath,
		Content:   "",
		Hash:      "",
	}

	if err := fs.txLog.Append(logEntry); err != nil {
		slog.Error("file_store: failed to append to transaction log", "err", err)
	}

	return nil
}

// Delete is an alias for DeleteFact (legacy compatibility).
func (fs *FileStore) Delete(ctx context.Context, id string) error {
	return fs.DeleteFact(ctx, id)
}

// ListByScope lists all facts for a given scope.
func (fs *FileStore) ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var filePaths []string
	var dirs []string

	switch scope {
	case ScopeSession:
		// List all session files
		sessionIDs := fs.indexManager.ListSessionIDs()
		for _, sessionID := range sessionIDs {
			if file, ok := fs.indexManager.GetSessionFile(sessionID); ok {
				filePaths = append(filePaths, file)
				dirs = append(dirs, filepath.Dir(file))
			}
		}
	case ScopeUser:
		if file, ok := fs.indexManager.GetUserFile(userID); ok {
			filePaths = append(filePaths, file)
			dirs = append(dirs, filepath.Dir(file))
		}
	case ScopeGlobal:
		if file, ok := fs.indexManager.GetGlobalFile(); ok {
			filePaths = append(filePaths, file)
			dirs = append(dirs, filepath.Dir(file))
		}
	}

	var entries []Entry
	for i, dir := range dirs {
		// Check if this is chunked storage
		metadata, err := fs.metadataManager.Load(dir)
		if err == nil && len(metadata.Chunks) > 0 {
			// Load from chunks
			doc, err := fs.loadDocumentFromChunks(metadata)
			if err != nil {
				slog.Error("file_store: failed to load chunked document", "dir", dir, "err", err)
				continue
			}

			for _, fact := range doc.Facts {
				entry := Entry{
					ID:        fact.ID,
					SessionID: doc.Frontmatter.SessionID,
					UserID:    doc.Frontmatter.UserID,
					Scope:     MemoryScope(doc.Frontmatter.Scope),
					Content:   fact.Content,
					Metadata: map[string]string{
						"category": fact.Category,
					},
					Version:   fact.Version,
					ExpiresAt: fact.ExpiresAt,
					CreatedAt: fact.CreatedAt,
					UpdatedAt: fact.UpdatedAt,
				}
				entries = append(entries, entry)
			}
		} else {
			// Load from single file
			filePath := filePaths[i]
			doc, err := fs.loadDocument(filePath)
			if err != nil {
				slog.Error("file_store: failed to load document", "path", filePath, "err", err)
				continue
			}

			for _, fact := range doc.Facts {
				entry := Entry{
					ID:        fact.ID,
					SessionID: doc.Frontmatter.SessionID,
					UserID:    doc.Frontmatter.UserID,
					Scope:     MemoryScope(doc.Frontmatter.Scope),
					Content:   fact.Content,
					Metadata: map[string]string{
						"category": fact.Category,
					},
					Version:   fact.Version,
					ExpiresAt: fact.ExpiresAt,
					CreatedAt: fact.CreatedAt,
					UpdatedAt: fact.UpdatedAt,
				}
				entries = append(entries, entry)
			}
		}
	}

	return entries, nil
}

// Search performs hybrid search (BM25 + vector search with RRF fusion).
func (fs *FileStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	// Check cache first
	if fs.searchCache != nil {
		if cached, ok := fs.searchCache.Get(query); ok {
			slog.Debug("file_store: cache hit", "query", query.Text)
			return cached, nil
		}
	}

	// Generate query embedding
	var queryEmbedding []float32
	var err error
	if fs.embedder != nil && query.Text != "" {
		queryEmbedding, err = fs.embedder.Embed(ctx, query.Text)
		if err != nil {
			slog.Warn("file_store: failed to generate query embedding", "err", err)
		}
	}

	// Use provided embedding if available
	if len(query.Embedding) > 0 {
		queryEmbedding = query.Embedding
	}

	// Determine scopes to search
	scopes := query.Scopes
	if len(scopes) == 0 {
		scopes = []MemoryScope{ScopeSession, ScopeUser, ScopeGlobal}
	}

	// Set default limit
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	// Parallel search: BM25 + Vector
	var bm25Results []FTS5SearchResult
	var vectorResults []VectorSearchResult
	var wg sync.WaitGroup
	var bm25Err, vectorErr error

	// BM25 search
	if query.Text != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bm25Results, bm25Err = fs.embeddingsDB.FTS5Search(ctx, query.Text, limit*2, scopes, query.UserID, query.SessionID)
		}()
	}

	// Vector search
	if len(queryEmbedding) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectorResults, vectorErr = fs.embeddingsDB.VectorSearch(ctx, queryEmbedding, limit*2, scopes, query.UserID, query.SessionID)
		}()
	}

	wg.Wait()

	// Check for errors
	if bm25Err != nil {
		slog.Warn("file_store: BM25 search failed", "err", bm25Err)
	}
	if vectorErr != nil {
		slog.Warn("file_store: vector search failed", "err", vectorErr)
	}

	// Perform RRF fusion
	fusedResults := fs.rrfFusion(bm25Results, vectorResults, limit)

	// Hydrate results from markdown files
	hydratedResults, err := fs.hydrateResults(ctx, fusedResults)
	if err != nil {
		return nil, fmt.Errorf("failed to hydrate results: %w", err)
	}

	// Cache results
	if fs.searchCache != nil {
		fs.searchCache.Set(query, hydratedResults)
	}

	return hydratedResults, nil
}

// rrfFusion performs Reciprocal Rank Fusion on BM25 and vector search results.
func (fs *FileStore) rrfFusion(bm25Results []FTS5SearchResult, vectorResults []VectorSearchResult, limit int) []fusedResult {
	const k = 60 // RRF constant

	// Get weights from config
	bm25Weight := fs.cfg.BM25Weight
	vectorWeight := fs.cfg.VectorWeight
	if bm25Weight == 0 && vectorWeight == 0 {
		bm25Weight = 0.4
		vectorWeight = 0.6
	}

	// Build score map
	scoreMap := make(map[string]float64)
	filePathMap := make(map[string]string)

	// Add BM25 scores
	for rank, result := range bm25Results {
		score := bm25Weight / float64(rank+k)
		scoreMap[result.FactID] += score
		filePathMap[result.FactID] = result.FilePath
	}

	// Add vector scores
	for rank, result := range vectorResults {
		score := vectorWeight / float64(rank+k)
		scoreMap[result.FactID] += score
		filePathMap[result.FactID] = result.FilePath
	}

	// Convert to slice and sort
	var results []fusedResult
	for factID, score := range scoreMap {
		results = append(results, fusedResult{
			FactID:   factID,
			FilePath: filePathMap[factID],
			Score:    score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// hydrateResults loads full content from markdown files.
func (fs *FileStore) hydrateResults(ctx context.Context, fusedResults []fusedResult) ([]SearchResult, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var results []SearchResult

	// Group by file path to minimize file reads
	fileMap := make(map[string][]fusedResult)
	for _, fr := range fusedResults {
		fileMap[fr.FilePath] = append(fileMap[fr.FilePath], fr)
	}

	// Load each file once and extract relevant facts
	for filePath, frs := range fileMap {
		doc, err := fs.loadDocument(filePath)
		if err != nil {
			slog.Warn("file_store: failed to load document for hydration", "path", filePath, "err", err)
			continue
		}

		// Build fact map for quick lookup
		factMap := make(map[string]MarkdownFact)
		for _, fact := range doc.Facts {
			factMap[fact.ID] = fact
		}

		// Hydrate each result
		for _, fr := range frs {
			fact, ok := factMap[fr.FactID]
			if !ok {
				continue
			}

			entry := Entry{
				ID:        fact.ID,
				SessionID: doc.Frontmatter.SessionID,
				UserID:    doc.Frontmatter.UserID,
				Scope:     MemoryScope(doc.Frontmatter.Scope),
				Content:   fact.Content,
				Metadata: map[string]string{
					"category": fact.Category,
				},
				Version:   fact.Version,
				ExpiresAt: fact.ExpiresAt,
				CreatedAt: fact.CreatedAt,
				UpdatedAt: fact.UpdatedAt,
			}

			results = append(results, SearchResult{
				Entry: entry,
				Score: fr.Score,
			})
		}
	}

	// Sort by score (maintain RRF order)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// fusedResult is an intermediate result from RRF fusion.
type fusedResult struct {
	FactID   string
	FilePath string
	Score    float64
}

// getFilePath returns the file path for a given scope.
func (fs *FileStore) getFilePath(scope MemoryScope, userID, sessionID string) (string, error) {
	switch scope {
	case ScopeSession:
		if sessionID == "" {
			return "", fmt.Errorf("session_id required for session scope")
		}
		dir := filepath.Join(fs.storageDir, "session", sessionID)
		os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "facts.md"), nil
	case ScopeUser:
		if userID == "" {
			return "", fmt.Errorf("user_id required for user scope")
		}
		dir := filepath.Join(fs.storageDir, "user", userID)
		os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "facts.md"), nil
	case ScopeGlobal:
		return filepath.Join(fs.storageDir, "global", "facts.md"), nil
	default:
		return "", fmt.Errorf("unknown scope: %s", scope)
	}
}

// chunkDocument splits a document into multiple chunk files.
func (fs *FileStore) chunkDocument(doc *MarkdownDocument, scope MemoryScope, userID, sessionID string) error {
	// Get directory for this scope
	dir := filepath.Dir(fs.mustGetFilePath(scope, userID, sessionID))

	// Load or create metadata
	metadata, err := fs.metadataManager.Load(dir)
	if err != nil {
		return fmt.Errorf("failed to load metadata: %w", err)
	}

	// Update metadata scope info
	metadata.Scope = string(scope)
	metadata.UserID = userID
	metadata.SessionID = sessionID

	// Calculate target facts per chunk
	targetFactsPerChunk := ChunkThreshold / 2 // ~100 facts per chunk

	// Split facts into chunks
	factChunks := fs.chunker.SplitFacts(doc.Facts, targetFactsPerChunk)

	// Get chunk directory
	chunkDir, err := fs.metadataManager.GetChunkDir(scope, userID, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get chunk dir: %w", err)
	}

	// Ensure chunk directory exists
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return fmt.Errorf("failed to create chunk dir: %w", err)
	}

	// Clear existing chunks
	metadata.Chunks = []ChunkInfo{}
	metadata.TotalFacts = 0

	// Write each chunk
	for i, factChunk := range factChunks {
		chunkID := fmt.Sprintf("chunk_%03d", i+1)
		chunkFile := filepath.Join(chunkDir, fmt.Sprintf("%s.md", chunkID))

		// Create chunk document
		chunkDoc := &MarkdownDocument{
			Frontmatter: doc.Frontmatter,
			Facts:       factChunk,
		}
		chunkDoc.Frontmatter.Version = i + 1

		// Serialize and write
		data, err := fs.parser.Serialize(chunkDoc)
		if err != nil {
			return fmt.Errorf("failed to serialize chunk %s: %w", chunkID, err)
		}

		if err := os.WriteFile(chunkFile, data, 0644); err != nil {
			return fmt.Errorf("failed to write chunk %s: %w", chunkID, err)
		}

		// Build fact range
		var factRange []string
		if len(factChunk) > 0 {
			factRange = []string{factChunk[0].ID, factChunk[len(factChunk)-1].ID}
		}

		// Add chunk info to metadata
		chunkInfo := ChunkInfo{
			ChunkID:   chunkID,
			File:      chunkFile,
			FactRange: factRange,
			FactCount: len(factChunk),
			SizeBytes: len(data),
			CreatedAt: time.Now(),
		}
		fs.metadataManager.AddChunk(metadata, chunkInfo)

		slog.Info("file_store: created chunk", "chunk_id", chunkID, "facts", len(factChunk), "size", len(data))
	}

	// Save metadata
	if err := fs.metadataManager.Save(dir, metadata); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	// Update index with all fact IDs
	allFactIDs := make([]string, 0, len(doc.Facts))
	for _, fact := range doc.Facts {
		allFactIDs = append(allFactIDs, fact.ID)
	}

	// Use first chunk file as representative in index
	if len(metadata.Chunks) > 0 {
		hash := ComputeHash(string(allFactIDs[0])) // Use first fact ID as hash
		if err := fs.updateIndex(scope, userID, sessionID, metadata.Chunks[0].File, hash, allFactIDs); err != nil {
			slog.Error("file_store: failed to update index", "err", err)
		}
	}

	slog.Info("file_store: document chunked", "total_chunks", len(factChunks), "total_facts", len(doc.Facts))
	return nil
}

// loadOrCreateDocument loads an existing document or creates a new one.
// It handles both single-file and chunked storage.
func (fs *FileStore) loadOrCreateDocument(filePath string, scope MemoryScope, userID, sessionID string) (*MarkdownDocument, error) {
	dir := filepath.Dir(filePath)

	// Check if metadata exists (chunked storage)
	metadata, err := fs.metadataManager.Load(dir)
	if err == nil && len(metadata.Chunks) > 0 {
		// Load from chunks
		return fs.loadDocumentFromChunks(metadata)
	}

	// Check if single file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Create new document
		now := time.Now()
		return &MarkdownDocument{
			Frontmatter: MarkdownFrontmatter{
				Scope:     string(scope),
				UserID:    userID,
				SessionID: sessionID,
				CreatedAt: now,
				UpdatedAt: now,
				Version:   1,
			},
			Facts: []MarkdownFact{},
		}, nil
	}

	// Load single file
	return fs.loadDocument(filePath)
}

// loadDocumentFromChunks loads a document from multiple chunk files.
func (fs *FileStore) loadDocumentFromChunks(metadata *ChunkMetadata) (*MarkdownDocument, error) {
	if len(metadata.Chunks) == 0 {
		return nil, fmt.Errorf("no chunks in metadata")
	}

	// Load first chunk to get frontmatter
	firstChunk, err := fs.loadDocument(metadata.Chunks[0].File)
	if err != nil {
		return nil, fmt.Errorf("failed to load first chunk: %w", err)
	}

	doc := &MarkdownDocument{
		Frontmatter: firstChunk.Frontmatter,
		Facts:       []MarkdownFact{},
	}

	// Load all chunks
	for _, chunkInfo := range metadata.Chunks {
		chunkDoc, err := fs.loadDocument(chunkInfo.File)
		if err != nil {
			slog.Warn("file_store: failed to load chunk", "file", chunkInfo.File, "err", err)
			continue
		}
		doc.Facts = append(doc.Facts, chunkDoc.Facts...)
	}

	return doc, nil
}

// mustGetFilePath is like getFilePath but panics on error (for internal use).
func (fs *FileStore) mustGetFilePath(scope MemoryScope, userID, sessionID string) string {
	path, err := fs.getFilePath(scope, userID, sessionID)
	if err != nil {
		panic(err)
	}
	return path
}

// loadDocument loads a document from disk.
func (fs *FileStore) loadDocument(filePath string) (*MarkdownDocument, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	doc, err := fs.parser.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	return doc, nil
}

// findFact finds a fact by ID across all scopes.
func (fs *FileStore) findFact(id string) (string, int, *MarkdownDocument, error) {
	// Search in all scopes
	scopes := []struct {
		scope   MemoryScope
		ids     []string
		getFile func(string) (string, bool)
	}{
		{ScopeSession, fs.indexManager.ListSessionIDs(), fs.indexManager.GetSessionFile},
		{ScopeUser, fs.indexManager.ListUserIDs(), fs.indexManager.GetUserFile},
	}

	for _, s := range scopes {
		for _, scopeID := range s.ids {
			filePath, ok := s.getFile(scopeID)
			if !ok {
				continue
			}

			dir := filepath.Dir(filePath)

			// Check if chunked storage
			metadata, err := fs.metadataManager.Load(dir)
			if err == nil && len(metadata.Chunks) > 0 {
				// Search in chunks
				doc, err := fs.loadDocumentFromChunks(metadata)
				if err != nil {
					continue
				}

				for i, fact := range doc.Facts {
					if fact.ID == id {
						// Return the first chunk file as representative
						return metadata.Chunks[0].File, i, doc, nil
					}
				}
			} else {
				// Search in single file
				doc, err := fs.loadDocument(filePath)
				if err != nil {
					continue
				}

				for i, fact := range doc.Facts {
					if fact.ID == id {
						return filePath, i, doc, nil
					}
				}
			}
		}
	}

	// Check global scope
	if filePath, ok := fs.indexManager.GetGlobalFile(); ok {
		dir := filepath.Dir(filePath)

		// Check if chunked storage
		metadata, err := fs.metadataManager.Load(dir)
		if err == nil && len(metadata.Chunks) > 0 {
			doc, err := fs.loadDocumentFromChunks(metadata)
			if err == nil {
				for i, fact := range doc.Facts {
					if fact.ID == id {
						return metadata.Chunks[0].File, i, doc, nil
					}
				}
			}
		} else {
			doc, err := fs.loadDocument(filePath)
			if err == nil {
				for i, fact := range doc.Facts {
					if fact.ID == id {
						return filePath, i, doc, nil
					}
				}
			}
		}
	}

	return "", 0, nil, fmt.Errorf("fact not found: %s", id)
}

// updateIndex updates the index for a given scope.
func (fs *FileStore) updateIndex(scope MemoryScope, userID, sessionID, filePath, hash string, factIDs []string) error {
	switch scope {
	case ScopeSession:
		return fs.indexManager.UpdateSession(sessionID, filePath, hash, factIDs)
	case ScopeUser:
		return fs.indexManager.UpdateUser(userID, filePath, hash, factIDs)
	case ScopeGlobal:
		return fs.indexManager.UpdateGlobal(filePath, hash, factIDs)
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}
