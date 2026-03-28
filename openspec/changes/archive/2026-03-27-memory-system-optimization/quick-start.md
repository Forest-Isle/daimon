# Memory System Optimization - Quick Start Implementation

This document provides the **minimal viable implementation** for the highest-impact improvements. Focus on 20% of work that delivers 80% of value.

## Priority 1: Unified Storage (3 days)

### Step 1: Create UnifiedStore

```go
// internal/memory/unified_store.go
package memory

import (
    "context"
    "log/slog"
)

type UnifiedStore struct {
    primary  *FileStore
    index    *SQLiteStore
    history  *HistoryManager
    cfg      MemoryConfig
}

func NewUnifiedStore(primary *FileStore, index *SQLiteStore, cfg MemoryConfig) *UnifiedStore {
    return &UnifiedStore{
        primary: primary,
        index:   index,
        history: NewHistoryManager(index.db),
        cfg:     cfg,
    }
}

func (s *UnifiedStore) SaveFact(ctx context.Context, entry Entry) error {
    // 1. Write to file (primary, blocking)
    if err := s.primary.SaveFact(ctx, entry); err != nil {
        return err
    }

    // 2. Update index (async, best-effort)
    go func() {
        if err := s.index.SaveFact(context.Background(), entry); err != nil {
            slog.Warn("unified: index update failed", "err", err)
        }
    }()

    // 3. Record history (async)
    if s.cfg.EnableHistory {
        go s.history.Record(context.Background(), entry, "ADD")
    }

    return nil
}

func (s *UnifiedStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
    // Try index first (fast path)
    results, err := s.index.Search(ctx, query)
    if err == nil {
        return results, nil
    }

    // Fallback to file scan
    slog.Warn("unified: index search failed, falling back to file scan", "err", err)
    return s.primary.Search(ctx, query)
}

func (s *UnifiedStore) UpdateFact(ctx context.Context, id, content string, version int) error {
    if err := s.primary.UpdateFact(ctx, id, content, version); err != nil {
        return err
    }
    go s.index.UpdateFact(context.Background(), id, content, version)
    if s.cfg.EnableHistory {
        go s.history.RecordUpdate(context.Background(), id, content, version)
    }
    return nil
}

func (s *UnifiedStore) DeleteFact(ctx context.Context, id string) error {
    if err := s.primary.DeleteFact(ctx, id); err != nil {
        return err
    }
    go s.index.DeleteFact(context.Background(), id)
    if s.cfg.EnableHistory {
        go s.history.RecordDelete(context.Background(), id)
    }
    return nil
}

// Rebuild index from files (recovery)
func (s *UnifiedStore) RebuildIndex(ctx context.Context) error {
    slog.Info("unified: rebuilding index from files")

    scopes := []MemoryScope{ScopeSession, ScopeUser, ScopeGlobal}
    for _, scope := range scopes {
        facts, err := s.primary.ListByScope(ctx, scope, "")
        if err != nil {
            return err
        }

        for _, fact := range facts {
            if err := s.index.SaveFact(ctx, fact); err != nil {
                slog.Warn("unified: failed to index fact", "id", fact.ID, "err", err)
            }
        }
    }

    slog.Info("unified: index rebuild complete")
    return nil
}
```

### Step 2: Add History Table

```sql
-- internal/store/migrations/008_fact_history.sql
CREATE TABLE IF NOT EXISTS fact_history (
    fact_id      TEXT NOT NULL,
    version      INTEGER NOT NULL,
    content      TEXT NOT NULL,
    changed_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    changed_by   TEXT,
    change_type  TEXT CHECK(change_type IN ('ADD', 'UPDATE', 'DELETE')),
    PRIMARY KEY (fact_id, version)
);

CREATE INDEX IF NOT EXISTS idx_fact_history_time ON fact_history(changed_at);
CREATE INDEX IF NOT EXISTS idx_fact_history_fact ON fact_history(fact_id);
```

