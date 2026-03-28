# ReMe + 记忆系统设计 融合方案

## 核心洞察对比

### 你的探索 vs ReMe vs IronClaw 当前实现

| 特性 | 你的设计文档 | ReMe | IronClaw 当前 | 融合方案 |
|------|-------------|------|--------------|---------|
| **存储方式** | 四层架构(工作/情节/语义/程序) | 文件为主(MEMORY.md + 日志) | 双存储(File+SQLite) | **分层文件系统** |
| **压缩策略** | 滑动窗口+LLM摘要 | 三层压缩(工具/对话/上下文) | 无自动压缩 | **增量摘要+Token感知** |
| **遗忘曲线** | Ebbinghaus公式 | 无 | 时间阈值 | **访问频率+时间衰减** |
| **冲突检测** | LLM判断+版本管理 | 无 | 简单去重 | **LLM冲突解析+历史** |
| **检索策略** | RRF融合(语义+BM25+时序) | 70%向量+30%BM25 | RRF(40%BM25+60%向量) | **自适应权重** |
| **MemGPT思想** | 分页管理+心跳机制 | 无 | 无 | **轻量级自主管理** |

---

## 融合架构设计

### 1. 分层文件系统 (ReMe启发)

```
~/.IronClaw/memory/
├── CORE.md                    # 核心记忆(MemGPT core_memory)
│   ├── [USER_PROFILE]         # 用户画像
│   ├── [PREFERENCES]          # 偏好设置
│   └── [CONSTRAINTS]          # 约束条件
│
├── sessions/
│   ├── 2026-03-26.jsonl       # 原始对话(ReMe dialog)
│   └── 2026-03-26.summary.md  # 日摘要(ReMe memory)
│
├── facts/
│   ├── user/
│   │   ├── profile.md         # 用户级事实
│   │   └── preferences.md
│   └── global/
│       └── knowledge.md       # 全局知识
│
├── archive/
│   └── 2026-03/               # 月度归档(自动压缩)
│       └── summary.md
│
└── .index/                    # SQLite索引(可重建)
    ├── embeddings.db
    ├── fts.db
    └── access_log.db
```

