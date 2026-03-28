# Memory System Optimization - Design

## Architecture

### Unified Storage Model

```
┌─────────────────────────────────────────────────────────────────┐
│                        Application Layer                        │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                    UnifiedMemoryStore                           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Primary: FileStore (source of truth)                    │  │
│  │  - Markdown files: ~/.IronClaw/memory/{scope}/{id}.md    │  │
│  │  - Human-readable, git-friendly                          │  │
│  │  - Transaction log for durability                        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                             │                                   │
│                             ▼                                   │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Secondary: SQLite Indexes (derived, rebuildable)        │  │
│  │  - fact_index: Fast lookups by ID/scope/user            │  │
│  │  - fact_fts: BM25 full-text search                      │  │
│  │  - fact_vss: HNSW vector index (default enabled)        │  │
│  │  - fact_history: Immutable audit log                    │  │
│  │  - fact_access_log: Track retrieval frequency           │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### Component Design

#### 1. Unified Store Interface

```go
type UnifiedStore struct {
    primary   *FileStore          // Source of truth
    index     *IndexDB            // SQLite indexes
    graph     *graph.SQLiteGraph  // Knowledge graph
    embedder  EmbeddingProvider
    cfg       MemoryConfig
}

// Write path: File → Index → Graph
func (s *UnifiedStore) SaveFact(ctx, entry) error {
    // 1. Write to file (primary)
    if err := s.primary.SaveFact(ctx, entry); err != nil {
        return err
    }

    // 2. Update index (async, best-effort)
    go s.index.IndexFact(ctx, entry)

    // 3. Extract entities → graph (async)
    if s.cfg.GraphEnabled {
        go s.graph.ExtractAndIndex(ctx, entry)
    }

    return nil
}

// Read path: Index → File (if needed)
func (s *UnifiedStore) Search(ctx, query) ([]SearchResult, error) {
    // Fast path: Use indexes
    results, err := s.index.HybridSearch(ctx, query)
    if err != nil {
        // Fallback: Scan files
        return s.primary.Search(ctx, query)
    }
    return results
}
```

#### 2. Smart Consolidation

```go
type ConsolidationScore struct {
    AccessFrequency float64  // 0-1: normalized access count
    Recency         float64  // 0-1: time decay
    Importance      float64  // 0-1: LLM-scored value
    Final           float64  // weighted sum
}

type SmartConsolidator struct {
    store       UnifiedStore
    completer   Completer
    accessLog   *AccessLog
    cfg         ConsolidationConfig
}

func (c *SmartConsolidator) Score(fact Entry) ConsolidationScore {
    // Access frequency (normalized by max)
    accessCount := c.accessLog.GetCount(fact.ID)
    maxAccess := c.accessLog.GetMaxCount()
    accessFreq := float64(accessCount) / float64(maxAccess)

    // Recency (exponential decay)
    age := time.Since(fact.CreatedAt)
    recency := math.Exp(-age.Hours() / (24 * 30)) // 30-day half-life

    // Importance (cached LLM score)
    importance := c.getImportanceScore(fact)

    // Weighted sum (configurable)
    final := c.cfg.AccessWeight*accessFreq +
             c.cfg.RecencyWeight*recency +
             c.cfg.ImportanceWeight*importance

    return ConsolidationScore{
        AccessFrequency: accessFreq,
        Recency:         recency,
        Importance:      importance,
        Final:           final,
    }
}

func (c *SmartConsolidator) Consolidate(ctx) error {
    facts, _ := c.store.ListByScope(ctx, ScopeSession, "")

    for _, fact := range facts {
        score := c.Score(fact)

        // Promote if score exceeds threshold
        if score.Final >= c.cfg.PromotionThreshold {
            c.promoteFact(ctx, fact, score)
        }
    }
    return nil
}
```

#### 3. Memory-Graph Integration

```go
type GraphIntegrator struct {
    graph     *graph.SQLiteGraph
    extractor *graph.LLMEntityExtractor
}

// Automatically extract entities from facts
func (g *GraphIntegrator) IndexFact(ctx, fact Entry) error {
    // Extract entities and relations
    triples, err := g.extractor.Extract(ctx, fact.Content)
    if err != nil {
        return err
    }

    // Add to graph with provenance
    for _, triple := range triples {
        g.graph.AddNode(ctx, graph.Node{
            ID:   triple.Subject,
            Type: triple.SubjectType,
            Properties: map[string]any{
                "source_fact_id": fact.ID,
                "extracted_at":   time.Now(),
            },
        })

        g.graph.AddEdge(ctx, graph.Edge{
            From:       triple.Subject,
            To:         triple.Object,
            Relation:   triple.Predicate,
            Properties: map[string]any{
                "confidence": triple.Confidence,
            },
        })
    }

    return nil
}