```go
// internal/memory/history.go
package memory

import (
    "context"
    "time"
    "github.com/punkopunko/ironclaw/internal/store"
)

type HistoryManager struct {
    db *store.DB
}

func NewHistoryManager(db *store.DB) *HistoryManager {
    return &HistoryManager{db: db}
}

func (h *HistoryManager) Record(ctx context.Context, entry Entry, changeType string) error {
    _, err := h.db.ExecContext(ctx, `
        INSERT INTO fact_history (fact_id, version, content, changed_by, change_type)
        VALUES (?, ?, ?, ?, ?)
    `, entry.ID, entry.Version, entry.Content, entry.SessionID, changeType)
    return err
}

func (h *HistoryManager) RecordUpdate(ctx context.Context, id, content string, version int) error {
    _, err := h.db.ExecContext(ctx, `
        INSERT INTO fact_history (fact_id, version, content, change_type)
        VALUES (?, ?, ?, 'UPDATE')
    `, id, version, content)
    return err
}

func (h *HistoryManager) RecordDelete(ctx context.Context, id string) error {
    _, err := h.db.ExecContext(ctx, `
        INSERT INTO fact_history (fact_id, version, content, change_type)
        VALUES (?, 0, '', 'DELETE')
    `, id)
    return err
}

func (h *HistoryManager) GetHistory(ctx context.Context, factID string) ([]Entry, error) {
    rows, err := h.db.QueryContext(ctx, `
        SELECT fact_id, version, content, changed_at, change_type
        FROM fact_history
        WHERE fact_id = ?
        ORDER BY version DESC
    `, factID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var history []Entry
    for rows.Next() {
        var e Entry
        var changeType string
        rows.Scan(&e.ID, &e.Version, &e.Content, &e.UpdatedAt, &changeType)
        e.Metadata = map[string]string{"change_type": changeType}
        history = append(history, e)
    }
    return history, nil
}
```

---

## Priority 2: Smart Consolidation (2 days)

```go
// internal/memory/smart_consolidator.go
package memory

import (
    "context"
    "math"
    "time"
)

type SmartConsolidator struct {
    store     Store
    completer Completer
    cfg       SmartConsolidationConfig
}

type SmartConsolidationConfig struct {
    Interval           time.Duration
    PromotionThreshold float64
    AccessWeight       float64
    RecencyWeight      float64
    ImportanceWeight   float64
}

func NewSmartConsolidator(store Store, completer Completer, cfg SmartConsolidationConfig) *SmartConsolidator {
    // Set defaults
    if cfg.PromotionThreshold == 0 {
        cfg.PromotionThreshold = 0.7
    }
    if cfg.AccessWeight == 0 {
        cfg.AccessWeight = 0.4
    }
    if cfg.RecencyWeight == 0 {
        cfg.RecencyWeight = 0.3
    }
    if cfg.ImportanceWeight == 0 {
        cfg.ImportanceWeight = 0.3
    }

    return &SmartConsolidator{
        store:     store,
        completer: completer,
        cfg:       cfg,
    }
}

func (c *SmartConsolidator) Score(ctx context.Context, fact Entry) (float64, error) {
    // Access frequency (mock for now, implement access_log later)
    accessFreq := 0.5 // TODO: Get from access_log

    // Recency (exponential decay, 30-day half-life)
    age := time.Since(fact.CreatedAt)
    recency := math.Exp(-age.Hours() / (24 * 30))

    // Importance (LLM-scored, cached in metadata)
    importance := c.getImportance(ctx, fact)

    // Weighted sum
    score := c.cfg.AccessWeight*accessFreq +
             c.cfg.RecencyWeight*recency +
             c.cfg.ImportanceWeight*importance

    return score, nil
}

func (c *SmartConsolidator) getImportance(ctx context.Context, fact Entry) float64 {
    // Check cache
    if imp, ok := fact.Metadata["importance"]; ok {
        if score, err := parseFloat(imp); err == nil {
            return score
        }
    }

    // Score with LLM (expensive, cache result)
    if c.completer == nil {
        return 0.5 // Default
    }

    prompt := "Rate the long-term importance of this fact (0-1): " + fact.Content
    resp, err := c.completer.Complete(ctx, "You are a memory importance scorer.", prompt)
    if err != nil {
        return 0.5
    }

    score := parseImportanceScore(resp)

    // Cache in metadata
    if fact.Metadata == nil {
        fact.Metadata = make(map[string]string)
    }
    fact.Metadata["importance"] = fmt.Sprintf("%.2f", score)

    return score
}

func (c *SmartConsolidator) Consolidate(ctx context.Context) error {
    facts, err := c.store.ListByScope(ctx, ScopeSession, "")
    if err != nil {
        return err
    }

    promoted := 0
    for _, fact := range facts {
        score, err := c.Score(ctx, fact)
        if err != nil {
            continue
        }

        if score >= c.cfg.PromotionThreshold {
            // Promote to user scope
            promoted++
            fact.Scope = ScopeUser
            fact.Version++
            c.store.SaveFact(ctx, fact)
        }
    }

    slog.Info("smart_consolidator: done", "promoted", promoted, "total", len(facts))
    return nil
}
```

