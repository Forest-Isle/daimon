package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/punkopunko/ironclaw/internal/knowledge/ingest"
)

// IngestPipeline orchestrates document ingestion: parse -> chunk -> embed -> store.
type IngestPipeline struct {
	kb       *SQLiteKnowledgeBase
	registry *ingest.Registry
	strategy ChunkStrategy
}

// NewIngestPipeline creates a pipeline with default ingesters.
func NewIngestPipeline(kb *SQLiteKnowledgeBase, cfg Config) *IngestPipeline {
	size := cfg.ChunkSize
	if size <= 0 {
		size = 512
	}
	overlap := cfg.ChunkOverlap
	if overlap < 0 {
		overlap = 64
	}
	return &IngestPipeline{
		kb:       kb,
		registry: ingest.NewRegistry(),
		strategy: ChunkStrategy{ChunkSize: size, ChunkOverlap: overlap},
	}
}

// Ingest fetches a URI, splits into chunks, embeds, and stores.
func (p *IngestPipeline) Ingest(ctx context.Context, uri, sourceType string) error {
	if sourceType == "" {
		sourceType = ingest.DetectSourceType(uri)
	}

	slog.Info("knowledge: ingesting", "uri", uri, "type", sourceType)

	title, content, err := p.registry.Extract(ctx, uri, sourceType)
	if err != nil {
		return fmt.Errorf("extract %s: %w", uri, err)
	}

	if content == "" {
		return fmt.Errorf("empty content from %s", uri)
	}

	// Upsert source record
	sourceID, err := p.kb.saveSource(ctx, uri, sourceType, title)
	if err != nil {
		return fmt.Errorf("save source: %w", err)
	}

	// Split into chunks
	chunks := ChunkText(content, p.strategy)
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks produced from %s", uri)
	}

	// Store each chunk (embedding happens inside saveChunk)
	for i, text := range chunks {
		chunk := Chunk{
			ID:         fmt.Sprintf("chunk_%s_%d_%d", sourceID, i, time.Now().UnixNano()),
			SourceID:   sourceID,
			SourceURI:  uri,
			SourceType: sourceType,
			Content:    text,
			ChunkIndex: i,
			CreatedAt:  time.Now(),
		}
		if err := p.kb.saveChunk(ctx, chunk); err != nil {
			slog.Warn("knowledge: failed to save chunk", "uri", uri, "index", i, "err", err)
		}
	}

	// Update chunk count
	p.kb.updateChunkCount(ctx, sourceID)

	slog.Info("knowledge: ingested", "uri", uri, "chunks", len(chunks))
	return nil
}

// IngestDir scans and ingests all files in a directory.
func (p *IngestPipeline) IngestDir(ctx context.Context, dir string) error {
	files, err := ingest.ScanDir(dir)
	if err != nil {
		return fmt.Errorf("scan dir %s: %w", dir, err)
	}
	for _, f := range files {
		if err := p.Ingest(ctx, f.Path, f.SourceType); err != nil {
			slog.Warn("knowledge: ingest file failed", "path", f.Path, "err", err)
		}
	}
	return nil
}
