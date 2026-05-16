# L0 — Unified Cortex (统一记忆皮层)

> 优先级: P0 | 工作量: 3-4 周 | 依赖: 无  
> 将三套独立存储系统融合为一个统一的、向量原生的、多类型记忆皮层。

---

## 一、现状痛点

```
当前架构:
┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│ memory/      │  │ knowledge/   │  │ graph/       │  │ profile/     │
│ Markdown文件  │  │ SQLite块     │  │ SQLite三元组  │  │ Markdown分片  │
│ +FTS5+向量    │  │ +FTS5+向量   │  │ +CTE遍历     │  │ +分类路由     │
└──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘
       ↓                ↓                ↓                ↓
  四次独立检索，各自排序，没有融合打分 ──▶ 上下文质量取决于最差的那个检索结果
```

**实际影响:**
- 认知循环的 PERCEIVE 阶段做 4 次独立检索，耗时叠加
- 记忆和知识的排名不互通——一段对话记忆可能和一段文档高度相关，但系统不知道
- 知识图谱是静态实体关系，不和动态记忆交互
- 没有"程序记忆"概念——agent 不会记住"上次怎么解决问题的"
- Markdown 文件主存储是聪明做法，但向量检索体验差（文件 IO 延迟）

---

## 二、目标架构

```
┌──────────────────────────────────────────────────────────┐
│                   Unified Cortex                         │
│                                                          │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐         │
│  │ Episodic   │  │ Semantic   │  │ Procedural │         │
│  │ 情节记忆    │  │ 语义记忆    │  │ 程序记忆    │         │
│  │ session级  │  │ 文档+图谱   │  │ 工具模式    │         │
│  └─────┬──────┘  └─────┬──────┘  └─────┬──────┘         │
│        │               │               │                 │
│  ┌─────┴───────────────┴───────────────┴──────┐         │
│  │         Unified Retriever (统一检索)        │         │
│  │   ColBERT级晚交互 + Learned Fusion Weights  │         │
│  └────────────────────┬───────────────────────┘         │
│                       │                                  │
│  ┌────────────────────┴───────────────────────┐         │
│  │         Working Memory (工作记忆)           │         │
│  │   注意力机制 | 上下文窗口管理 | 优先级排序   │         │
│  └────────────────────────────────────────────┘         │
│                                                          │
│  ┌────────────────────────────────────────────┐         │
│  │    Consolidation Engine (睡眠巩固引擎)       │         │
│  │   后台: 合并 | 抽象 | 遗忘 | 提升           │         │
│  └────────────────────────────────────────────┘         │
│                                                          │
│  Storage: Embedded LanceDB/DuckDB + 向量索引              │
│  Export: Markdown (可读导出格式，非主存储)                │
└──────────────────────────────────────────────────────────┘
```

---

## 三、详细设计

### 3.1 四种记忆类型

```go
// internal/cortex/types.go

// MemoryType 定义皮层中的记忆类别
type MemoryType int

const (
    Episodic   MemoryType = iota // 情节记忆 — 对话、交互、事件
    Semantic                      // 语义记忆 — 文档、事实、知识
    Procedural                    // 程序记忆 — 工具使用模式、成功策略
    Working                       // 工作记忆 — 当前上下文窗口内容（瞬态）
)

// Memory 是皮层中的统一记忆单元
type Memory struct {
    ID          string            `json:"id"`
    Type        MemoryType        `json:"type"`
    Scope       string            `json:"scope"`       // session / user / global
    SessionID   string            `json:"session_id,omitempty"`
    Content     string            `json:"content"`
    Summary     string            `json:"summary"`      // 压缩摘要（用于候选召回）
    Embedding   []float32         `json:"embedding"`    // 向量嵌入
    Metadata    map[string]any    `json:"metadata"`
    Strength    float64           `json:"strength"`     // 0.0-1.0, 遗忘曲线
    AccessCount int               `json:"access_count"`
    LastAccess  time.Time         `json:"last_access"`
    CreatedAt   time.Time         `json:"created_at"`
    // 图谱关系
    Relations   []Relation        `json:"relations,omitempty"`
    // 程序记忆特有
    ToolName    string            `json:"tool_name,omitempty"`
    Strategy    *StrategyRecord   `json:"strategy,omitempty"`
}

// Relation 连接两个记忆单元
type Relation struct {
    TargetID string  `json:"target_id"`
    Type     string  `json:"type"`     // "references", "contradicts", "summarizes", "follows"
    Weight   float64 `json:"weight"`
}
```

### 3.2 存储层 — 从 Markdown+SQLite 到 LanceDB/DuckDB