---

## Priority 3: Enable HNSW by Default (1 day)

```yaml
# configs/ironclaw.example.yaml
memory:
  enabled: true
  storage_type: unified  # NEW: "unified" | "file" | "sqlite"

  # Vector search (HNSW enabled by default)
  enable_vss: true
  vector_dimension: 1536

  # History
  enable_history: true

  # Smart consolidation
  consolidation:
    strategy: smart  # "smart" | "time-based"
    promotion_threshold: 0.7
    weights:
      access: 0.4
      recency: 0.3
      importance: 0.3
```

```go
// internal/gateway/gateway.go (modify)
func New(cfg *config.Config) (*Gateway, error) {
    // ... existing code ...

    var memStore memory.Store
    if cfg.Memory.Enabled {
        embedder := createEmbedder(cfg)

        switch cfg.Memory.StorageType {
        case "unified":
            fileStore := memory.NewFileStore(cfg.Memory.StorageDir, embeddingsDB, embedder, memCfg)
            sqliteStore := memory.NewSQLiteStore(db, embedder, memCfg)
            memStore = memory.NewUnifiedStore(fileStore, sqliteStore, memCfg)

        case "file":
            memStore = memory.NewFileStore(cfg.Memory.StorageDir, embeddingsDB, embedder, memCfg)

        case "sqlite":
            memStore = memory.NewSQLiteStore(db, embedder, memCfg)

        default:
            return nil, fmt.Errorf("unknown storage_type: %s", cfg.Memory.StorageType)
        }

        // Smart consolidation
        if cfg.Memory.Consolidation.Strategy == "smart" {
            consolidator := memory.NewSmartConsolidator(
                memStore,
                completerAdapter{provider},
                memory.SmartConsolidationConfig{
                    Interval:           cfg.Memory.Consolidation.Interval,
                    PromotionThreshold: cfg.Memory.Consolidation.PromotionThreshold,
                    AccessWeight:       cfg.Memory.Consolidation.Weights.Access,
                    RecencyWeight:      cfg.Memory.Consolidation.Weights.Recency,
                    ImportanceWeight:   cfg.Memory.Consolidation.Weights.Importance,
                },
            )
            consolidator.Start(context.Background())
        }
    }

    // ... rest of code ...
}
```

---

## Priority 4: Memory-Graph Integration (2 days)