// Query: "What does user know about X?"
func (g *GraphIntegrator) QueryKnowledge(ctx, entity string, depth int) ([]Entry, error) {
    // 1. Find entity in graph
    nodes, _ := g.graph.FindNodes(ctx, entity)

    // 2. Traverse N hops
    related := g.graph.Traverse(ctx, nodes[0].ID, depth)

    // 3. Retrieve source facts
    var facts []Entry
    for _, node := range related {
        factID := node.Properties["source_fact_id"].(string)
        fact, _ := g.store.GetByID(ctx, factID)
        facts = append(facts, fact)
    }

    return facts, nil
}
```

#### 4. Multi-Provider Embeddings

```go
type EmbeddingProvider interface {
    Embed(ctx, text) ([]float32, error)
    EmbedBatch(ctx, texts) ([][]float32, error)
    Dimensions() int
    ModelID() string  // For versioning
}

// Provider registry
type EmbeddingRegistry struct {
    providers map[string]EmbeddingProvider
    default   string
}

func NewEmbeddingRegistry() *EmbeddingRegistry {
    r := &EmbeddingRegistry{
        providers: make(map[string]EmbeddingProvider),
    }

    // Register providers
    r.Register("openai-small", NewOpenAIEmbedding("text-embedding-3-small"))
    r.Register("openai-large", NewOpenAIEmbedding("text-embedding-3-large"))
    r.Register("voyage-2", NewVoyageEmbedding("voyage-2"))
    r.Register("local-bge", NewOllamaEmbedding("bge-m3"))

    return r
}

// Automatic fallback chain
func (r *EmbeddingRegistry) EmbedWithFallback(ctx, text) ([]float32, error) {
    providers := []string{r.default, "local-bge", "openai-small"}

    for _, name := range providers {
        if p, ok := r.providers[name]; ok {
            if emb, err := p.Embed(ctx, text); err == nil {
                return emb, nil
            }
        }
    }

    return nil, fmt.Errorf("all embedding providers failed")
}
```

#### 5. Fact History & Versioning

```sql
-- Immutable append-only history
CREATE TABLE fact_history (
    fact_id      TEXT NOT NULL,
    version      INTEGER NOT NULL,
    content      TEXT NOT NULL,
    embedding    BLOB,
    changed_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    changed_by   TEXT,  -- session_id
    change_type  TEXT,  -- ADD/UPDATE/DELETE
    change_reason TEXT, -- From lifecycle decision
    PRIMARY KEY (fact_id, version)
);

CREATE INDEX idx_fact_history_time ON fact_history(changed_at);
CREATE INDEX idx_fact_history_type ON fact_history(change_type);
```

```go
type HistoryManager struct {
    db *store.DB
}

