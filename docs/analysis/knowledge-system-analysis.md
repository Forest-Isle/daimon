# IronClaw 知识系统 — 完整实现分析

## 目录

- [1. 架构总览](#1-架构总览)
- [2. 核心接口与数据类型](#2-核心接口与数据类型)
- [3. 文本分块策略](#3-文本分块策略)
- [4. 文档摄入管道](#4-文档摄入管道)
- [5. 知识库：混合检索 + RRF 融合](#5-知识库混合检索--rrf-融合)
- [6. HybridRetriever + LLM Reranker](#6-hybridretriever--llm-reranker)
- [7. 知识图谱：实体关系与时序版本](#7-知识图谱实体关系与时序版本)
- [8. 图谱实体提取](#8-图谱实体提取)
- [9. 图谱同步：记忆 ↔ 图谱](#9-图谱同步记忆--图谱)
- [10. 图谱衰减：后台维护](#10-图谱衰减后台维护)
- [11. 数据库 Schema](#11-数据库-schema)
- [12. 推荐阅读顺序](#12-推荐阅读顺序)

---

## 1. 架构总览

```
┌─────────────────────────────────────────────────────────────┐
│                     知识系统全景                              │
│                                                             │
│  ┌──────────────────┐     ┌───────────────────────────┐    │
│  │  文档摄入管道      │     │   知识图谱                  │    │
│  │  (IngestPipeline) │     │   (SQLiteGraph)            │    │
│  │                   │     │                            │    │
│  │  URI → 解析 →     │     │  Node ──Edge──▶ Node      │    │
│  │  分块 → 嵌入 →    │     │   │     ▲                  │    │
│  │  存储             │     │   │  Provenance            │    │
│  └────────┬──────────┘     │   ▼     │                  │    │
│           │                │  ┌──────┴──────┐           │    │
│           ▼                │  │LLM Entity   │           │    │
│  ┌────────────────────┐   │  │Extractor    │           │    │
│  │  SQLiteKnowledgeBase│   │  └──────┬──────┘           │    │
│  │                    │    │         │                   │    │
│  │  kb_sources        │    │  ┌──────▼──────┐           │    │
│  │  kb_chunks         │    │  │ GraphSync   │           │    │
│  │  kb_chunks_fts     │    │  │ (记忆↔图谱)  │           │    │
│  │  kb_embeddings     │    │  └──────┬──────┘           │    │
│  └────────┬───────────┘   │         │                   │    │
│           │                │  ┌──────▼──────┐           │    │
│           ▼                │  │ GraphDecay  │           │    │
│  ┌────────────────────┐   │  │ (后台衰减)   │           │    │
│  │  HybridRetriever   │   │  └─────────────┘           │    │
│  │                    │    └───────────────────────────┘    │
│  │  Search → RRF →   │                                     │
│  │  Rerank            │                                     │
│  └────────────────────┘                                     │
└─────────────────────────────────────────────────────────────┘
```

---

## 2. 核心接口与数据类型

📄 **文件**: `internal/knowledge/knowledge.go`

### 2.1 Source — 文档源

```go
type Source struct {
    ID         string            // 唯一 ID
    URI        string            // 源位置（文件路径或 URL）
    SourceType string            // "markdown" | "pdf" | "code" | "web" | "text"
    Title      string            // 提取或推断的标题
    ChunkCount int               // 产生的块数
    Metadata   map[string]string
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### 2.2 Chunk — 文本块

```go
type Chunk struct {
    ID         string            // 唯一 ID
    SourceID   string            // 外键 → Source
    SourceURI  string            // 源 URI（冗余存储，方便查询）
    SourceType string
    Content    string            // 实际文本内容
    Embedding  []float32         // 向量嵌入（二进制序列化到 BLOB）
    ChunkIndex int               // 在源中的位置（0 起始）
    Metadata   map[string]string
    CreatedAt  time.Time
}
```

### 2.3 KnowledgeBase 接口

```go
type KnowledgeBase interface {
    Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
    Ingest(ctx context.Context, uri, sourceType string) error
    Sources(ctx context.Context) ([]Source, error)
    DeleteSource(ctx context.Context, sourceID string) error
}

type Searcher interface {
    Search(ctx context.Context, query KnowledgeQuery) ([]KnowledgeResult, error)
}
```

`KnowledgeBase` 由 `SQLiteKnowledgeBase` 实现。`Searcher` 是轻量检索接口，同时被 `SQLiteKnowledgeBase` 和 `HybridRetriever` 实现。

---

## 3. 文本分块策略

📄 **文件**: `internal/knowledge/chunk.go`

```go
type ChunkStrategy struct {
    ChunkSize    int  // 目标大小（rune 数），默认 512
    ChunkOverlap int  // 相邻块重叠（rune 数），默认 64
}
```

**分块算法** — `ChunkText(text, strategy)`：

```
1. 优先在句子边界（. ? ! \n）处分割
2. 在 chunk 末尾 20% 范围内搜索句子边界
3. 找不到边界 → 在 ChunkSize 处硬切
4. 应用 ChunkOverlap 保持上下文连续性
5. 过滤空块
```

**示例**（ChunkSize=40, Overlap=10）：

```
原文: "Alice works at Company A. Bob works at Company B. Charlie knows Alice."

输出:
  [0] "Alice works at Company A. Bob works at"
  [1] "at Company B. Charlie knows Alice."
        ^重叠部分^
```

---

## 4. 文档摄入管道

📄 **核心文件**: `internal/knowledge/pipeline.go` + `internal/knowledge/ingest/`

### 4.1 Ingester 接口

```go
type Ingester interface {
    CanHandle(sourceType string) bool
    Extract(ctx context.Context, uri string) (title string, content string, err error)
}
```

### 4.2 内置 Ingester

| Ingester | 文件 | 处理类型 | 说明 |
|----------|------|---------|------|
| `MarkdownIngester` | `ingest/markdown.go` | `.md` | 剥离 Markdown 格式 → 纯文本 |
| `CodeIngester` | `ingest/code.go` | `.go/.py/.js/...` | 添加文件路径 + 语言注释 |
| `WebIngester` | `ingest/web.go` | `http(s)://` | 抓取 HTML → 纯文本（30s 超时，5MB 限制） |
| `PlainTextIngester` | `ingest/text.go` | `.txt` | 直接读取 |
| `PDFIngester` | `ingest/pdf.go` | `.pdf` | 桩实现，返回 "not yet supported" |

**DetectSourceType(uri)** 自动推断源类型：
- `http/https` → `"web"`
- `.pdf` → `"pdf"`
- `.md` → `"markdown"`
- 代码扩展名 → `"code"`
- 默认 → `"text"`

### 4.3 管道完整流程

📄 **文件**: `internal/knowledge/pipeline.go` — `Ingest` 方法

```
IngestPipeline.Ingest(ctx, uri, sourceType)
     │
     ├─ 1. 检测源类型（如果未提供）
     │
     ├─ 2. registry.Extract(uri) → (title, content)
     │     └─ 路由到对应 Ingester
     │
     ├─ 3. kb.saveSource(uri, sourceType, title) → sourceID
     │     └─ UPSERT kb_sources 表
     │
     ├─ 4. ChunkText(content, strategy) → []chunks
     │
     ├─ 5. 对每个 chunk：
     │     ├─ 创建 Chunk 结构体
     │     └─ kb.saveChunk(chunk)
     │         ├─ 如果有 embedder → Embed(chunk.Content)
     │         ├─ 序列化 embedding → BLOB
     │         └─ INSERT INTO kb_chunks
     │
     └─ 6. kb.updateChunkCount(sourceID)
```

**批量摄入**: `IngestDir(dir)` 扫描目录下所有文件并逐个摄入。

---

## 5. 知识库：混合检索 + RRF 融合

📄 **核心文件**: `internal/knowledge/store.go`

### 5.1 SQLiteKnowledgeBase

```go
type SQLiteKnowledgeBase struct {
    db            *store.DB
    embedder      EmbeddingProvider
    fts5Available bool                  // FTS5 可用性探测结果
    cfg           Config
    pipeline      *IngestPipeline
    searchCache   *KnowledgeSearchCache // 可选 LRU 缓存
}

type Config struct {
    ChunkSize         int           // 默认 512
    ChunkOverlap      int           // 默认 64
    BM25Weight        float64       // 默认 0.4
    VectorWeight      float64       // 默认 0.6
    IngestDirs        []string      // 摄入目录列表
    EnableSearchCache bool
    SearchCacheSize   int           // 默认 500
    SearchCacheTTL    time.Duration // 默认 5min
}
```

### 5.2 Search 完整流程

```
kb.Search(ctx, KnowledgeQuery{Text, Embedding, Limit, SourceType})
     │
     ├─ 1. 检查 searchCache（如果启用）
     │
     ├─ 2. 如果 query.Embedding 为空 → Embed(query.Text)
     │
     ├─ 3. vectorSearch → 返回 3×Limit 个结果
     │     ├─ 查询所有有 embedding 的 chunks
     │     ├─ 计算 cosineSimilarity(query, chunk)
     │     └─ 按相似度降序排列
     │
     ├─ 4. fts5Search 或 likeSearch → 返回 3×Limit 个结果
     │     ├─ FTS5 可用 → BM25 排名
     │     └─ FTS5 不可用 → LIKE 子串匹配（降级）
     │
     ├─ 5. RRF 融合：
     │     对每个 chunk ID：
     │       score = vectorW × 1/(K + vector_rank + 1)
     │            + bm25W  × 1/(K + bm25_rank + 1)
     │     其中 K=60, vectorW=0.6, bm25W=0.4
     │
     ├─ 6. 按融合分数排序 → 返回 top-k
     │
     └─ 7. 缓存结果（如果启用）
```

### 5.3 RRF 公式详解

```
score(chunk) = 0.6 × 1/(60 + vRank + 1) + 0.4 × 1/(60 + bRank + 1)
```

- `vRank` = 向量搜索排名（-1 如果不在结果中）
- `bRank` = BM25 排名（-1 如果不在结果中）
- K=60 平衡排名靠前和靠后的结果
- 向量权重 0.6 > BM25 权重 0.4：语义匹配优先

**对比记忆系统**：记忆系统额外加入了 `strength × 0.3` 的记忆强度权重。

### 5.4 搜索缓存

📄 **文件**: `internal/knowledge/cache.go`

```go
type KnowledgeSearchCache struct {
    mu      sync.RWMutex
    cache   map[string]*knowledgeCacheEntry  // SHA256(query) → 结果
    maxSize int
    ttl     time.Duration
}
```

- 缓存 key = SHA256(text + sourceType)
- LRU 淘汰策略
- TTL 过期自动清理
- 新文档摄入时调用 `Invalidate()` 清空全部缓存

---

## 6. HybridRetriever + LLM Reranker

📄 **文件**: `internal/knowledge/retriever.go` + `internal/knowledge/reranker.go`

### 6.1 HybridRetriever

```go
type HybridRetriever struct {
    kb       *SQLiteKnowledgeBase
    reranker Reranker
}

func (h *HybridRetriever) Search(ctx, query) ([]KnowledgeResult, error) {
    results := h.kb.Search(ctx, query)     // 第一步：混合检索 + RRF
    return h.reranker.Rerank(ctx, query.Text, results)  // 第二步：重排序
}
```

### 6.2 LLM Reranker

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, results []KnowledgeResult) ([]KnowledgeResult, error)
}

type NoopReranker struct{}   // 不排序，直接返回
type LLMReranker struct {
    completer Completer
}
```

**LLM Reranker 流程**：

```
LLMReranker.Rerank(ctx, query, results)
     │
     ├─ 如果结果 ≤ 1 → 直接返回
     │
     ├─ 格式化输入：
     │   "QUERY: <user_query>
     │    DOCUMENTS:
     │    [0] ID: chunk_id_1
     │        <前 300 字符>...
     │    [1] ID: chunk_id_2
     │        <前 300 字符>..."
     │
     ├─ 调用 LLM："按相关性排序这些文档"
     │
     ├─ 解析输出：JSON 数组 ["chunk_id_2", "chunk_id_1", ...]
     │
     └─ 按 LLM 返回的顺序重排结果（稳定排序，未排名的放末尾）
```

---

## 7. 知识图谱：实体关系与时序版本

📄 **核心文件**: `internal/knowledge/graph/graph.go` + `internal/knowledge/graph/sqlite_graph.go`

### 7.1 核心类型

```go
type Node struct {
    ID         string             // 唯一 ID
    Type       string             // "person" | "org" | "concept" | "location" | "product"
    Name       string             // 实体名称
    Properties map[string]string  // JSON 属性
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type Edge struct {
    ID         string             // 唯一 ID
    SourceID   string             // 主体节点
    TargetID   string             // 客体节点
    Type       string             // 谓词："knows", "works_at", "related_to", "part_of"
    Weight     float64            // 置信度/强度（0.1 = 弱, 1.0 = 强）
    Properties map[string]string
    CreatedAt  time.Time
    ValidFrom  *time.Time         // 生效时间
    ValidTo    *time.Time         // 失效时间（NULL = 当前有效）
}

type Triple struct {
    Subject   Node               // 主体
    Predicate string             // 谓词
    Object    Node               // 客体
    Weight    float64            // 边权重
}
```

### 7.2 Graph 接口

```go
type Graph interface {
    UpsertNode(ctx, node Node) (string, error)                        // 创建/更新节点
    UpsertEdge(ctx, edge Edge) (string, error)                        // 创建/更新边（时序版本）
    Neighbors(ctx, nodeID, edgeType string) ([]Triple, error)         // 1 跳邻居
    Traverse(ctx, nodeID string, maxDepth int) ([]Triple, error)      // 多跳遍历
    FindNode(ctx, nodeType, name string) (*Node, error)               // 精确查找
    FindByName(ctx, name string) ([]Node, error)                      // 模糊查找
    AddProvenance(ctx, edgeID, sourceType, sourceID string) error     // 溯源记录
}
```

### 7.3 时序版本控制（Temporal Versioning）

📄 **文件**: `internal/knowledge/graph/sqlite_graph.go` — `UpsertEdge` 方法

```
UpsertEdge 对同一 (source, target, type) 的处理：

   旧边:  Alice ──works_at──▶ CompanyA   (valid_from=T1, valid_to=NULL)
                                            │
   UpsertEdge(Alice, CompanyA, works_at)     │
                                            ▼
   旧边:  Alice ──works_at──▶ CompanyA   (valid_from=T1, valid_to=NOW)  ← 失效
   新边:  Alice ──works_at──▶ CompanyA   (valid_from=NOW, valid_to=NULL) ← 新版本
```

这允许**时间穿越查询**：

- `Neighbors(nodeID, edgeType)` — 只查当前有效边（`valid_to IS NULL`）
- `NeighborsAt(nodeID, edgeType, timestamp)` — 查某一时间点有效的边

### 7.4 递归 CTE 多跳遍历

📄 **文件**: `internal/knowledge/graph/sqlite_graph.go` — `Traverse` 方法

```sql
WITH RECURSIVE traverse(source_id, target_id, edge_type, weight, depth) AS (
    -- 基础情况：从起始节点出发的所有边
    SELECT e.source_id, e.target_id, e.type, e.weight, 1
    FROM kg_edges e
    WHERE e.source_id = ? AND e.valid_to IS NULL

    UNION ALL

    -- 递归：沿着已到达节点的边继续前进
    SELECT e.source_id, e.target_id, e.type, e.weight, t.depth + 1
    FROM kg_edges e
    JOIN traverse t ON e.source_id = t.target_id
    WHERE t.depth < ? AND e.valid_to IS NULL
)
SELECT DISTINCT ... FROM traverse JOIN kg_nodes ...
ORDER BY depth
```

只遍历当前有效的边（`valid_to IS NULL`），深度受 `maxDepth` 限制。

---

## 8. 图谱实体提取

📄 **文件**: `internal/knowledge/graph/extractor.go`

### 8.1 LLMEntityExtractor

```go
type LLMEntityExtractor struct {
    graph     Graph
    completer EntityCompleter
}

type RawTriple struct {
    Subject     string  // "Alice"
    SubjectType string  // "person"
    Predicate   string  // "works_at"
    Object      string  // "Company A"
    ObjectType  string  // "org"
}
```

### 8.2 提取流程

```
extractor.Extract(ctx, text, sourceType, sourceID)
     │
     ├─ 1. 截断文本到 3000 字符
     │
     ├─ 2. 调用 LLM（entityExtractionPrompt）
     │     "提取实体和关系，输出 JSON 三元组数组"
     │
     ├─ 3. 解析响应 → []RawTriple
     │
     └─ 4. 对每个三元组：
           ├─ UpsertNode(subject) → subjectID
           ├─ UpsertNode(object) → objectID
           ├─ UpsertEdge(source=subjectID, target=objectID, type=predicate) → edgeID
           └─ AddProvenance(edgeID, sourceType, sourceID)
```

**类型归一化**：`normalizeType()` 将 "organization/company" → "org"，"place/city/country" → "location" 等。

---

## 9. 图谱同步：记忆 ↔ 图谱

📄 **文件**: `internal/knowledge/graph/graph_sync.go`

### 9.1 GraphSync

```go
type GraphSync struct {
    graph     *SQLiteGraph
    extractor *LLMEntityExtractor
}
```

**实现 `memory.GraphSyncer` 接口**，被 LifecycleManager 调用：

| 方法 | 触发时机 | 行为 |
|------|---------|------|
| `SyncOnAdd(factID, content)` | 新记忆创建 | 从内容中提取实体 → 写入图谱 |
| `SyncOnUpdate(oldID, newID, content)` | 记忆更新 | 更新溯源 old→new + 重新提取 |
| `SyncOnDelete(factID)` | 记忆删除 | 移除溯源 + 弱化无支撑的边 |

### 9.2 删除时的边弱化逻辑

```
SyncOnDelete(factID)
     │
     ├─ 找到所有以 factID 为溯源的边
     │
     └─ 对每条边：
         ├─ 移除溯源记录
         ├─ 计算剩余溯源数
         └─ 如果溯源数 = 0 → 边权重设为 0.1（弱化状态）
```

**设计哲学**：不立即删除边，而是弱化它。如果后续有新的证据支撑同一关系，边可以恢复。

---

## 10. 图谱衰减：后台维护

📄 **文件**: `internal/knowledge/graph/graph_decay.go`

```go
type GraphDecayTask struct {
    graph    *SQLiteGraph
    interval time.Duration  // 默认 24h
    done     chan struct{}
}
```

**每 24 小时执行一次衰减循环**：

```
Decay(ctx)
     │
     ├─ 1. 清理孤儿溯源：
     │     删除 source_type='memory' 但 source_id 不在 memory_index 中的记录
     │
     ├─ 2. 衰减无支撑的边：
     │     UPDATE kg_edges SET weight = weight × 0.9
     │     WHERE id NOT IN (SELECT DISTINCT edge_id FROM kg_provenance)
     │     （无溯源支撑的边每天衰减 10%）
     │
     ├─ 3. 删除死边：
     │     DELETE FROM kg_edges WHERE weight < 0.1 AND valid_to IS NOT NULL
     │     （已失效且极弱的边被清理）
     │
     └─ 4. 清理孤儿溯源（再次）：
           DELETE FROM kg_provenance WHERE edge_id NOT IN (SELECT id FROM kg_edges)
```

**衰减速度**：无溯源的边每天 ×0.9，约 22 天降至 0.1 以下被清理。

---

## 11. 数据库 Schema

### Migration 004: 知识库表

📄 **文件**: `internal/store/migrations/004_knowledge_base.sql`

```sql
-- 文档源
CREATE TABLE kb_sources (
    id TEXT PRIMARY KEY,
    uri TEXT NOT NULL UNIQUE,
    source_type TEXT NOT NULL,    -- "markdown" | "pdf" | "code" | "web" | "text"
    title TEXT,
    chunk_count INTEGER DEFAULT 0,
    metadata TEXT DEFAULT '{}',
    created_at, updated_at
);

-- 文本块
CREATE TABLE kb_chunks (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES kb_sources(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    embedding BLOB,              -- float32 序列化
    chunk_index INTEGER,
    ...
);

-- FTS5 全文搜索（触发器自动同步 kb_chunks）
CREATE VIRTUAL TABLE kb_chunks_fts USING fts5(
    content,
    content='kb_chunks',
    content_rowid='rowid',
    tokenize='porter unicode61'
);
```

### Migration 005 + 011: 知识图谱表

📄 **文件**: `internal/store/migrations/005_knowledge_graph.sql` + `011_temporal_graph.sql`

```sql
-- 实体节点
CREATE TABLE kg_nodes (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,           -- "person" | "org" | "concept" | ...
    name TEXT NOT NULL,
    properties TEXT DEFAULT '{}',
    UNIQUE(type, name)
);

-- 关系边（带时序版本）
CREATE TABLE kg_edges (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    type TEXT NOT NULL,           -- "knows" | "works_at" | ...
    weight REAL DEFAULT 1.0,
    valid_from DATETIME,         -- 生效时间
    valid_to DATETIME,           -- 失效时间（NULL = 当前有效）
);
-- 唯一约束：同一 (source, target, type) 只有一条活跃边
CREATE UNIQUE INDEX idx_kg_edges_active
    ON kg_edges(source_id, target_id, type)
    WHERE valid_to IS NULL;

-- 溯源表（边 → 记忆/KB 来源）
CREATE TABLE kg_provenance (
    edge_id TEXT NOT NULL,
    source_type TEXT NOT NULL,    -- "memory" | "kb_chunk"
    source_id TEXT NOT NULL,
    PRIMARY KEY (edge_id, source_id)
);
```

---

## 12. 推荐阅读顺序

### 第一层：核心接口和数据类型

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 1 | `knowledge.go` | `Source`, `Chunk`, `KnowledgeBase`, `Searcher` 接口 |
| 2 | `chunk.go` | `ChunkStrategy`, `ChunkText` 分块算法 |

### 第二层：摄入管道

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 3 | `ingest/ingest.go` | `Ingester` 接口, `Registry`, `DetectSourceType` |
| 4 | `ingest/markdown.go` | Markdown 解析 + `stripMarkdown` |
| 5 | `ingest/web.go` | 网页抓取 + `htmlToText` |
| 6 | `pipeline.go` | `IngestPipeline.Ingest` 完整流程 |

### 第三层：检索系统

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 7 | `store.go` | `SQLiteKnowledgeBase.Search` — 混合检索 + RRF |
| 8 | `cache.go` | `KnowledgeSearchCache` — LRU 缓存 |
| 9 | `retriever.go` | `HybridRetriever` — 检索 + 重排序 |
| 10 | `reranker.go` | `LLMReranker` — LLM 重排序 |

### 第四层：知识图谱

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 11 | `graph/graph.go` | `Node`, `Edge`, `Triple`, `Graph` 接口 |
| 12 | `graph/sqlite_graph.go` | `UpsertEdge`（时序版本）, `Traverse`（递归 CTE） |
| 13 | `graph/extractor.go` | `LLMEntityExtractor` — 实体提取 |
| 14 | `graph/graph_sync.go` | `GraphSync` — 记忆↔图谱同步 |
| 15 | `graph/graph_decay.go` | `GraphDecayTask` — 后台衰减 |

### 第五层：数据库

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 16 | `migrations/004_knowledge_base.sql` | 知识库表 DDL |
| 17 | `migrations/005_knowledge_graph.sql` | 图谱表 DDL |
| 18 | `migrations/011_temporal_graph.sql` | 时序版本迁移 |
