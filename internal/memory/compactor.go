package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const compactionSystemPrompt = `You are a memory compaction engine. Given a set of related facts in the same category, merge them into a single structured summary that preserves all important information.

Rules:
1. Preserve all key facts - do not lose information.
2. Organize the summary logically, grouping related points.
3. Be concise but complete.
4. Output a well-structured paragraph or short set of bullet points.
5. The summary should be self-contained and understandable without the original facts.`

// Compactor merges related memories within the same category into summaries
// when the number of memories in a category exceeds a threshold.
type Compactor struct {
	store     Store
	completer Completer
	db        *sql.DB
	baseDir   string
	cfg       MemoryConfig
	interval  time.Duration
	done      chan struct{}
}

// NewCompactor creates a Compactor with the given configuration.
func NewCompactor(store Store, completer Completer, db *sql.DB, baseDir string, cfg MemoryConfig) *Compactor {
	interval := 6 * time.Hour
	if cfg.CompactionInterval > 0 {
		interval = cfg.CompactionInterval
	}
	return &Compactor{
		store:     store,
		completer: completer,
		db:        db,
		baseDir:   baseDir,
		cfg:       cfg,
		interval:  interval,
		done:      make(chan struct{}),
	}
}

// Start begins the background compaction loop.
func (c *Compactor) Start(ctx context.Context) {
	go c.loop(ctx)
}

// Stop signals the compaction loop to exit.
func (c *Compactor) Stop() {
	close(c.done)
}

func (c *Compactor) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.Compact(ctx); err != nil {
				slog.Warn("compactor: compaction failed", "err", err)
			}
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Compact finds categories with too many memories and generates summaries.
func (c *Compactor) Compact(ctx context.Context) error {
	slog.Info("compactor: running memory compaction")

	candidates, err := c.findCompactionCandidates(ctx)
	if err != nil {
		return fmt.Errorf("find compaction candidates: %w", err)
	}

	if len(candidates) == 0 {
		slog.Info("compactor: no categories above threshold")
		return nil
	}

	compacted := 0
	for category, memIDs := range candidates {
		// Load content for each memory
		var contents []string
		for _, id := range memIDs {
			var filePath string
			err := c.db.QueryRowContext(ctx, `SELECT file_path FROM memory_index WHERE memory_id = ?`, id).Scan(&filePath)
			if err != nil {
				slog.Warn("compactor: failed to find file path", "id", id, "err", err)
				continue
			}

			mf, err := c.parseFile(filePath)
			if err != nil {
				slog.Warn("compactor: failed to parse file", "path", filePath, "err", err)
				continue
			}
			contents = append(contents, mf.Content)
		}

		if len(contents) == 0 {
			continue
		}

		if err := c.generateSummary(ctx, category, memIDs, contents); err != nil {
			slog.Warn("compactor: failed to generate summary", "category", category, "err", err)
			continue
		}
		compacted++
	}

	slog.Info("compactor: done", "categories_compacted", compacted)
	return nil
}

// findCompactionCandidates returns a map of category -> []memory_id for categories
// that have more memories than the compaction threshold.
func (c *Compactor) findCompactionCandidates(ctx context.Context) (map[string][]string, error) {
	threshold := 8
	if c.cfg.CompactionThreshold > 0 {
		threshold = c.cfg.CompactionThreshold
	}

	// Query all user-scope non-summary memories from index
	rows, err := c.db.QueryContext(ctx, `
		SELECT memory_id, file_path FROM memory_index
		WHERE scope = 'user' AND memory_type NOT IN ('summary', 'reflection', 'profile')
	`)
	if err != nil {
		return nil, fmt.Errorf("query memory_index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Group memories by category extracted from file metadata
	type memInfo struct {
		id       string
		filePath string
	}
	categoryMemories := make(map[string][]memInfo)

	for rows.Next() {
		var mi memInfo
		if err := rows.Scan(&mi.id, &mi.filePath); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		mf, err := c.parseFile(mi.filePath)
		if err != nil {
			slog.Warn("compactor: failed to parse file for categorization", "path", mi.filePath, "err", err)
			continue
		}

		category := "general"
		if mf.Metadata != nil {
			if cat, ok := mf.Metadata["category"]; ok && cat != "" {
				category = cat
			}
		}

		categoryMemories[category] = append(categoryMemories[category], mi)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	// Filter categories above threshold and collect memory IDs
	result := make(map[string][]string)
	for category, memories := range categoryMemories {
		if len(memories) >= threshold {
			ids := make([]string, len(memories))
			for i, m := range memories {
				ids[i] = m.id
			}
			result[category] = ids
		}
	}

	return result, nil
}

// generateSummary uses the LLM to create a summary of the given facts and saves it.
func (c *Compactor) generateSummary(ctx context.Context, category string, factIDs []string, factContents []string) error {
	// Build user prompt with all fact contents
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Category: %s\n\nFacts to merge:\n\n", category)
	for i, content := range factContents {
		_, _ = fmt.Fprintf(&sb, "--- Fact %d ---\n%s\n\n", i+1, content)
	}
	sb.WriteString("Please merge these facts into a single structured summary.")

	// Call completer
	summary, err := c.completer.Complete(ctx, compactionSystemPrompt, sb.String())
	if err != nil {
		return fmt.Errorf("LLM compaction failed: %w", err)
	}

	// Save as a new summary memory
	now := time.Now()
	summaryID := fmt.Sprintf("summary_%s_%d", category, now.UnixNano())

	entry := Entry{
		ID:        summaryID,
		Scope:     ScopeUser,
		Content:   summary,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]string{
			"type":         "summary",
			"category":     category,
			"source_facts": strings.Join(factIDs, ","),
		},
	}

	if err := c.store.Save(ctx, entry); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}

	slog.Info("compactor: generated summary",
		"category", category,
		"source_count", len(factIDs),
		"summary_id", summaryID,
	)

	// Note: source facts are NOT deleted - they decay naturally via the forgetting curve
	return nil
}

func (c *Compactor) parseFile(path string) (*MemoryFile, error) {
	fs := &FileMemoryStore{baseDir: c.baseDir}
	return fs.parseFile(path)
}