// Record every change
func (h *HistoryManager) RecordChange(ctx, change FactChange) error {
    _, err := h.db.ExecContext(ctx, `
        INSERT INTO fact_history
        (fact_id, version, content, embedding, changed_by, change_type, change_reason)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, change.FactID, change.Version, change.Content, change.Embedding,
       change.ChangedBy, change.Type, change.Reason)
    return err
}

// Time-travel query
func (h *HistoryManager) GetFactAtTime(ctx, factID string, t time.Time) (Entry, error) {
    var entry Entry
    err := h.db.QueryRowContext(ctx, `
        SELECT fact_id, version, content, embedding, changed_at
        FROM fact_history
        WHERE fact_id = ? AND changed_at <= ?
        ORDER BY version DESC
        LIMIT 1
    `, factID, t).Scan(&entry.ID, &entry.Version, &entry.Content,
                       &entry.Embedding, &entry.UpdatedAt)
    return entry, err
}

// Rollback to version
func (h *HistoryManager) Rollback(ctx, factID string, version int) error {
    // Get historical version
    old, err := h.GetFactVersion(ctx, factID, version)
    if err != nil {
        return err
    }

    // Create new version with old content
    return h.store.UpdateFact(ctx, factID, old.Content, old.Version+1)
}
```

#### 6. Search Explainability

```go
type SearchExplanation struct {
    Query        string
    Results      []ExplainedResult
    TotalLatency time.Duration
    CacheHit     bool
}

type ExplainedResult struct {
    Entry        Entry
    FinalScore   float64
    BM25Score    float64
    VectorScore  float64
    RRFRank      int
    Explanation  string
}

func (s *UnifiedStore) SearchWithExplanation(ctx, query) (SearchExplanation, error) {
    start := time.Now()

    // BM25 search
    bm25Start := time.Now()
    bm25Results := s.index.BM25Search(ctx, query.Text)
    bm25Latency := time.Since(bm25Start)

    // Vector search
    vecStart := time.Now()
    vecResults := s.index.VectorSearch(ctx, query.Embedding)
    vecLatency := time.Since(vecStart)

    // RRF fusion
    fused := s.fuseWithExplanation(bm25Results, vecResults)

    return SearchExplanation{
        Query:        query.Text,
        Results:      fused,
        TotalLatency: time.Since(start),
        Metadata: map[string]any{
            "bm25_latency":   bm25Latency,
            "vector_latency": vecLatency,
            "bm25_count":     len(bm25Results),
            "vector_count":   len(vecResults),
        },
    }, nil
}

func (s *UnifiedStore) fuseWithExplanation(bm25, vector []SearchResult) []ExplainedResult {
    // RRF fusion with detailed scoring
    scores := make(map[string]*ExplainedResult)

    for rank, r := range bm25 {
        scores[r.Entry.ID] = &ExplainedResult{
            Entry:       r.Entry,
            BM25Score:   r.Score,
            RRFRank:     rank,
            Explanation: fmt.Sprintf("BM25 rank %d (score %.3f)", rank, r.Score),
        }
    }

    for rank, r := range vector {
        if ex, ok := scores[r.Entry.ID]; ok {
            ex.VectorScore = r.Score
            ex.Explanation += fmt.Sprintf(" + Vector rank %d (score %.3f)", rank, r.Score)
        } else {
            scores[r.Entry.ID] = &ExplainedResult{
                Entry:       r.Entry,
                VectorScore: r.Score,
                RRFRank:     rank,
                Explanation: fmt.Sprintf("Vector rank %d (score %.3f)", rank, r.Score),
            }
        }
    }

    // Calculate final RRF scores
    for id, ex := range scores {
        bm25Rank := ex.RRFRank
        vecRank := findRank(vector, id)
        ex.FinalScore = 1.0/(60+bm25Rank) + 1.0/(60+vecRank)
    }

    // Sort by final score
    results := make([]ExplainedResult, 0, len(scores))
    for _, ex := range scores {
        results = append(results, *ex)
    }
    sort.Slice(results, func(i, j int) bool {
        return results[i].FinalScore > results[j].FinalScore
    })

    return results
}
```

## Migration Strategy

### Phase 1: Add New Components (No Breaking Changes)

1. Create `UnifiedStore` wrapper around existing stores
2. Add `fact_history` table
3. Add `fact_access_log` table
4. Implement `SmartConsolidator` alongside old one
5. Add `EmbeddingRegistry` with OpenAI as default

### Phase 2: Enable New Features (Opt-in)

1. Config flag: `memory.unified_mode: true`
2. Config flag: `memory.smart_consolidation: true`
3. Config flag: `memory.graph_integration: true`
4. Config flag: `memory.enable_history: true`

### Phase 3: Migrate Data

```bash
# Migration tool
ironclaw memory migrate \
  --from sqlite \
  --to unified \
  --backup ./backup
```

### Phase 4: Deprecate Old Code

1. Mark `SQLiteStore.Save()` as deprecated
2. Remove dual writes
3. Remove time-based consolidator
4. Update documentation

## Configuration

```yaml
memory:
  # Storage
  storage_mode: unified  # "unified" | "sqlite" | "file"
  storage_dir: ~/.IronClaw/memory

  # Embeddings
  embedding:
    provider: openai-small  # or "voyage-2", "local-bge"
    fallback_chain:
      - local-bge
      - openai-small
    cache_size: 1000
    cache_ttl: 10m

  # Search
  search:
    enable_hnsw: true  # Default enabled
    bm25_weight: 0.4
    vector_weight: 0.6
    enable_cache: true
    explain_mode: false  # Debug mode

  # Consolidation
  consolidation:
    strategy: smart  # "smart" | "time-based"
    interval: 1h
    promotion_threshold: 0.7
    weights:
      access: 0.4
      recency: 0.3
      importance: 0.3

  # Graph integration
  graph:
    enabled: true
    auto_extract: true
    extraction_batch_size: 10

  # History
  history:
    enabled: true
    retention_days: 90  # Prune old history
```

## Performance Targets

| Metric | Current | Target | Method |
|--------|---------|--------|--------|
| Search latency (1K facts) | ~100ms | <10ms | HNSW default |
| Search latency (10K facts) | ~1s | <50ms | HNSW + cache |
| Consolidation accuracy | ~60% | >85% | Smart scoring |
| Storage overhead | 2x (dual) | 1.3x | Unified + history |
| Embedding cost | $0.10/1K | $0.02/1K | Local fallback |

## Testing Strategy

1. **Unit tests**: Each component isolated
2. **Integration tests**: Full pipeline with real LLM
3. **Migration tests**: SQLite → Unified with data validation
4. **Performance tests**: Benchmark search at 1K/10K/100K scale
5. **Chaos tests**: Simulate index corruption, rebuild from files