```go
// internal/cortex/store.go

// Store 是皮层持久化接口
type Store interface {
    // 基本 CRUD
    Save(ctx context.Context, m *Memory) error
    Get(ctx context.Context, id string) (*Memory, error)
    Update(ctx context.Context, m *Memory) error
    Delete(ctx context.Context, id string) error

    // 批量操作
    SaveBatch(ctx context.Context, memories []*Memory) error

    // 向量检索 (ANN)
    SearchSimilar(ctx context.Context, embedding []float32, limit int, filters ...Filter) ([]*Memory, error)

    // 全文检索 (BM25/FTS)
    SearchText(ctx context.Context, query string, limit int, filters ...Filter) ([]*Memory, error)

    // 关系遍历
    GetRelations(ctx context.Context, id string, relationType string, depth int) ([]*Memory, error)
}
```

**为什么选 LanceDB/DuckDB 而不是继续用 SQLite:**

| 维度 | SQLite 当前 | LanceDB | DuckDB |
|------|------------|---------|--------|
| 向量检索 | 余弦相似度，全表扫描 | 原生 ANN (IVF-PQ) | Array 类型 + 自定义距离函数 |
| 嵌入存储 | BLOB，每次反序列化 | 原生 FixedSizeList | 支持但非原生 |
| 列式扫描 | 不支持 | 原生列式 | 原生列式 |
| 全文检索 | FTS5 (OK) | 无 | FTS 扩展 |
| 零依赖 | ✅ | 需 CGO | 需 CGO |
| Go 绑定 | mattn/go-sqlite3 | 无(需 CGO 封装) | go-duckdb |

**推荐方案:**
- **过渡期**: 保留 SQLite 作为元数据存储，嵌入向量换用内存映射文件（mmap）减少 IO
- **目标态**: 嵌入用 LanceDB (via CGO) 或直接用 DuckDB 的 Array 类型 + 自定义 ANN 索引
- **最务实方案**: 用现有的 SQLite + 新增一个嵌入专用的内存映射向量索引（参考 FAISS 的 Product Quantization），不引入新 C 依赖

### 3.3 统一检索器 — 不再是简单的 RRF

```go
// internal/cortex/retriever.go

// UnifiedRetriever 将所有记忆类型融合检索
type UnifiedRetriever struct {
    store       Store
    embedder    EmbeddingProvider  // 生成查询嵌入
    weights     FusionWeights      // 可学习的融合权重
    reranker    Reranker           // 精排模型
}

// FusionWeights 定义每种检索信号的权重
// 这些权重不是硬编码的，而是通过用户反馈在线学习
type FusionWeights struct {
    VectorWeight   float64  // 向量语义相似度
    BM25Weight     float64  // 关键词匹配
    RecencyBoost   float64  // 时间衰减因子
    StrengthBias   float64  // 记忆强度偏置
    GraphBoost     float64  // 图谱关系加成
    TypeBoosts     map[MemoryType]float64  // 各类型记忆的权重
}

// Search 执行统一检索
// 一次调用返回所有类型的记忆，按融合分数排序
func (ur *UnifiedRetriever) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
    // 1. 生成查询嵌入
    queryEmb, _ := ur.embedder.Embed(ctx, query)

    // 2. 并行执行多种检索
    var (
        vectorResults  []*ScoredMemory
        bm25Results    []*ScoredMemory
        graphResults   []*ScoredMemory
        proceduralRes  []*ScoredMemory
    )
    var wg sync.WaitGroup
    wg.Add(4)
    go func() { defer wg.Done(); vectorResults = ur.vectorSearch(ctx, queryEmb, opts) }()
    go func() { defer wg.Done(); bm25Results = ur.textSearch(ctx, query, opts) }()
    go func() { defer wg.Done(); graphResults = ur.graphWalk(ctx, query, opts) }()
    go func() { defer wg.Done(); proceduralRes = ur.proceduralMatch(ctx, query, opts) }()
    wg.Wait()

    // 3. 融合排序 — 不是 RRF，是学出来的权重
    fused := ur.fuse(vectorResults, bm25Results, graphResults, proceduralRes)

    // 4. 精排 (可选，使用轻量级交叉编码器)
    if opts.Rerank && len(fused) > opts.RerankTopK {
        fused = ur.reranker.Rerank(ctx, query, fused[:opts.RerankTopK])
    }

    return &SearchResult{Memories: fused[:opts.Limit]}, nil
}

// fuse 使用学到的权重融合多种信号
func (ur *UnifiedRetriever) fuse(results ...[]*ScoredMemory) []*ScoredMemory {
    // 将所有结果放入 map，按 ID 去重并加权求和
    scores := make(map[string]*ScoredMemory)
    for i, batch := range results {
        for _, m := range batch {
            if existing, ok := scores[m.ID]; ok {
                existing.Score += m.Score * ur.signalWeight(i)
            } else {
                m.Score *= ur.signalWeight(i)
                scores[m.ID] = m
            }
        }
    }
    // 应用时间衰减和强度偏置
    for _, m := range scores {
        m.Score *= ur.recencyDecay(m.LastAccess)
        m.Score *= (0.5 + 0.5*m.Strength) // strength 偏置
    }
    // 排序
    sort.Slice(scores, ...) // by score desc
    return scores
}
```

