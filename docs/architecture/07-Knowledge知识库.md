# 07 - Knowledge 知识库与知识图谱

## 文件结构

```
internal/knowledge/
├── store.go        # SQLiteKnowledgeBase (BM25 + 向量混合检索)
├── chunk.go        # 文档分块
├── reranker.go     # LLM 重排器
├── cache.go        # 搜索结果缓存
├── ingest/
│   ├── ingest.go   # 摄取管线入口
│   ├── text.go     # 纯文本摄取
│   ├── markdown.go # Markdown 摄取
│   ├── code.go     # 代码文件摄取
│   ├── pdf.go      # PDF 文档摄取
│   └── web.go      # 网页摄取
└── graph/
    ├── graph.go     # SQLiteGraph (实体-关系三元组)
    ├── extractor.go # LLM 实体提取器
    ├── sync.go      # Memory↔Graph 同步
    └── decay.go     # 关系强度衰减
```

## 一、知识库（Knowledge Base）

### 架构

```
┌────────────────────────────────────────────────┐
│              IngestPipeline                     │
│                                                 │
│  文件/目录 → 检测类型 → 解析器 → 分块 → 入库    │
│                                                 │
│  ┌─────────────────────────────────────────┐    │
│  │ 解析器                                  │    │
│  │ text │ markdown │ code │ pdf │ web      │    │
│  └──────────────────────┬──────────────────┘    │
│                         ▼                       │
│  ┌─────────────────────────────────────────┐    │
│  │ Chunker                                 │    │
│  │ chunk_size=512, overlap=64              │    │
│  └──────────────────────┬──────────────────┘    │
│                         ▼                       │
│  ┌─────────────────────────────────────────┐    │
│  │ SQLite (kb_chunks + kb_chunks_fts)      │    │
│  │ + 向量嵌入 (kb_embeddings)              │    │
│  └─────────────────────────────────────────┘    │
└────────────────────────────────────────────────┘
```

### 混合检索

```
Search(query)
    │
    ├── FTS5 BM25 搜索 (if fts5Available)
    │   └── SELECT ... FROM kb_chunks_fts WHERE kb_chunks_fts MATCH ?
    │
    ├── 向量余弦相似度搜索 (if embedder != noop)
    │   └── 对比 kb_embeddings 中的向量
    │
    ├── RRF 融合
    │   score = bm25_weight/(k+rank_bm25) + vector_weight/(k+rank_vector)
    │   k = 60, bm25_weight = 0.4, vector_weight = 0.6
    │
    └── 返回 Top-K 结果
```

### HybridRetriever

```go
type HybridRetriever struct {
    kb       *SQLiteKnowledgeBase
    reranker Reranker
}

// 先混合检索，再 LLM 重排
func (r *HybridRetriever) Search(ctx, query) ([]Result, error) {
    results := r.kb.Search(ctx, query)
    return r.reranker.Rerank(ctx, query.Text, results)
}
```

### LLM 重排器（reranker.go）

```
初始检索结果 (Top-20)
    │
    ├── 构建重排提示：
    │   "给定查询和以下文档片段，按相关性排序..."
    │
    ├── LLM 返回排序后的 ID 列表
    │
    └── 返回重排后的 Top-K 结果
```

### 搜索缓存（cache.go）

```
KnowledgeSearchCache
    ├── LRU 缓存 (max size 可配)
    ├── TTL 过期 (默认 5 分钟)
    └── 缓存 key = query hash
```

## 二、文档摄取管线（ingest/）

### 支持的文档类型

| 类型 | 文件 | 分块策略 |
|------|------|----------|
| 纯文本 | text.go | 按段落/固定大小分块 |
| Markdown | markdown.go | 按标题层级分块 |
| 代码 | code.go | 按函数/类分块 |
| PDF | pdf.go | 按页/段落分块 |
| 网页 | web.go | HTML 解析后按段落分块 |

### 分块参数

```yaml
knowledge:
  chunk_size: 512      # 每块最大 token 数
  chunk_overlap: 64    # 块间重叠
```

### 目录摄取

```
IngestDir(dir)
    │
    ├── 遍历目录
    ├── 检测文件类型
    ├── 选择解析器
    ├── 解析为文本
    ├── 分块
    ├── 生成嵌入
    └── 存入 SQLite
```

## 三、知识图谱（graph/）

### 数据模型

```
实体 (Entity)
├── id
├── name
├── type (person, concept, tool, etc.)
├── properties (JSON)
└── created_at

关系 (Relation)
├── id
├── source_entity_id
├── target_entity_id
├── relation_type (uses, depends_on, related_to, etc.)
├── strength (0.0-1.0)
├── properties (JSON)
└── created_at
```

### SQLiteGraph

```go
type Graph interface {
    AddEntity(ctx, entity) error
    AddRelation(ctx, relation) error
    FindRelated(ctx, entityName, depth int) ([]Relation, error)
    Search(ctx, query string) ([]Entity, error)
}
```

**递归 CTE 遍历**：`FindRelated` 使用 SQLite 的 `WITH RECURSIVE` 实现多跳图遍历。

### LLM 实体提取（extractor.go）

```
Extract(ctx, content, sourceType, sourceID)
    │
    ├── LLM 提取实体和关系:
    │   "从以下文本中提取实体和关系..."
    │
    ├── 解析 JSON 响应
    │
    ├── 存储实体 (AddEntity)
    └── 存储关系 (AddRelation)
```

### GraphSync（sync.go）

Memory 生命周期与知识图谱的同步：

```go
type GraphSync struct {
    graph     Graph
    extractor *LLMEntityExtractor
}

func (s *GraphSync) SyncOnAdd(ctx, factID, content) error    // ADD → 提取实体
func (s *GraphSync) SyncOnUpdate(ctx, old, new, content) error // UPDATE → 重提取
func (s *GraphSync) SyncOnDelete(ctx, factID) error            // DELETE → 清理
```

### GraphDecayTask（decay.go）

```
每 24 小时运行:
    ├── 遍历所有关系
    ├── 降低 strength (衰减因子)
    └── strength < 阈值 → 删除关系
```

## 四、在 Cognitive 模式中的使用

```
PERCEIVE 阶段:
    ├── knowledge.Searcher.Search()  → KnowledgeContext
    └── graph.FindRelated()          → GraphContext

这些上下文注入到 CognitiveState 中，
供 PLAN 和 REFLECT 阶段使用。
```

## 设计亮点

1. **管线式摄取**：统一的文档处理管线，易于扩展新类型
2. **混合检索 + 重排**：BM25 召回 + 向量精排 + LLM 重排三阶段
3. **图谱与记忆联动**：GraphSync 确保记忆变更同步到图谱
4. **时间衰减**：关系强度随时间自然衰减，保持图谱时效性
5. **递归遍历**：CTE 支持多跳关系查询
