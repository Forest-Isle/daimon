# IronClaw 记忆系统 — 完整实现分析

## 目录

- [1. 架构总览](#1-架构总览)
- [2. 核心数据模型](#2-核心数据模型)
- [3. 存储层：文件 + SQLite 双轨架构](#3-存储层文件--sqlite-双轨架构)
- [4. 事实提取：从对话到记忆](#4-事实提取从对话到记忆)
- [5. 生命周期管理：ADD/UPDATE/DELETE/NOOP](#5-生命周期管理addupdatedeletenoop)
- [6. 混合检索：BM25 + 向量 + RRF 融合](#6-混合检索bm25--向量--rrf-融合)
- [7. 遗忘曲线：记忆强度衰减与自动归档](#7-遗忘曲线记忆强度衰减与自动归档)
- [8. 记忆整合：Session → User 晋升](#8-记忆整合session--user-晋升)
- [9. 记忆压缩（Compaction）：类别内合并](#9-记忆压缩compaction类别内合并)
- [10. 反思系统：L1/L2 多层反思](#10-反思系统l1l2-多层反思)
- [11. 用户画像（Profiler）](#11-用户画像profiler)
- [12. 隐私保护：PII 检测](#12-隐私保护pii-检测)
- [13. 辅助组件](#13-辅助组件)
- [14. Gateway 装配顺序](#14-gateway-装配顺序)
- [15. 数据库迁移](#15-数据库迁移)
- [16. 阅读建议：推荐阅读顺序](#16-阅读建议推荐阅读顺序)

---

## 1. 架构总览

```
┌────────────────────────────────────────────────────────────────┐
│                      Gateway (装配层)                          │
│  internal/gateway/gateway.go                                   │
└──────────┬──────────┬───────────┬──────────┬──────────────────┘
           │          │           │          │
     ┌─────▼────┐ ┌───▼───┐ ┌────▼────┐ ┌───▼────────┐
     │FactExtr. │ │Lifecy.│ │ Search  │ │ Background │
     │(提取)    │ │(生命周│ │ (混合   │ │  Tasks     │
     │          │ │期管理)│ │  检索)  │ │            │
     └────┬─────┘ └───┬───┘ └────┬────┘ └─┬──┬──┬───┘
          │           │          │         │  │  │
          │     ┌─────▼──────────▼─┐       │  │  │
          └────►│ FileMemoryStore  │◄──────┘  │  │
                │ (核心存储)        │          │  │
                └──┬────────────┬──┘          │  │
         ┌─────────▼──┐  ┌─────▼──────┐      │  │
         │ Markdown   │  │ SQLite     │      │  │
         │ Files      │  │ Index      │      │  │
         │ (~/.iron-  │  │ (FTS5+Vec) │      │  │
         │ claw/      │  │            │      │  │
         │ memory/)   │  │ memory_index│     │  │
         └────────────┘  │ memory_fts  │     │  │
                         │ memory_emb. │     │  │
                         └─────────────┘     │  │
                                             │  │
     ┌──────────────┐  ┌───────────────┐  ┌──▼──▼──────────┐
     │ Consolidator │  │ Compactor     │  │ForgettingCurve │
     │ (整合晋升)    │  │ (压缩合并)    │  │ (遗忘衰减)     │
     └──────────────┘  └───────────────┘  └────────────────┘
                                    │
     ┌──────────────┐  ┌───────────▼───┐
     │ Profiler     │  │ Reflector     │
     │ (用户画像)    │  │ (L1/L2反思)   │
     └──────────────┘  └───────────────┘
```

**设计理念**：File-first（文件优先）+ SQLite 辅助索引。Markdown 文件是真正的存储源（source of truth），SQLite 只是加速检索的索引层。

---

## 2. 核心数据模型

### 2.1 Entry — 内存中的记忆条目

📄 **文件**: `internal/memory/store.go:53-65`

```go
type Entry struct {
    ID        string
    SessionID string
    UserID    string
    Scope     MemoryScope  // session | user | global
    Content   string       // 蒸馏后的事实，而非原始消息
    Embedding []float32    // 向量嵌入
    Metadata  map[string]string
    Version   int
    ExpiresAt *time.Time
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

`Entry` 是在代码内部流转的记忆单元，在保存到文件时会被转换为 `MemoryFile`。

### 2.2 MemoryFile — 磁盘上的 Markdown 文件表示

📄 **文件**: `internal/memory/file_store.go:27-45`

```go
type MemoryFile struct {
    ID           string            `yaml:"id"`
    Scope        string            `yaml:"scope"`
    UserID       string            `yaml:"user_id,omitempty"`
    SessionID    string            `yaml:"session_id,omitempty"`
    CreatedAt    time.Time         `yaml:"created_at"`
    UpdatedAt    time.Time         `yaml:"updated_at"`
    LastAccessed *time.Time        `yaml:"last_accessed_at,omitempty"`
    Strength     float64           `yaml:"strength,omitempty"`
    Type         string            `yaml:"type,omitempty"`        // episodic, semantic, procedural, reflection, summary, profile
    Importance   int               `yaml:"importance,omitempty"`  // 1-10
    Emotion      string            `yaml:"emotion,omitempty"`     // positive, negative, neutral
    Sensitivity  string            `yaml:"sensitivity,omitempty"` // public, private, secret
    RelatedTo    string            `yaml:"related_to,omitempty"`
    PromotedFrom string            `yaml:"promoted_from,omitempty"`
    PromotedAt   *time.Time        `yaml:"promoted_at,omitempty"`
    Metadata     map[string]string `yaml:"metadata,omitempty"`
    Content      string            `yaml:"-"`  // 不序列化到 frontmatter，写到 --- 分隔符后
}
```

对应的磁盘文件格式如下：

```markdown
---
id: fact_1712345678
scope: user
user_id: alice
created_at: 2026-04-01T10:00:00Z
updated_at: 2026-04-01T10:00:00Z
type: semantic
importance: 7
emotion: positive
sensitivity: public
---

用户偏好使用 Go 语言编写后端服务，喜欢简洁的 API 设计。
```

### 2.3 记忆作用域（Scope）

📄 **文件**: `internal/memory/store.go:34-40`

| Scope | 含义 | 生命周期 |
|-------|------|---------|
| `session` | 会话级记忆 | 短期，可被晋升或自动归档 |
| `user` | 用户级记忆 | 长期，跨对话保留 |
| `global` | 全局记忆 | 系统级别，所有用户共享 |
| `feedback` | 反馈记忆 | 用户反馈相关 |

### 2.4 记忆类型（Type）

| Type | 含义 | 遗忘速度 |
|------|------|---------|
| `episodic` | 事件/经历型 | 快（stability × 12h） |
| `semantic` | 知识/偏好型 | 中（stability × 24h） |
| `procedural` | 行为/流程型 | 慢（stability × 48h） |
| `reflection` | 反思型（L1/L2） | 由反思系统生成 |
| `summary` | 压缩摘要型 | 由 Compactor 生成 |
| `profile` | 用户画像 | 由 Profiler 生成 |

### 2.5 Store 接口

📄 **文件**: `internal/memory/store.go:85-91`

```go
type Store interface {
    Save(ctx context.Context, entry Entry) error
    Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
    ListByScope(ctx context.Context, scope MemoryScope, userID string) ([]Entry, error)
    Update(ctx context.Context, id string, content string, version int) error
    Delete(ctx context.Context, id string) error
}
```

唯一实现：`FileMemoryStore`。所有上层组件（LifecycleManager, Consolidator, Compactor, Reflector, Profiler）都通过此接口操作记忆。

---

## 3. 存储层：文件 + SQLite 双轨架构

📄 **核心文件**: `internal/memory/file_store.go`

### 3.1 目录结构

```
~/.ironclaw/memory/
├── MEMORY.md              ← 索引文件（人类可读）
├── user/                  ← 用户级记忆
│   ├── memory_20260401_fact_xxx.md
│   └── profile_alice.md   ← 用户画像
├── session/               ← 会话级记忆
│   └── memory_20260401_fact_yyy.md
├── feedback/              ← 反馈记忆
├── global/                ← 全局记忆
└── archived/              ← 已归档（被删除/遗忘的记忆）
```

### 3.2 FileMemoryStore 初始化

📄 **文件**: `internal/memory/file_store.go:48-62`

```go
func NewFileMemoryStore(baseDir string, db *sql.DB, embedder EmbeddingProvider, cfg MemoryConfig) (*FileMemoryStore, error) {
    store := &FileMemoryStore{baseDir, db, embedder, cfg}
    store.initDirectories()       // 创建 user/, session/, feedback/, global/, archived/
    store.checkIndexStaleness()   // 如果索引过期（>24h），自动 RebuildIndex
    return store, nil
}
```

**关键点**：启动时会检测 SQLite 索引是否过期，如果文件修改时间领先索引 24h 以上，会触发完整的索引重建（`RebuildIndex`）。

### 3.3 Save 流程

📄 **文件**: `internal/memory/file_store.go:113-149`

```
Save(entry)
  │
  ├─ 1. Entry → MemoryFile 转换（提取 metadata 中的 type/importance/emotion/sensitivity）
  │
  ├─ 2. buildFilePath: {scope}/{category}_{YYYYMMDD}_{id}.md
  │
  ├─ 3. writeFileAtomic: 写临时文件 → fsync → rename（原子写入）
  │
  └─ 4. syncIndex: 同步到 SQLite 三张表
       ├─ MEMORY.md 追加条目
       ├─ memory_index: UPSERT 元数据
       ├─ memory_fts: DELETE + INSERT（FTS5 不支持 UPSERT）
       └─ memory_embeddings: UPSERT 向量
```

**原子写入**（`writeFileAtomic`，L479-512）：写 `.tmp` 文件 → `f.Sync()` → `os.Rename()`，确保写入要么完整成功要么不影响原文件。

### 3.4 Delete 流程

📄 **文件**: `internal/memory/file_store.go:439-470`

Delete **不是真正删除**，而是：
1. 将文件从 `{scope}/` 移动到 `archived/`
2. 从 `memory_index`、`memory_fts`、`memory_embeddings` 三张表中删除记录

### 3.5 MEMORY.md 索引文件

📄 **文件**: `internal/memory/memory_index.go`

`MEMORY.md` 是一个人类可读的 Markdown 索引文件，格式如下：

```markdown
# Memory Index
Last updated: 2026-04-01T10:00:00Z

## User Memories
- [memory_20260401_fact_xxx.md](user/memory_20260401_fact_xxx.md) — 用户偏好 Go 语言...

## Session Memories
- [memory_20260401_fact_yyy.md](session/memory_20260401_fact_yyy.md) — 今天讨论了...
```

它在搜索时被解析用于快速过滤（`Parse` 方法，L153-191），并在每次 Save 时追加（`AddEntry`，L56-69）。

### 3.6 RebuildIndex — 全量索引重建

📄 **文件**: `internal/memory/file_store.go:603-657`

扫描所有 scope 目录下的 `.md` 文件 → 解析 frontmatter → 生成 embedding → 写入 SQLite 三张表 → 重建 MEMORY.md。

---

## 4. 事实提取：从对话到记忆

📄 **核心文件**: `internal/memory/facts.go`

### 4.1 Completer 接口

```go
type Completer interface {
    Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}
```

这是记忆子系统的本地 LLM 接口，避免与 `agent` 包的循环依赖。Gateway 中通过 `completerAdapter` 桥接 `agent.Provider` 到此接口。

### 4.2 LLMFactExtractor

📄 **文件**: `internal/memory/facts.go:27-84`

**核心流程**：

```
用户消息 + AI 回复
       │
       ▼
LLMFactExtractor.Extract(goal, outcome)
       │
       ├─ 1. 构造 Prompt: "USER GOAL: {goal}\nOUTCOME/RESPONSE: {outcome}"
       │
       ├─ 2. 调用 LLM (system prompt = factExtractionSystemPrompt)
       │     提取最多 5 个 fact，JSON 数组格式
       │
       ├─ 3. parseFacts: 解析 JSON → []ExtractedFact
       │     每个 fact 包含: content, category, type, importance(1-10), emotion
       │
       └─ 4. PII 检测: 如果包含敏感信息，标记 sensitivity = "private"
```

**ExtractedFact 结构**：

```go
type ExtractedFact struct {
    Content     string `json:"content"`     // "用户偏好使用 Go 语言"
    Category    string `json:"category"`    // preference | fact | task | relationship | identity
    Type        string `json:"type"`        // episodic | semantic | procedural
    Importance  int    `json:"importance"`  // 1-10
    Emotion     string `json:"emotion"`     // positive | negative | neutral
    Sensitivity string `json:"-"`           // 由 PII 检测设置，不从 LLM 获取
}
```

**System Prompt**（L42-57）要求 LLM：
- 只输出 JSON 数组，不输出散文
- 每个 fact 必须自包含（无需上下文即可理解）
- 只记忆有长期价值的事实（偏好、身份、目标）
- 忽略临时性信息（当前时间、临时状态）
- 每次最多 5 条

---

## 5. 生命周期管理：ADD/UPDATE/DELETE/NOOP

📄 **核心文件**: `internal/memory/lifecycle.go`

这是 IronClaw 记忆系统的"大脑"，参考了 [mem0](https://github.com/mem0ai/mem0) 的设计。

### 5.1 决策流程

```
新 fact 进入
     │
     ▼
LifecycleManager.Process(fact, sessionID, userID, scope)
     │
     ├─ 1. 相似度搜索: store.Search(text=fact.Content, limit=5)
     │
     ├─ 2. 过滤: 只保留 score >= threshold(默认 0.85) 的结果
     │
     ├─ 3. 如果无相似结果 或 无 LLM → 直接 ADD
     │     如果有相似结果 → 调用 LLM 决策
     │
     │     LLM 输入: "NEW FACT: {content}\nEXISTING SIMILAR MEMORIES:\n- ID: x, Score: 0.92\n  Content: ..."
     │     LLM 输出: {"action": "UPDATE", "target_id": "...", "reason": "..."}
     │
     ├─ 4. 执行决策:
     │     ├─ ADD    → executeAdd: 创建新 Entry，保存到 Store
     │     ├─ UPDATE → executeUpdate: 归档旧条目 + 创建新条目（metadata 中记录 updated_from）
     │     ├─ DELETE → executeDelete: 归档目标条目
     │     └─ NOOP   → 什么都不做
     │
     └─ 5. 通知反思追踪器: reflector.Track(factID, content, userID)
```

### 5.2 LLM 决策 Prompt

📄 **文件**: `internal/memory/lifecycle.go:60-74`

LLM 需要判断：
- **冲突检测**：新 fact 是否与已有记忆矛盾？（返回 `conflicting_ids`）
- **时间替代**：新 fact 是否取代了旧信息？（返回 `target_id` + UPDATE）
- **互补关系**：新 fact 与已有记忆是否互补？（返回 `related_to`）
- **重复检测**：新 fact 是否已经被捕获？（NOOP）

### 5.3 与知识图谱的同步

📄 **文件**: `internal/memory/lifecycle.go:23-27` + `internal/memory/lifecycle.go:233-238`

通过可选的 `GraphSyncer` 接口，每次 ADD/UPDATE/DELETE 都会同步到知识图谱（如果已启用）：

```go
type GraphSyncer interface {
    SyncOnAdd(ctx context.Context, factID, content string) error
    SyncOnUpdate(ctx context.Context, oldFactID, newFactID, content string) error
    SyncOnDelete(ctx context.Context, factID string) error
}
```

在 Gateway 中通过 `lifecycleMgr.SetGraphSync(graphSync)` 注入。

---

## 6. 混合检索：BM25 + 向量 + RRF 融合

📄 **核心文件**: `internal/memory/file_store.go:158-410`

### 6.1 搜索完整流程

```
SearchQuery
     │
     ▼
Search(query)
     │
     ├─ Step 1: 解析 MEMORY.md → 按 scope 快速过滤候选 ID
     │
     ├─ Step 2: 查询 memory_index 表 → 元数据过滤
     │           ├─ user_id 过滤
     │           ├─ session_id 过滤
     │           ├─ scope 过滤（来自 Step 1）
     │           ├─ type 过滤
     │           ├─ sensitivity 过滤（排除 secret；无 user 时还排除 private）
     │           └─ 返回 (memory_id, file_path, strength)
     │
     ├─ Step 3: hybridSearch — 混合搜索
     │   │
     │   ├─ BM25 搜索: memory_fts MATCH query.Text → 按 rank 排序
     │   │
     │   ├─ 向量搜索: 遍历 memory_embeddings → cosineSimilarity(query.Embedding, stored)
     │   │
     │   └─ RRF 融合: rrfFusion(bm25Results, vectorResults, idMap)
     │
     ├─ Step 4: 取 top-k 结果 → 读取 Markdown 文件填充 Content
     │
     └─ Step 5: trackAccess → 更新 last_accessed_at（用于遗忘曲线）
```

### 6.2 RRF（Reciprocal Rank Fusion）算法

📄 **文件**: `internal/memory/file_store.go:370-410`

```
对于每个检索源（BM25, Vector）：
  RRF_score(id) += 1 / (k + rank)    // k = 60（常数）

最终分数：
  final_score = relevance_score × 0.7 + strength × 0.3
```

这意味着：
- **70%** 权重来自检索相关性（BM25 + 向量的 RRF 融合分数）
- **30%** 权重来自记忆强度（由遗忘曲线计算）

### 6.3 向量嵌入

📄 **文件**: `internal/memory/embedding.go` + `internal/memory/openai.go` + `internal/memory/cached_embedder.go`

```
EmbeddingProvider (接口)
    │
    ├─ NoopEmbedding      ← 无 API Key 时的占位实现，返回 nil
    │
    ├─ OpenAIEmbedding    ← 调用 OpenAI API（text-embedding-3-small, 1536 维）
    │
    └─ CachedEmbedder     ← 包装层，SHA256(text) 为 key 做内存缓存
```

Gateway 装配时：无 API Key → `NoopEmbedding`（纯 BM25 降级），有 API Key → `CachedEmbedder(OpenAIEmbedding)`。

---

## 7. 遗忘曲线：记忆强度衰减与自动归档

📄 **核心文件**: `internal/memory/forgetting_curve.go`

### 7.1 强度计算公式

📄 **文件**: `internal/memory/forgetting_curve.go:30-80`

```
stability = base_importance × type_multiplier

其中 type_multiplier：
  episodic   → 12  （衰减最快，12h 后降至 1/e）
  semantic   → 24  （中等速度）
  procedural → 48  （衰减最慢）

retention = e^(-elapsed_hours / stability)     ← 基础遗忘曲线

access_bonus = (1 + access_factor × access_count)
               × 1.5    (如果 24h 内有访问)

其中 access_factor：
  procedural → 0.12
  其他       → 0.10

final_strength = retention × access_bonus
```

**直觉理解**：
- importance=1 的 episodic 记忆，12h 后强度降至 ~37%，24h 后 ~14%
- importance=5 的 semantic 记忆，120h（5天）后强度降至 ~37%
- 每次被检索访问，强度会获得加成（"越用越记得"）

### 7.2 弱记忆归档

📄 **文件**: `internal/memory/forgetting_curve.go:120-160`

`FadeWeakMemories`：查询 `memory_index` 中 strength < 0.3 的 session/user 记忆 → 移入 `archived/`。

`FadeWeakMemoriesFromFiles`：直接扫描文件 → 计算 strength → 移入 `archived/`。

### 7.3 保留策略

📄 **文件**: `internal/memory/forgetting_curve.go:206-267`

`FadeByRetentionPolicy`：按类型的绝对保留期限归档。例如：
- episodic: 超过 `RetentionEpisodic` 时长后无论强度如何都归档
- procedural: 设为 0 表示永不自动删除

---

## 8. 记忆整合：Session → User 晋升

📄 **核心文件**: `internal/memory/consolidator.go`

### 8.1 工作原理

```
Consolidator.loop (后台协程，默认 24h 一次)
     │
     ▼
consolidateFiles()
     │
     ├─ 扫描 session/ 下所有 .md 文件
     │
     ├─ 筛选条件:
     │   ├─ 文件修改时间 > 24h（即足够"老"）
     │   ├─ 有 user_id（匿名用户的不晋升）
     │   └─ strength >= 0.5（只晋升有价值的记忆）
     │
     └─ 执行晋升:
         ├─ os.Rename(session/file.md → user/file.md)
         ├─ 更新 frontmatter: scope="user", promoted_from, promoted_at
         └─ 更新 memory_index: file_path, scope='user'
```

### 8.2 启动方式

```go
consolidator := NewConsolidator(store, db, baseDir, interval)
consolidator.Start(ctx)  // 启动后台协程
// 关闭时: consolidator.Stop()
```

---

## 9. 记忆压缩（Compaction）：类别内合并

📄 **核心文件**: `internal/memory/compactor.go`

### 9.1 工作原理

当同一 category 下的记忆数量超过阈值（默认 8 条），触发 LLM 驱动的合并：

```
Compactor.loop (后台协程，默认 6h 一次)
     │
     ▼
Compact()
     │
     ├─ findCompactionCandidates:
     │   ├─ 查询 memory_index: scope='user', type 非 summary/reflection/profile
     │   ├─ 按 category 分组
     │   └─ 返回 count >= threshold 的分组
     │
     └─ 对每个超阈值分组:
         ├─ 读取所有 fact 的 Content
         ├─ 调用 LLM 合并 → 生成 summary
         ├─ 保存新 summary 记忆 (type="summary", category=原分类)
         └─ 注意: 原始 fact 不会被删除，它们会通过遗忘曲线自然衰减
```

### 9.2 合并 Prompt

📄 **文件**: `internal/memory/compactor.go:12-19`

LLM 被要求：
- 保留所有关键事实，不丢失信息
- 逻辑组织，分组相关要点
- 简洁但完整
- 输出自包含的摘要

---

## 10. 反思系统：L1/L2 多层反思

📄 **核心文件**: `internal/memory/reflector.go`

### 10.1 架构设计

```
每个新 fact 被处理后
       │
       ▼
ReflectionTracker.Track(factID, content, userID)
       │
       ├─ unreflectedFactCount++
       ├─ 更新 topic embedding（指数移动平均，α=0.3）
       │
       └─ shouldTrigger()?
           ├─ 条件 1: unreflectedFactCount >= 10（默认）
           ├─ 条件 2: topic drift < 0.7（话题偏移检测）
           │
           ▼ 是
       triggerReflection (L1)
           │
           ├─ 收集所有 unreflected facts
           ├─ 调用 LLM: "识别模式、主题、综合洞察"
           ├─ 保存为 type="reflection", level="1" 的记忆
           ├─ 重置 unreflectedFactCount = 0
           │
           └─ l1CountSinceLastL2++
               │
               └─ >= 5（默认）?
                      │
                      ▼ 是
               triggerL2Reflection (L2 元反思)
                      │
                      ├─ 加载所有 L1 反思的 Content
                      ├─ 调用 LLM: "从模式级观察中综合战略洞察"
                      ├─ 保存为 type="reflection", level="2"
                      └─ 重置 l1CountSinceLastL2 = 0
```

### 10.2 话题偏移检测

📄 **文件**: `internal/memory/reflector.go:95-120`

使用指数移动平均（EMA）追踪话题向量：

```go
// α = 0.3
running[i] = 0.3 * new[i] + 0.7 * running[i]
```

当 `cosineSimilarity(running, lastReflectionTopic) < 0.7` 时，说明话题已经偏移得够远，即使 fact 数量没到阈值也触发反思。

### 10.3 状态持久化

📄 **文件**: `internal/memory/reflector.go:294-396`

`SaveState` / `LoadState` 将追踪器状态存入 `reflection_tracker_state` 表，确保跨重启不丢失未反思的 fact 计数和 topic embedding。

---

## 11. 用户画像（Profiler）

📄 **核心文件**: `internal/memory/profiler.go`

### 11.1 触发机制

```
每次 L1 反思创建后 → OnReflectionCreated(userID, level=1)
                         │
                         ├─ l1CountSinceProfile++
                         │
                         └─ >= 5（默认）?
                                │
                                ▼ 是
                         GenerateProfile(userID)
```

### 11.2 生成流程

📄 **文件**: `internal/memory/profiler.go:64-111`

```
GenerateProfile(userID)
     │
     ├─ 1. 加载已有 profile (user/profile_{userID}.md)
     ├─ 2. 收集 user/ 下所有 type="reflection" 的记忆
     ├─ 3. 构造 Prompt: 反思列表 + 现有画像
     ├─ 4. 调用 LLM → 生成结构化画像
     │     输出格式:
     │     ## Identity      ← 角色、专长、背景
     │     ## Preferences   ← 工作方式、沟通风格
     │     ## Current Focus ← 当前项目、目标、挑战
     │
     └─ 5. 保存到 user/profile_{userID}.md + memory_index
```

### 11.3 画像使用

📄 **文件**: `internal/memory/profiler.go:152-171`

`LoadUserProfile(baseDir, userID)` 是一个独立函数，供 `agent/runtime.go` 的 `buildSystemPrompt` 调用，将用户画像注入系统提示中。

---

## 12. 隐私保护：PII 检测

📄 **核心文件**: `internal/memory/privacy.go`

### 12.1 检测类型

| PII Type | 正则示例 |
|----------|---------|
| Email | `user@example.com` |
| Phone | `(123) 456-7890` |
| SSN | `123-45-6789` |
| Credit Card | `1234 5678 9012 3456` |

### 12.2 集成方式

在 `LLMFactExtractor.Extract` 中（`facts.go:77-80`）：

```go
for i := range facts {
    if e.piiDetector.HasPII(facts[i].Content) {
        facts[i].Sensitivity = "private"
    }
}
```

标记为 `private` 的记忆在搜索时受限：
- `secret` 记忆在任何自动搜索中都被排除
- `private` 记忆只在指定了 `user_id` 的搜索中才返回

📄 **搜索过滤代码**: `internal/memory/file_store.go:210-216`

---

## 13. 辅助组件

### 13.1 AccessLog — 访问日志

📄 **文件**: `internal/memory/access_log.go`

记录每次记忆被检索的访问日志，为遗忘曲线的 access_bonus 提供数据。

```
memory_access_log: (fact_id, session_id, accessed_at)
memory_access_stats: (fact_id, access_count, last_access, first_access)
```

### 13.2 AuditLogger — 审计日志

📄 **文件**: `internal/memory/audit.go`

记录所有记忆操作到 `memory_audit_log` 表，用于审计追踪。

### 13.3 IncrementalCompressor — 增量压缩器

📄 **文件**: `internal/memory/compressor.go`

实现 ReMe 风格的三层压缩：
- **工具输出压缩**：超长工具输出截断（>500 字符），完整输出保存到文件
- **每日摘要**：增量更新的对话摘要（存为 `sessions/YYYY-MM-DD.summary.md`）
- **上下文检测**：估算 token 数（4 字符 ≈ 1 token），超过阈值时触发压缩

### 13.4 MarkdownParser — Markdown 解析器

📄 **文件**: `internal/memory/markdown.go`

处理旧格式的 facts.md 文件（多条 fact 写在一个文件中），格式如下：

```markdown
---
scope: session
created_at: 2026-04-01T10:00:00Z
---
## fact_123
**Category**: preference
**Version**: 1
用户喜欢 Go 语言
---
## fact_456
...
```

---

## 14. Gateway 装配顺序

📄 **文件**: `internal/gateway/gateway.go:133-248`

```go
// 1. 创建 Embedding Provider
var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
if cfg.Memory.OpenAIAPIKey != "" {
    baseEmbedder := memory.NewOpenAIEmbedding(apiKey, model)
    embedder = memory.NewCachedEmbedder(baseEmbedder)
}

// 2. 创建 FileMemoryStore
fileStore := memory.NewFileMemoryStore(storageDir, db.DB, embedder, memCfg)

// 3. 注入到 Agent Runtime
runtime.SetMemoryStore(memStore)
runtime.SetMemoryBaseDir(storageDir)

// 4. 创建辅助组件
compressor := memory.NewIncrementalCompressor(storageDir, completer)
forgettingCurve := memory.NewForgettingCurveManager(db)

// 5. 如果启用了 FactExtraction：
factExtractor = memory.NewLLMFactExtractor(completer, memCfg)
reflector := memory.NewReflectionTracker(memStore, completer, embedder, memCfg, db.DB)
lifecycleMgr = memory.NewLifecycleManager(memStore, embedder, completer, memCfg, reflector)
compactor := memory.NewCompactor(memStore, completer, db.DB, storageDir, memCfg)
compactor.Start(ctx)    // 启动后台循环
profiler := memory.NewProfiler(memStore, completer, db.DB, storageDir, memCfg)

// 6. 如果启用了 Cognitive Agent：
cognitiveAgent.SetLifecycleManager(lifecycleMgr)

// 7. 如果启用了知识图谱：
lifecycleMgr.SetGraphSync(graphSync)  // 记忆→图谱同步
```

**关键适配器**: `completerAdapter` 将 `agent.Provider` 桥接为 `memory.Completer`：

📄 **文件**: `internal/gateway/gateway.go:625`

---

## 15. 数据库迁移

### Migration 006: 核心索引表

📄 **文件**: `internal/store/migrations/006_file_memory_index.sql`

```sql
-- 元数据索引（快速过滤）
CREATE TABLE memory_index (
    memory_id TEXT PRIMARY KEY,
    file_path TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL,
    user_id TEXT,
    session_id TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    strength REAL DEFAULT 1.0
);

-- FTS5 全文搜索（BM25）
CREATE VIRTUAL TABLE memory_fts USING fts5(
    memory_id UNINDEXED,
    content,
    tokenize = 'porter unicode61'   -- Porter 词干 + Unicode 支持
);

-- 向量嵌入（语义搜索）
CREATE TABLE memory_embeddings (
    memory_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    dimension INTEGER NOT NULL
);
```

### Migration 009: 类型字段

📄 **文件**: `internal/store/migrations/009_memory_type_fields.sql`

为 `memory_index` 添加 `memory_type`、`emotion`、`sensitivity` 字段。

### Migration 012: 审计日志

📄 **文件**: `internal/store/migrations/012_memory_audit_log.sql`

创建 `memory_audit_log` 表用于记录所有记忆操作。

---

## 16. 阅读建议：推荐阅读顺序

如果你想快速理解整个记忆系统的实现，建议按以下顺序阅读代码：

### 第一层：核心数据模型和存储（先理解数据怎么存）

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 1 | `internal/memory/store.go` | `Entry`, `MemoryScope`, `Store` 接口 — 所有组件的基础 |
| 2 | `internal/memory/file_store.go` | `FileMemoryStore`, `Save`, `Search`, `Delete` — 核心存储实现 |
| 3 | `internal/memory/memory_index.go` | `MEMORY.md` 索引文件的管理 |
| 4 | `internal/store/migrations/006_file_memory_index.sql` | 三张 SQLite 表的 DDL |

### 第二层：记忆的产生流程（理解数据从哪来）

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 5 | `internal/memory/facts.go` | `LLMFactExtractor` — LLM 如何从对话中提取事实 |
| 6 | `internal/memory/lifecycle.go` | `LifecycleManager.Process` — ADD/UPDATE/DELETE/NOOP 决策 |
| 7 | `internal/memory/privacy.go` | `PIIDetector` — PII 检测与隐私标记 |

### 第三层：记忆的管理机制（理解数据如何演化）

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 8 | `internal/memory/forgetting_curve.go` | 强度计算公式、弱记忆归档 |
| 9 | `internal/memory/consolidator.go` | Session → User 晋升逻辑 |
| 10 | `internal/memory/compactor.go` | 同类记忆的 LLM 合并 |

### 第四层：高级特性（理解记忆的智能层）

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 11 | `internal/memory/reflector.go` | L1/L2 反思、话题漂移检测 |
| 12 | `internal/memory/profiler.go` | 用户画像生成 |
| 13 | `internal/memory/embedding.go` + `openai.go` + `cached_embedder.go` | 向量嵌入链 |

### 第五层：装配全景（理解所有组件如何连接）

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 14 | `internal/gateway/gateway.go:133-248` | 所有记忆组件的初始化和连线 |

### 辅助文件

| 文件 | 说明 |
|------|------|
| `internal/memory/access_log.go` | 访问日志（支撑遗忘曲线） |
| `internal/memory/audit.go` | 审计日志 |
| `internal/memory/compressor.go` | 增量压缩（ReMe 风格） |
| `internal/memory/markdown.go` | 旧格式 Markdown 解析器 |