**关键设计决策**:
- **CORE.md** = MemGPT的固定区域，始终加载到context
- **sessions/** = ReMe的dialog+memory，支持增量摘要
- **facts/** = 当前的memory_facts，但组织为markdown
- **.index/** = 性能层，可从文件重建

---

### 2. 增量摘要系统 (ReMe三层压缩)

```go
// internal/memory/compressor.go
package memory

type IncrementalCompressor struct {
    store      UnifiedStore
    completer  Completer
    tokenLimit int
}

// ReMe风格的三层压缩
func (c *IncrementalCompressor) Compress(ctx context.Context, sessionID string) error {
    // Layer 1: 工具结果压缩
    c.compressToolResults(ctx, sessionID)

    // Layer 2: 对话压缩(增量摘要)
    c.compressConversation(ctx, sessionID)

    // Layer 3: 上下文检查
    if c.exceedsTokenLimit(ctx, sessionID) {
        c.archiveOldSessions(ctx, sessionID)
    }

    return nil
}

// 增量摘要: 新对话 + 旧摘要 → 更新摘要
func (c *IncrementalCompressor) compressConversation(ctx context.Context, sessionID string) error {
    // 读取今日对话
    dialogPath := fmt.Sprintf("sessions/%s.jsonl", time.Now().Format("2006-01-02"))
    dialogs := c.readDialogs(dialogPath)

    // 读取现有摘要
    summaryPath := fmt.Sprintf("sessions/%s.summary.md", time.Now().Format("2006-01-02"))
    existingSummary := c.readSummary(summaryPath)

    // LLM增量更新
    prompt := fmt.Sprintf(`
现有摘要:
%s

新增对话:
%s

请更新摘要，保留关键信息:
- 用户目标
- 重要决策
- 待办事项
- 关键上下文

输出格式(Markdown):
## 目标
...
## 进展
...
## 待办
...
`, existingSummary, formatDialogs(dialogs))

    newSummary, err := c.completer.Complete(ctx, "你是记忆压缩助手", prompt)
    if err != nil {
        return err
    }

    // 写回文件
    return c.writeSummary(summaryPath, newSummary)
}

// 工具结果压缩: 长输出截断+文件引用
func (c *IncrementalCompressor) compressToolResults(ctx context.Context, sessionID string) error {
    const MAX_TOOL_OUTPUT = 500 // 字符

    dialogs := c.readDialogs(fmt.Sprintf("sessions/%s.jsonl", time.Now().Format("2006-01-02")))

    for i, dialog := range dialogs {
        if dialog.Role == "tool" && len(dialog.Content) > MAX_TOOL_OUTPUT {
            // 保存完整输出到文件
            resultID := uuid.New().String()
            resultPath := fmt.Sprintf("tool_results/%s.txt", resultID)
            c.writeFile(resultPath, dialog.Content)

            // 替换为引用
            dialogs[i].Content = fmt.Sprintf(
                "[工具输出过长，已截断]\n前500字符: %s...\n完整输出: %s",
                dialog.Content[:MAX_TOOL_OUTPUT],
                resultPath,
            )
        }
    }

    return c.writeDialogs(fmt.Sprintf("sessions/%s.jsonl", time.Now().Format("2006-01-02")), dialogs)
}
```

---

### 3. 遗忘曲线记忆管理 (你的设计)

```go
// internal/memory/forgetting_curve.go
package memory

import "math"

type ForgettingCurveManager struct {
    store     UnifiedStore
    accessLog *AccessLog
}

type MemoryStrength struct {
    FactID          string
    BaseImportance  float64  // 1.0=普通, 2.0=重要, 5.0=关键
    AccessCount     int
    LastAccessTime  time.Time
    CreatedAt       time.Time
    CurrentStrength float64  // 0-1
}

// Ebbinghaus遗忘曲线: R(t) = e^(-t/S)
func (m *ForgettingCurveManager) ComputeStrength(fact Entry) float64 {
    now := time.Now()

    // 时间衰减
    elapsedHours := now.Sub(fact.CreatedAt).Hours()
    stability := fact.Metadata["base_importance"] * 24  // 重要信息稳定性更高
    if stability == 0 {
        stability = 24  // 默认1天
    }
    retention := math.Exp(-elapsedHours / stability)

    // 访问加成(间隔重复效应)
    accessCount := m.accessLog.GetCount(fact.ID)
    accessBonus := 1 + 0.1*float64(accessCount)

    // 最近访问加成
    if lastAccess := m.accessLog.GetLastAccess(fact.ID); !lastAccess.IsZero() {
        recentHours := now.Sub(lastAccess).Hours()
        if recentHours < 24 {
            accessBonus *= 1.5  // 24小时内访问过，强化记忆
        }
    }

    return retention * accessBonus
}

// 获取活跃记忆(强度>阈值)
func (m *ForgettingCurveManager) GetActiveMemories(ctx context.Context, threshold float64) ([]Entry, error) {
    allFacts, err := m.store.ListByScope(ctx, ScopeUser, "")
    if err != nil {
        return nil, err
    }

    var active []Entry
    for _, fact := range allFacts {
        strength := m.ComputeStrength(fact)
        if strength >= threshold {
            active = append(active, fact)
        }
    }

    // 按强度排序
    sort.Slice(active, func(i, j int) bool {
        return m.ComputeStrength(active[i]) > m.ComputeStrength(active[j])
    })

    return active, nil
}

// 标记访问(间隔重复)
func (m *ForgettingCurveManager) MarkAccessed(ctx context.Context, factID string) error {
    return m.accessLog.Increment(ctx, factID)
}
```

---

### 4. 冲突检测与版本管理 (你的设计)

```go
// internal/memory/conflict_resolver.go
package memory

type ConflictResolver struct {
    store     UnifiedStore
    completer Completer
    history   *HistoryManager
}

type ConflictResolution struct {
    HasConflict      bool
    Action           string  // "update" | "keep_both" | "flag_review"
    NewIsMoreAccurate bool
    Reason           string
    ConflictingIDs   []string
}

func (r *ConflictResolver) ResolveConflict(ctx context.Context, newFact Entry) (*ConflictResolution, error) {
    // 1. 查找相似事实
    similar, err := r.store.Search(ctx, SearchQuery{
        Text:   newFact.Content,
        Limit:  5,
        UserID: newFact.UserID,
        Scopes: []MemoryScope{newFact.Scope},
    })
    if err != nil || len(similar) == 0 {
        return &ConflictResolution{HasConflict: false}, nil
    }

    // 2. LLM判断冲突
    prompt := fmt.Sprintf(`
新信息: %s

现有相关记忆:
%s

判断:
1. 新信息与现有记忆是否矛盾?
2. 如果矛盾，新信息是否更准确(更新的时间/用户明确更正)?

JSON格式:
{
  "has_conflict": bool,
  "new_is_more_accurate": bool,
  "action": "update|keep_both|flag_review",
  "reason": "..."
}
`, newFact.Content, formatSimilar(similar))

    resp, err := r.completer.Complete(ctx, "你是记忆冲突检测器", prompt)
    if err != nil {
        return nil, err
    }

    var resolution ConflictResolution
    if err := json.Unmarshal([]byte(extractJSON(resp)), &resolution); err != nil {
        return nil, err
    }

    // 3. 执行决策
    switch resolution.Action {
    case "update":
        // 更新旧事实，保留历史
        for _, s := range similar {
            r.history.Record(ctx, s.Entry, "SUPERSEDED")
            r.store.UpdateFact(ctx, s.Entry.ID, newFact.Content, s.Entry.Version+1)
        }

    case "keep_both":
        // 两者都保留，标记关系
        newFact.Metadata["related_to"] = similar[0].Entry.ID
        r.store.SaveFact(ctx, newFact)

    case "flag_review":
        // 标记为需要人工审核
        newFact.Metadata["needs_review"] = "true"
        newFact.Metadata["conflicts_with"] = similar[0].Entry.ID
        r.store.SaveFact(ctx, newFact)
    }

    return &resolution, nil
}
```

---

### 5. 自适应检索权重 (融合RRF)

```go
// internal/memory/adaptive_retriever.go
package memory

type AdaptiveRetriever struct {
    store   UnifiedStore
    weights *DynamicWeights
}

type DynamicWeights struct {
    BM25Weight       float64
    VectorWeight     float64
    RecencyWeight    float64
    ImportanceWeight float64

    // 自适应调整
    successRate map[string]float64  // 每种权重组合的成功率
}

func (r *AdaptiveRetriever) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
    // 1. 多路检索
    bm25Results := r.store.BM25Search(ctx, query)
    vectorResults := r.store.VectorSearch(ctx, query)
    recentResults := r.store.RecentSearch(ctx, query)

    // 2. 自适应权重(基于历史成功率)
    weights := r.weights.GetOptimalWeights(query.Context)

    // 3. RRF融合
    merged := r.fuseWithWeights(bm25Results, vectorResults, recentResults, weights)

    // 4. 重排序(重要性+遗忘曲线)
    reranked := r.rerank(merged, query)

    return reranked, nil
}

func (r *AdaptiveRetriever) fuseWithWeights(
    bm25, vector, recent []SearchResult,
    weights DynamicWeights,
) []SearchResult {
    scores := make(map[string]float64)

    // RRF with weighted contributions
    for rank, result := range bm25 {
        rrf := 1.0 / (60 + float64(rank))
        scores[result.Entry.ID] += weights.BM25Weight * rrf
    }

    for rank, result := range vector {
        rrf := 1.0 / (60 + float64(rank))
        scores[result.Entry.ID] += weights.VectorWeight * rrf
    }

    for rank, result := range recent {
        rrf := 1.0 / (60 + float64(rank))
        scores[result.Entry.ID] += weights.RecencyWeight * rrf
    }

    // 转换为结果列表
    var merged []SearchResult
    for id, score := range scores {
        entry := r.getEntryByID(id)
        merged = append(merged, SearchResult{Entry: entry, Score: score})
    }

    sort.Slice(merged, func(i, j int) bool {
        return merged[i].Score > merged[j].Score
    })

    return merged
}

// 学习最优权重(基于用户反馈)
func (w *DynamicWeights) UpdateFromFeedback(queryType string, success bool) {
    if success {
        // 当前权重有效，增强
        w.successRate[queryType] += 0.1
    } else {
        // 调整权重
        w.successRate[queryType] -= 0.1
        w.adjustWeights(queryType)
    }
}
```

---

### 6. 轻量级MemGPT (自主记忆管理)

```go
// internal/memory/autonomous_manager.go
package memory

type AutonomousMemoryManager struct {
    store       UnifiedStore
    compressor  *IncrementalCompressor
    completer   Completer
    tokenLimit  int
    coreMemory  *CoreMemory  // CORE.md
}

type CoreMemory struct {
    UserProfile  string
    Preferences  []string
    Constraints  []string
}

// 心跳机制: 定期整理记忆
func (m *AutonomousMemoryManager) Heartbeat(ctx context.Context) error {
    slog.Info("memory: heartbeat triggered")

    // 1. 检查context压力
    pressure := m.computeContextPressure(ctx)
    if pressure > 0.8 {
        slog.Warn("memory: context pressure high", "pressure", pressure)
        m.compressor.Compress(ctx, "current")
    }

    // 2. 归档旧会话
    if m.shouldArchive(ctx) {
        m.archiveOldSessions(ctx)
    }

    // 3. 清理过期事实
    m.cleanupExpiredFacts(ctx)

    // 4. 更新CORE.md(如果有重要变化)
    m.updateCoreMemory(ctx)

    return nil
}

// LLM自主决定是否归档
func (m *AutonomousMemoryManager) shouldArchive(ctx context.Context) bool {
    sessions, _ := m.store.ListByScope(ctx, ScopeSession, "")
    if len(sessions) < 100 {
        return false  // 规则层: 少于100条不归档
    }

    // LLM判断
    prompt := fmt.Sprintf(`
当前有 %d 条会话记忆。

判断是否应该归档旧会话:
- 如果大部分是临时信息，应该归档
- 如果包含重要上下文，暂缓归档

回复 JSON: {"should_archive": bool, "reason": "..."}
`, len(sessions))

    resp, err := m.completer.Complete(ctx, "你是记忆管理助手", prompt)
    if err != nil {
        return true  // 默认归档
    }

    var decision struct {
        ShouldArchive bool   `json:"should_archive"`
        Reason        string `json:"reason"`
    }
    json.Unmarshal([]byte(extractJSON(resp)), &decision)

    slog.Info("memory: archive decision", "should", decision.ShouldArchive, "reason", decision.Reason)
    return decision.ShouldArchive
}

// 更新核心记忆(CORE.md)
func (m *AutonomousMemoryManager) updateCoreMemory(ctx context.Context) error {
    // 从最近事实中提取核心信息
    recentFacts, _ := m.store.Search(ctx, SearchQuery{
        Limit:  50,
        Scopes: []MemoryScope{ScopeUser},
    })

    prompt := fmt.Sprintf(`
当前核心记忆:
%s

最近事实:
%s

判断是否需要更新核心记忆(用户画像/偏好/约束)。
如果需要，输出新的CORE.md内容(Markdown格式)。
如果不需要，输出 "NO_UPDATE"。
`, m.coreMemory.String(), formatFacts(recentFacts))

    resp, err := m.completer.Complete(ctx, "你是核心记忆管理器", prompt)
    if err != nil {
        return err
    }

    if strings.TrimSpace(resp) == "NO_UPDATE" {
        return nil
    }

    // 写入CORE.md
    return m.writeCoreMemory(resp)
}
```

---

## 实施路线图

### Phase 1: 文件系统重构 (3天)

1. 实现分层文件结构
2. 迁移现有facts到markdown
3. 实现CORE.md加载

### Phase 2: 增量压缩 (2天)

1. 实现三层压缩器
2. 工具结果截断
3. 日摘要生成

### Phase 3: 遗忘曲线 (2天)

1. 实现强度计算
2. 访问日志追踪
3. 自动淡化低强度记忆

### Phase 4: 冲突检测 (2天)

1. LLM冲突判断
2. 版本历史保留
3. 人工审核标记

### Phase 5: 自适应检索 (2天)

1. 动态权重调整
2. 反馈学习
3. 性能基准测试

### Phase 6: 自主管理 (2天)

1. 心跳机制
2. 自动归档
3. CORE.md更新

**总计: 13天**

---

## 配置示例

```yaml
memory:
  storage_mode: layered_file  # 新模式

  # 文件结构
  file_structure:
    core_memory: CORE.md
    sessions_dir: sessions/
    facts_dir: facts/
    archive_dir: archive/
    index_dir: .index/

  # 压缩策略(ReMe)
  compression:
    enabled: true
    tool_result_max_chars: 500
    daily_summary: true
    token_limit: 100000
    archive_after_days: 30

  # 遗忘曲线
  forgetting_curve:
    enabled: true
    base_stability_hours: 24
    access_bonus: 0.1
    strength_threshold: 0.3  # 低于此值淡化

  # 冲突检测
  conflict_resolution:
    enabled: true
    auto_resolve: true  # false=人工审核

  # 自适应检索
  retrieval:
    adaptive_weights: true
    initial_bm25: 0.4
    initial_vector: 0.6
    initial_recency: 0.2
    learning_rate: 0.1

  # 自主管理(MemGPT)
  autonomous:
    enabled: true
    heartbeat_interval: 1h
    context_pressure_threshold: 0.8
    auto_archive: true
```

---

## 关键创新点

1. **ReMe的文件透明性** + **你的四层架构** = 分层文件系统
2. **ReMe的增量摘要** + **Token感知** = 智能压缩
3. **你的遗忘曲线** + **访问追踪** = 动态记忆强度
4. **你的冲突检测** + **版本管理** = 一致性保证
5. **RRF融合** + **自适应权重** = 最优检索
6. **MemGPT思想** + **轻量级实现** = 自主管理

这个方案将IronClaw的记忆系统提升到业界领先水平！
