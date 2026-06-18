package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

var defaultCodebaseIndexExcludes = map[string]struct{}{
	".git":         {},
	".claude":      {}, // worktrees/commands/skills — not project source, and worktrees are whole-repo copies
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"data":         {},
}

// EmbeddingProvider generates embeddings for code chunks and queries.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// CodeChunk is a ranked chunk returned by semantic code search.
type CodeChunk struct {
	FilePath  string  `json:"file_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// IndexConfig controls how files are chunked and embedded.
type IndexConfig struct {
	ChunkSize      int    `json:"chunk_size"`
	Overlap        int    `json:"overlap"`
	EmbeddingModel string `json:"embedding_model"`
	Concurrency    int    `json:"concurrency"`
}

// CodebaseIndex maintains an in-memory semantic index for repository code.
type CodebaseIndex struct {
	provider   EmbeddingProvider
	config     IndexConfig
	mu         sync.RWMutex
	chunkStore map[string][]indexedChunk
	fileHashes map[string]string
}

type indexedChunk struct {
	CodeChunk
	Embedding []float64
}

// noopEmbeddingProvider is a no-op provider used when embeddings are unavailable.
type noopEmbeddingProvider struct{}

func (noopEmbeddingProvider) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, nil
}

// NewCodebaseIndex creates a semantic code index with sane defaults.
func NewCodebaseIndex(provider EmbeddingProvider, cfg IndexConfig) *CodebaseIndex {
	if provider == nil {
		provider = noopEmbeddingProvider{}
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 50
	}
	if cfg.Overlap < 0 {
		cfg.Overlap = 0
	}
	if cfg.Overlap >= cfg.ChunkSize {
		cfg.Overlap = cfg.ChunkSize - 1
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	return &CodebaseIndex{
		provider:   provider,
		config:     cfg,
		chunkStore: make(map[string][]indexedChunk),
		fileHashes: make(map[string]string),
	}
}

func (idx *CodebaseIndex) concurrency() int {
	if idx.config.Concurrency > 0 {
		return idx.config.Concurrency
	}
	return 8
}

// IsAvailable reports whether the index has a real embedding provider configured.
func (idx *CodebaseIndex) IsAvailable() bool {
	_, noop := idx.provider.(noopEmbeddingProvider)
	return idx != nil && !noop
}

// IndexFile indexes a single file if its contents changed since the last run.
func (idx *CodebaseIndex) IndexFile(ctx context.Context, path string) error {
	return idx.IndexFileContext(ctx, path)
}

// IndexFileContext indexes a single file using the provided context.
func (idx *CodebaseIndex) IndexFileContext(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	idx.mu.RLock()
	if idx.fileHashes[path] == hashHex {
		idx.mu.RUnlock()
		return nil
	}
	idx.mu.RUnlock()

	chunks, err := idx.buildChunks(ctx, path, string(data))
	if err != nil {
		return err
	}

	idx.mu.Lock()
	idx.chunkStore[path] = chunks
	idx.fileHashes[path] = hashHex
	idx.mu.Unlock()
	return nil
}

// IndexDirectory recursively indexes source files under dir.
func (idx *CodebaseIndex) IndexDirectory(ctx context.Context, dir string) error {
	return idx.IndexDirectoryContext(ctx, dir)
}

// IndexDirectoryContext recursively indexes source files under dir with context.
func (idx *CodebaseIndex) IndexDirectoryContext(ctx context.Context, dir string) error {
	var paths []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := defaultCodebaseIndexExcludes[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !shouldIndexFile(path) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(idx.concurrency())
	for _, p := range paths {
		p := p
		g.Go(func() error {
			if err := idx.IndexFileContext(gctx, p); err != nil {
				if gctx.Err() != nil {
					return err
				}
				// Per-file tolerance: a single embed failure (e.g. a provider timeout)
				// must not abort indexing the whole tree. Skip the file and continue.
				slog.Warn("codebase index: skip file", "path", p, "err", err)
			}
			return nil
		})
	}
	return g.Wait()
}

// Search runs a semantic search and returns the top ranked chunks.
func (idx *CodebaseIndex) Search(ctx context.Context, query string, topK int) ([]CodeChunk, error) {
	if topK <= 0 {
		topK = 5
	}

	queryEmbedding, err := idx.provider.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(queryEmbedding) == 0 {
		return nil, nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	results := make([]CodeChunk, 0, topK)
	for _, chunks := range idx.chunkStore {
		for _, chunk := range chunks {
			if len(chunk.Embedding) == 0 {
				continue
			}
			score := cosineSimilarity(queryEmbedding, chunk.Embedding)
			if score <= 0 {
				continue
			}
			match := chunk.CodeChunk
			match.Score = score
			results = append(results, match)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].FilePath == results[j].FilePath {
				return results[i].StartLine < results[j].StartLine
			}
			return results[i].FilePath < results[j].FilePath
		}
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func (idx *CodebaseIndex) buildChunks(ctx context.Context, path, content string) ([]indexedChunk, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, nil
	}

	step := idx.config.ChunkSize - idx.config.Overlap
	if step <= 0 {
		step = idx.config.ChunkSize
	}

	chunks := make([]indexedChunk, 0, (len(lines)/step)+1)
	for start := 0; start < len(lines); start += step {
		end := start + idx.config.ChunkSize
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.Join(lines[start:end], "\n")
		embedding, err := idx.provider.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed %s:%d-%d: %w", path, start+1, end, err)
		}
		chunks = append(chunks, indexedChunk{
			CodeChunk: CodeChunk{
				FilePath:  path,
				StartLine: start + 1,
				EndLine:   end,
				Content:   text,
			},
			Embedding: embedding,
		})
		if end == len(lines) {
			break
		}
	}
	return chunks, nil
}

func shouldIndexFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".js", ".ts", ".tsx", ".jsx", ".py", ".java", ".rs", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".php", ".rb", ".swift", ".kt", ".kts", ".scala", ".sh", ".sql", ".yaml", ".yml", ".json", ".md":
		return true
	default:
		return false
	}
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt(normA) * sqrt(normB))
}

func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 8; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}
