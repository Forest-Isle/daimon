# 04 - Memory 记忆系统

## 文件结构

```
internal/memory/
├── store.go              # 核心接口 (Store, Entry, SearchQuery, MemoryConfig)
├── file_store.go         # 文件存储实现 (FileMemoryStore)
├── markdown.go           # Markdown + YAML frontmatter 解析
├── memory_index.go       # SQLite 辅助索引 (FTS5 + 向量)
├── facts.go              # LLM 事实提取 (LLMFactExtractor)
├── lifecycle.go          # 生命周期管理 (ADD/UPDATE/DELETE/NOOP)
├── forgetting_curve.go   # 遗忘曲线 (强度衰减/自动归档)
├── consolidator.go       # 会话→用户提升
├── compactor.go          # 后台压缩 (合并同类记忆)
├── compressor.go         # 增量压缩器 (工具输出压缩)
├── reflector.go          # L1/L2 反射 (元认知)
├── profiler.go           # 用户画像生成
├── embedding.go          # 嵌入接口 (EmbeddingProvider)
├── openai.go             # OpenAI 嵌入实现
├── cached_embedder.go    # 嵌入缓存层
├── access_log.go         # 访问日志
├── audit.go              # 审计日志
├── privacy.go            # 隐私/敏感度控制
└── *_test.go             # 测试文件
```

## 一、存储架构

### 双层存储设计

```
┌─────────────────────────────────────────────────┐
│                  查询入口                        │
│                                                  │
│           memory.Store.Search()                  │
│                    │                             │
│         ┌─────────┴─────────┐                   │
│         ▼                   ▼                    │
│  ┌──────────────┐   ┌──────────────┐            │
│  │ MEMORY.md    │   │ SQLite Index │            │
│  │ (索引文件)   │   │ (辅助检索)   │            │
│  └──────┬───────┘   └──────┬───────┘            │
│         │                  │                     │
│         │           ┌──────┴──────┐              │
│         │           │  memory_fts │ FTS5 全文    │
│         │           │  memory_emb │ 向量搜索     │
│         │           └──────┬──────┘              │
│         │                  │                     │
│         └────────┬─────────┘                     │
│                  │ RRF 融合                       │
│                  │ + 强度加权                     │
│                  ▼                                │
│  ┌──────────────────────────────┐                │
│  │ Top-K Markdown 文件          │                │
│  │ ~/.ironclaw/memory/          │                │
│  │ ├── user/                    │ ← 长期记忆     │
│  │ ├── session/                 │ ← 会话记忆     │
│  │ ├── feedback/                │ ← 反馈记忆     │
│  │ ├── global/                  │ ← 全局记忆     │
│  │ └── archived/                │ ← 归档记忆     │
│  └──────────────────────────────┘                │
└─────────────────────────────────────────────────┘
```

### 主存储：Markdown 文件

文件格式：
```markdown
---
id: "mem_1234567890"
scope: "user"
user_id: "alice"
created_at: 2025-01-15T10:30:00Z
updated_at: 2025-01-15T10:30:00Z
last_accessed_at: 2025-01-20T14:00:00Z
strength: 0.85
type: "semantic"
importance: 7
emotion: "positive"
sensitivity: "public"
related_to: "mem_1234567889"
---

用户偏好使用中文进行技术讨论，擅长 Go 和 Python 编程。
```

文件命名：`{scope}/{category}_{YYYYMMDD}_{id}.md`

### 辅助索引：SQLite

三张表（migration 006）：

| 表名 | 用途 |
|------|------|
| `memory_index` | 文件路径 → 元数据映射 |
| `memory_fts` | FTS5 虚拟表，BM25 全文搜索 |
| `memory_embeddings` | 向量嵌入，余弦相似度搜索 |

## 二、核心接口

```go
type Store interface {
    Save(ctx, entry Entry) error
    Search(ctx, query SearchQuery) ([]SearchResult, error)
    ListByScope(ctx, scope, userID) ([]Entry, error)
    Update(ctx, id, content string, version int) error
    Delete(ctx, id string) error
}

type Entry struct {
    ID, SessionID, UserID string
    Scope     MemoryScope    // session | user | global
    Content   string         // 蒸馏后的事实
    Embedding []float32
    Metadata  map[string]string
    Version   int
    ExpiresAt *time.Time
    CreatedAt, UpdatedAt time.Time
}

type SearchQuery struct {
    Text       string
    Embedding  []float32
    Limit      int
    SessionID  string        // 可选：限定会话
    UserID     string        // 可选：限定用户
    Scopes     []MemoryScope // 可选：限定作用域
    TypeFilter string        // 可选：限定类型
}
```

## 三、搜索流程