```go
// internal/memory/graph_integrator.go
package memory

import (
    "context"
    "log/slog"
    "github.com/punkopunko/ironclaw/internal/knowledge/graph"
)

type GraphIntegrator struct {
    graph     *graph.SQLiteGraph
    extractor *graph.LLMEntityExtractor
    enabled   bool
}

func NewGraphIntegrator(g *graph.SQLiteGraph, extractor *graph.LLMEntityExtractor, enabled bool) *GraphIntegrator {
    return &GraphIntegrator{
        graph:     g,
        extractor: extractor,
        enabled:   enabled,
    }
}

func (gi *GraphIntegrator) IndexFact(ctx context.Context, fact Entry) error {
    if !gi.enabled || gi.extractor == nil {
        return nil
    }

    // Extract entities and relations
    triples, err := gi.extractor.Extract(ctx, fact.Content)
    if err != nil {
        slog.Warn("graph_integrator: extraction failed", "err", err)
        return nil // Non-fatal
    }

    // Add to graph
    for _, triple := range triples {
        // Add subject node
        gi.graph.AddNode(ctx, graph.Node{
            ID:   triple.Subject,
            Type: triple.SubjectType,
            Properties: map[string]any{
                "source_fact_id": fact.ID,
                "extracted_at":   time.Now().Unix(),
            },
        })

        // Add object node
        gi.graph.AddNode(ctx, graph.Node{
            ID:   triple.Object,
            Type: triple.ObjectType,
            Properties: map[string]any{
                "source_fact_id": fact.ID,
            },
        })

        // Add edge
        gi.graph.AddEdge(ctx, graph.Edge{
            From:     triple.Subject,
            To:       triple.Object,
            Relation: triple.Predicate,
            Properties: map[string]any{
                "confidence": triple.Confidence,
                "fact_id":    fact.ID,
            },
        })
    }

    slog.Info("graph_integrator: indexed fact", "fact_id", fact.ID, "triples", len(triples))
    return nil
}

// Query: "What does user know about X?"
func (gi *GraphIntegrator) QueryKnowledge(ctx context.Context, entity string, depth int) ([]Entry, error) {
    if !gi.enabled {
        return nil, nil
    }

    // Find entity in graph
    nodes, err := gi.graph.FindNodes(ctx, entity)
    if err != nil || len(nodes) == 0 {
        return nil, err
    }

    // Traverse N hops
    related := gi.graph.Traverse(ctx, nodes[0].ID, depth)

    // Collect unique fact IDs
    factIDs := make(map[string]bool)
    for _, node := range related {
        if factID, ok := node.Properties["source_fact_id"].(string); ok {
            factIDs[factID] = true
        }
    }

    // Retrieve facts (TODO: batch query)
    var facts []Entry
    for factID := range factIDs {
        // fact, _ := gi.store.GetByID(ctx, factID)
        // facts = append(facts, fact)
    }

    return facts, nil
}
```

Update UnifiedStore to integrate:

```go
func (s *UnifiedStore) SaveFact(ctx context.Context, entry Entry) error {
    if err := s.primary.SaveFact(ctx, entry); err != nil {
        return err
    }

    go s.index.SaveFact(context.Background(), entry)

    if s.cfg.EnableHistory {
        go s.history.Record(context.Background(), entry, "ADD")
    }

    // NEW: Extract to graph
    if s.graphIntegrator != nil {
        go s.graphIntegrator.IndexFact(context.Background(), entry)
    }

    return nil
}
```

---

## Migration Command (1 day)