**关键改进:**
- 融合权重通过用户反馈在线学习（点击率、采纳率、满意度）
- 图谱关系加成：如果记忆 A 和记忆 B 有 "contradicts" 关系，且用户选择了 A，B 的权重自动降低
- 检索延迟从 4 次串行变 1 次并行，延迟降低 60%+

### 3.4 程序记忆 — Agent 学会"怎么做事"

这是现有系统完全缺失的记忆类型。

```go
// internal/cortex/procedural.go

// StrategyRecord 记录一次成功的任务执行策略
type StrategyRecord struct {
    TaskPattern     string        `json:"task_pattern"`     // 任务类型指纹
    ToolSequence    []string      `json:"tool_sequence"`    // 使用的工具序列
    PlanTemplate    string        `json:"plan_template"`    // 规划模板
    ContextHints    []string      `json:"context_hints"`    // 关键上下文提示
    SuccessRate     float64       `json:"success_rate"`     // 历史成功率
    AvgDurationMs   int64         `json:"avg_duration_ms"`
    LastUsed        time.Time     `json:"last_used"`
}

// ProceduralMatcher 将当前任务匹配到历史成功策略
type ProceduralMatcher struct {
    store       Store
    embedder    EmbeddingProvider
    minScore    float64  // 最低匹配阈值
}

func (pm *ProceduralMatcher) Match(ctx context.Context, taskDescription string) ([]*StrategyRecord, error) {
    // 1. 将任务描述向量化
    taskEmb, _ := pm.embedder.Embed(ctx, taskDescription)

    // 2. 检索相似的过去成功策略
    memories, _ := pm.store.SearchSimilar(ctx, taskEmb, 5,
        WithType(Procedural),
        WithMinSuccessRate(0.7),
    )

    // 3. 返回策略记录
    var strategies []*StrategyRecord
    for _, m := range memories {
        if m.Strategy != nil {
            strategies = append(strategies, m.Strategy)
        }
    }
    return strategies, nil
}

// InjectProceduralHints 将匹配到的策略注入系统提示词
func (pm *ProceduralMatcher) InjectProceduralHints(taskDescription string) string {
    strategies, err := pm.Match(context.Background(), taskDescription)
    if err != nil || len(strategies) == 0 {
        return ""
    }

    var sb strings.Builder
    sb.WriteString("\n## Past Successful Strategies\n")
    sb.WriteString("You have successfully handled similar tasks before. Consider these approaches:\n\n")
    for i, s := range strategies {
        fmt.Fprintf(&sb, "%d. Pattern: %s\n", i+1, s.TaskPattern)
        fmt.Fprintf(&sb, "   Tools: %s\n", strings.Join(s.ToolSequence, " → "))
        fmt.Fprintf(&sb, "   Success rate: %.0f%%\n", s.SuccessRate*100)
        if len(s.ContextHints) > 0 {
            sb.WriteString("   Tips:\n")
            for _, hint := range s.ContextHints {
                fmt.Fprintf(&sb, "     - %s\n", hint)
            }
        }
        sb.WriteString("\n")
    }
    return sb.String()
}
```

**效果:** Agent 第二次遇到类似任务时，不再从零开始规划，而是直接参考历史成功策略。这是"经验学习"的基础设施。

### 3.5 工作记忆 — 注意力驱动的上下文管理

这是现有 context_manager 的升级版，不是简单地压缩/截断，而是**有选择地保留**。

```go
// internal/cortex/working_memory.go

// WorkingMemory 管理当前认知循环的活跃上下文
type WorkingMemory struct {
    items       []*WorkingMemoryItem
    maxTokens   int
    totalTokens int
    attention   *AttentionScorer
}

// WorkingMemoryItem 是上下文中的一个片段
type WorkingMemoryItem struct {
    Content    string
    TokenCount int
    Importance float64  // 注意力分数
    Source     string   // "user_message", "tool_result", "memory_retrieval", "plan"
    Age        int      // 加入后的轮次计数
}

// AttentionScorer 计算每个上下文片段的注意力分数
// 使用简单的启发式，不需要 GPU：
//   - 和当前任务相关的记忆 → 分数高
//   - 已经被多次引用的信息 → 分数高
//   - 旧的工具输出 → 分数低（除非被后续引用）
//   - 用户刚刚说的 → 分数高
type AttentionScorer struct {
    embedder     EmbeddingProvider
    taskEmbedding []float32
    citationCount map[string]int  // ID → 被引用次数
}

func (wm *WorkingMemory) Evict() {
    // 当 token 预算超限时，按分数从低到高驱逐
    // 不是简单截断，而是保留高分片段
    sort.Slice(wm.items, func(i, j int) bool {
        return wm.items[i].Importance > wm.items[j].Importance
    })

    for wm.totalTokens > wm.maxTokens && len(wm.items) > 0 {
        last := wm.items[len(wm.items)-1]
        wm.totalTokens -= last.TokenCount
        wm.items = wm.items[:len(wm.items)-1]
    }
}
```