```
Search(query)
    │
    ├── 1. 解析 MEMORY.md 索引
    ├── 2. FTS5 全文搜索 (BM25 排序)
    ├── 3. 向量余弦相似度搜索 (if embedder != noop)
    ├── 4. RRF 融合 (Reciprocal Rank Fusion)
    │       score = bm25_weight / (k + bm25_rank) + vector_weight / (k + vector_rank)
    │       k = 60
    ├── 5. 强度加权 (遗忘曲线 strength)
    ├── 6. 取 Top-K 文件路径
    └── 7. 读取对应 Markdown 文件内容
```

## 四、事实提取（facts.go）

```go
type LLMFactExtractor struct {
    completer Completer    // LLM 补全接口
    cfg       MemoryConfig
}

type ExtractedFact struct {
    Content   string
    Type      string  // episodic | semantic | procedural
    Importance int    // 1-10
    Emotion    string // positive | negative | neutral
}
```

用户消息 → LLM 提取 → 结构化事实列表

## 五、生命周期管理（lifecycle.go）

核心设计来源于 **mem0** 项目：

```
新事实候选
    │
    ├── 1. 嵌入向量化
    ├── 2. 相似度搜索现有记忆
    ├── 3. LLM 决策
    │   │
    │   ├── ADD     → 新事实是新颖的，存储
    │   ├── UPDATE  → 新事实取代旧记忆 (target_id)
    │   ├── DELETE  → 新事实使旧记忆无效 (target_id)
    │   └── NOOP    → 已有相同记忆，忽略
    │
    ├── 4. 冲突检测 (conflicting_ids)
    ├── 5. 关联检测 (related_to)
    └── 6. 执行操作 + 同步知识图谱
```

## 六、遗忘曲线（forgetting_curve.go）

基于**艾宾浩斯遗忘曲线**的记忆强度管理：

```
强度计算:
    strength = f(last_accessed_at, access_frequency)

自动归档:
    if strength < 0.3 → 移至 archived/

后台任务:
    每 24 小时运行一次
    ├── FadeWeakMemoriesFromFiles()     # 衰减弱记忆
    └── FadeByRetentionPolicy()         # 按保留策略执行
```

保留策略（按记忆类型）：
- `episodic`：默认 30 天（可配置 `retention_episodic`）
- `semantic`：默认 365 天
- `procedural`：永不自动删除

## 七、会话整合（consolidator.go）

```
session 记忆（短期）→ user 记忆（长期）

触发条件:
    ├── 会话创建 > 24h
    └── 记忆强度 ≥ 0.5

操作:
    ├── 文件从 session/ 目录移至 user/ 目录
    ├── 更新 PromotedFrom 字段
    └── 更新索引
```

## 八、反射系统（reflector.go）

两级反射（元认知）：

### L1 反射
- **触发**：未反射事实数 ≥ `reflection_count_threshold` (默认 10)
- **内容**：综合最近事实，发现模式和主题
- **输出**：reflection 类型的记忆文件

### L2 反射
- **触发**：L1 反射数 ≥ `reflection_l2_trigger` (默认 5)
- **内容**：综合多个 L1 反射，生成更高层次的洞察
- **输出**：更抽象的 reflection 记忆

### 语义漂移检测
- 当新事实与现有记忆的余弦相似度 < `reflection_drift_threshold` (默认 0.7) 时触发反射

## 九、压缩系统

### Compactor（compactor.go）
后台定期运行，合并同类别过多的记忆：

```
每 6 小时检查:
    if category 下记忆数 >= compaction_threshold (默认 8):
        LLM 合并为 summary 类型记忆
        归档旧记忆
```

### IncrementalCompressor（compressor.go）
工具输出压缩：当工具返回结果过长时，截断或摘要化以减少 token 消耗。

## 十、用户画像（profiler.go）

从用户记忆中生成/更新用户画像文件：
```
~/.ironclaw/memory/user/profile_{userID}.md
```

由 ReflectionTracker 回调触发（非后台任务）。

## 十一、隐私控制（privacy.go）

记忆敏感度分级：
- `public`：可自由检索
- `private`：仅用户本人可检索
- `secret`：不进入搜索索引

## 十二、嵌入系统

```
EmbeddingProvider (接口)
    │
    ├── OpenAIEmbedding      # OpenAI text-embedding 模型
    ├── CachedEmbedder       # LRU 缓存层包装
    └── NoopEmbedding        # 空实现（降级为纯文本搜索）
```

## 设计亮点

1. **文件优先**：Markdown 文件作为主存储，人类可读可编辑
2. **双检索融合**：FTS5 + 向量 + RRF 融合，兼顾精确匹配和语义相似
3. **仿生记忆**：遗忘曲线 + 会话整合 + 反射系统，模拟人类记忆机制
4. **生命周期管理**：借鉴 mem0 的 ADD/UPDATE/DELETE/NOOP 决策循环
5. **渐进式降级**：无 OpenAI key → 纯 BM25；无 FTS5 → LIKE 查询