```go
// cmd/ironclaw/memory.go
package main

import (
    "github.com/spf13/cobra"
)

var memoryCmd = &cobra.Command{
    Use:   "memory",
    Short: "Memory management commands",
}

var migrateCmd = &cobra.Command{
    Use:   "migrate",
    Short: "Migrate memory storage",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Load config
        cfg, err := loadConfig()
        if err != nil {
            return err
        }

        // Backup
        backupPath := fmt.Sprintf("./backup/memory_%s.tar.gz", time.Now().Format("20060102_150405"))
        if err := backupMemory(cfg, backupPath); err != nil {
            return fmt.Errorf("backup failed: %w", err)
        }
        fmt.Printf("Backup created: %s\n", backupPath)

        // Migrate
        migrator := memory.NewMigrator(cfg)
        if err := migrator.MigrateToUnified(context.Background()); err != nil {
            return fmt.Errorf("migration failed: %w", err)
        }

        fmt.Println("Migration complete!")
        return nil
    },
}

var rebuildCmd = &cobra.Command{
    Use:   "rebuild-index",
    Short: "Rebuild SQLite index from files",
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, _ := loadConfig()
        store := createUnifiedStore(cfg)
        return store.RebuildIndex(context.Background())
    },
}

func init() {
    memoryCmd.AddCommand(migrateCmd)
    memoryCmd.AddCommand(rebuildCmd)
    rootCmd.AddCommand(memoryCmd)
}
```

---

## Testing (1 day)

```go
// internal/memory/unified_store_test.go
package memory

import (
    "context"
    "testing"
)

func TestUnifiedStore_SaveAndSearch(t *testing.T) {
    // Setup
    fileStore := setupFileStore(t)
    sqliteStore := setupSQLiteStore(t)
    unified := NewUnifiedStore(fileStore, sqliteStore, MemoryConfig{})

    // Save fact
    fact := Entry{
        ID:      "test_fact_1",
        Content: "User prefers dark mode",
        Scope:   ScopeUser,
    }
    err := unified.SaveFact(context.Background(), fact)
    if err != nil {
        t.Fatalf("SaveFact failed: %v", err)
    }

    // Search
    results, err := unified.Search(context.Background(), SearchQuery{
        Text:  "dark mode",
        Limit: 10,
    })
    if err != nil {
        t.Fatalf("Search failed: %v", err)
    }

    if len(results) == 0 {
        t.Fatal("Expected results, got none")
    }

    if results[0].Entry.ID != "test_fact_1" {
        t.Errorf("Expected fact_1, got %s", results[0].Entry.ID)
    }
}

func TestUnifiedStore_RebuildIndex(t *testing.T) {
    // Setup with corrupted index
    fileStore := setupFileStore(t)
    sqliteStore := setupSQLiteStore(t)
    unified := NewUnifiedStore(fileStore, sqliteStore, MemoryConfig{})

    // Add facts to file
    for i := 0; i < 10; i++ {
        fileStore.SaveFact(context.Background(), Entry{
            ID:      fmt.Sprintf("fact_%d", i),
            Content: fmt.Sprintf("Test fact %d", i),
        })
    }

    // Corrupt index
    sqliteStore.db.Exec("DELETE FROM memory_facts")

    // Rebuild
    err := unified.RebuildIndex(context.Background())
    if err != nil {
        t.Fatalf("RebuildIndex failed: %v", err)
    }

    // Verify
    results, _ := unified.Search(context.Background(), SearchQuery{
        Text:  "Test",
        Limit: 100,
    })

    if len(results) != 10 {
        t.Errorf("Expected 10 facts after rebuild, got %d", len(results))
    }
}
```

---

## Summary

**Total Time: ~9 days of focused work**

1. **Day 1-3**: UnifiedStore + History
2. **Day 4-5**: Smart Consolidation
3. **Day 6**: HNSW default + config
4. **Day 7-8**: Graph Integration
5. **Day 9**: Migration tool + tests

**Immediate Benefits**:
- ✅ Single source of truth (files)
- ✅ Fast queries (HNSW + indexes)
- ✅ Audit trail (history)
- ✅ Quality-based consolidation
- ✅ Graph-powered knowledge queries
- ✅ Recovery from index corruption

**What's Deferred**:
- Multi-provider embeddings (use OpenAI for now)
- Search explainability (add later)
- Incremental updates (not critical)
- Advanced observability (nice-to-have)