### 3.6 睡眠巩固引擎 — 离线记忆处理

```go
// internal/cortex/consolidation.go

// ConsolidationEngine 在后台运行，模仿睡眠的记忆巩固过程
type ConsolidationEngine struct {
    store        Store
    embedder     EmbeddingProvider
    llm          Completer  // 用于生成摘要和抽象
    config       ConsolidationConfig
}

type ConsolidationConfig struct {
    Interval           time.Duration  // 运行间隔，默认 6 小时
    MinAge             time.Duration  // 多久的记忆才处理，默认 24 小时
    MinStrengthForPromotion float64   // 强度阈值，超过则提升到 user scope
    MaxStrengthForArchival float64   // 强度阈值，低于则归档
    MergeSimilarThreshold float64    // 相似度阈值，超过则合并
}

// Run 执行一次巩固周期
func (ce *ConsolidationEngine) Run(ctx context.Context) error {
    // Phase 1: 遗忘 — 衰减低价值记忆
    ce.decay(ctx)

    // Phase 2: 抽象 — 将相似记忆合并为更高级别的总结
    ce.abstract(ctx)

    // Phase 3: 提升 — 将 session 级高价值记忆提升到 user 级
    ce.promote(ctx)

    // Phase 4: 归档 — 将长期未访问的低强度记忆归档
    ce.archive(ctx)

    // Phase 5: 图谱修剪 — 清理孤立节点和过期关系
    ce.pruneGraph(ctx)

    return nil
}

// abstract 找到内容相似且时间相近的记忆，合成更高层次的摘要
func (ce *ConsolidationEngine) abstract(ctx context.Context) {
    // 1. 找到近 24h 的所有 episodic 记忆
    recentMemories, _ := ce.store.Search(ctx, MemorySearch{
        Types:      []MemoryType{Episodic},
        CreatedAfter: time.Now().Add(-24 * time.Hour),
    })

    // 2. 聚类 — 用嵌入相似度分组
    clusters := ce.clusterBySimilarity(recentMemories, 0.8)

    // 3. 每个聚类生成一个摘要记忆
    for _, cluster := range clusters {
        if len(cluster) < 3 {
            continue  // 少于 3 个记忆不值得抽象
        }
        // 用 LLM 生成摘要
        summary := ce.summarizeCluster(ctx, cluster)
        // 保存为新的 semantic 记忆
        ce.store.Save(ctx, &Memory{
            Type:    Semantic,
            Scope:   cluster[0].Scope,
            Content: summary,
            Relations: ce.buildClusterRelations(cluster),
        })
        // 降低原始记忆的强度（它们被摘要代表了）
        for _, m := range cluster {
            m.Strength *= 0.5
            ce.store.Update(ctx, m)
        }
    }
}
```

---

## 四、迁移路径

### Phase 1: 接口抽象（第 1 周）
- 定义 `internal/cortex/` 的完整接口
- 用现有 `memory.Store` + `knowledge.Searcher` + `graph.Graph` 实现桥接适配器
- 认知循环改为调用 `cortex.UnifiedRetriever`
- **此时行为不变，但代码已切换到新接口**

### Phase 2: 向量存储升级（第 2 周）
- 实现嵌入式向量索引（Product Quantization + IVF）
- 替换现有的向量检索（从全表扫描到 ANN）
- 新增 `Procedural` 记忆类型

### Phase 3: 统一检索融合（第 3 周）
- 实现 `UnifiedRetriever`
- 可学习的融合权重（初始等权重，随反馈调整）
- 在线权重更新

### Phase 4: 巩固引擎（第 4 周）
- 实现 `ConsolidationEngine` 的后台循环
- 抽象、提升、遗忘、归档
- Markdown 导出（兼容现有的 MEMORY.md 可读性）

---

## 五、验收标准

1. **统一检索延迟**: 单次查询 < 100ms（当前约 400ms，4 次串行检索）
2. **检索质量**: 在人工标注的 100 条查询上，NDCG@5 比当前系统提升 20%+
3. **程序记忆生效**: Agent 第二次处理同类任务时，成功使用历史策略的概率 > 60%
4. **巩固自动化**: 睡眠巩固后，session 记忆自动抽象为 user 记忆，无需手动管理
5. **向后兼容**: 现有的 MEMORY.md 文件仍可读取和导出
