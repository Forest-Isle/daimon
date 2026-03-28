# 记忆系统优化 - 实施优先级与ROI分析

## 快速决策矩阵

```
影响力 vs 实施难度

高影响 │ ③ 遗忘曲线      │ ① 增量压缩 ★
      │ ⑤ 自适应检索    │ ② 分层文件 ★
      │                │
─────┼────────────────┼──────────────
      │ ⑥ 自主管理      │ ④ 冲突检测
低影响 │                │
      │   高难度         │   低难度

★ = 推荐优先实施
```

---

## 方案对比

### 方案A: 渐进式优化 (推荐)

**Week 1-2: 增量压缩 (ReMe核心)**
- 实施难度: ⭐⭐
- 影响力: ⭐⭐⭐⭐⭐
- ROI: **极高**

**为什么优先?**
- 立即解决context溢出问题
- 降低LLM成本(压缩后token减少50%+)
- 不破坏现有架构
- 用户无感知

**实施内容:**
```go
// 1. 工具结果压缩 (1天)
type ToolResultCompressor struct {
    maxChars int  // 500
}

// 2. 日摘要生成 (2天)
type DailySummarizer struct {
    completer Completer
}

// 3. Token感知触发 (1天)
func (c *Compressor) ShouldCompress(messages []Message) bool {
    return countTokens(messages) > threshold
}
```

**预期收益:**
- Context使用率: 100% → 60%
- LLM成本: -40%
- 响应速度: +20% (更少token处理)

---

**Week 3-4: 遗忘曲线**
- 实施难度: ⭐⭐⭐
- 影响力: ⭐⭐⭐⭐
- ROI: **高**

**为什么第二?**
- 显著提升记忆质量
- 自动淡化无用信息
- 与现有系统兼容

**实施内容:**
```go
// 1. 强度计算 (1天)
func ComputeStrength(fact Entry) float64 {
    retention := math.Exp(-elapsed / stability)
    return retention * accessBonus
}

// 2. 访问追踪 (2天)
type AccessLog struct {
    db *store.DB
}

// 3. 自动淡化 (1天)
func FadeWeakMemories(threshold float64) {
    // 强度<0.3的事实降级或删除
}
```

**预期收益:**
- 检索精度: +30%
- 存储效率: +25% (淡化无用记忆)
- 用户满意度: +40% (更相关的记忆)

---

**Week 5-6: 冲突检测**
- 实施难度: ⭐⭐⭐
- 影响力: ⭐⭐⭐
- ROI: **中**

**为什么第三?**
- 保证记忆一致性
- 避免矛盾信息
- 提升可信度

**实施内容:**
```go
// 1. LLM冲突判断 (2天)
func DetectConflict(newFact, existing []Entry) ConflictResolution

// 2. 版本历史 (1天)
// 已有fact_history表，扩展即可

// 3. 人工审核标记 (1天)
fact.Metadata["needs_review"] = "true"
```

**预期收益:**
- 记忆准确率: +20%
- 用户信任度: +35%

---

### 方案B: 激进重构 (高风险)

**Week 1-3: 分层文件系统**
- 实施难度: ⭐⭐⭐⭐
- 影响力: ⭐⭐⭐⭐
- ROI: **中** (长期高)

**风险:**
- 需要数据迁移
- 可能破坏现有功能
- 测试工作量大

**建议:** 延后到Phase 2，先验证其他优化效果

---

### 方案C: 最小可行方案 (MVP)

**仅实施: 增量压缩 (2周)**

如果资源有限，只做这一项也能获得巨大收益:
- 成本降低40%
- Context管理自动化
- 为后续优化打基础

---

## 详细实施计划

### Priority 1: 增量压缩 (2周)

#### Day 1-2: 工具结果压缩

```go
// internal/memory/tool_compressor.go
package memory

const MAX_TOOL_OUTPUT = 500

type ToolCompressor struct {
    storageDir string
}

func (tc *ToolCompressor) Compress(toolResult string) string {
    if len(toolResult) <= MAX_TOOL_OUTPUT {
        return toolResult
    }

    // 保存完整输出
    id := uuid.New().String()
    path := filepath.Join(tc.storageDir, "tool_results", id+".txt")
    os.WriteFile(path, []byte(toolResult), 0644)

    // 返回截断+引用
    return fmt.Sprintf(
        "[输出过长，已截断]\n前%d字符:\n%s\n...\n完整输出: %s",
        MAX_TOOL_OUTPUT,
        toolResult[:MAX_TOOL_OUTPUT],
        path,
    )
}
```

**集成点:** `internal/agent/runtime.go` 的工具调用后

```go
// runtime.go
result := tool.Execute(ctx, input)

// 新增: 压缩长输出
if len(result) > 500 {
    result = toolCompressor.Compress(result)
}
```

---

#### Day 3-5: 日摘要生成

```go
// internal/memory/daily_summarizer.go
package memory

type DailySummarizer struct {
    store     Store
    completer Completer
}

func (ds *DailySummarizer) GenerateDailySummary(ctx context.Context, date time.Time) error {
    // 1. 读取当日对话
    sessions := ds.store.GetSessionsByDate(ctx, date)

    // 2. 读取现有摘要(如果有)
    existingSummary := ds.loadSummary(date)

    // 3. LLM增量更新
    prompt := fmt.Sprintf(`
现有摘要:
%s

今日新增对话:
%s

请更新摘要，保留:
- 用户目标
- 重要决策
- 待办事项
- 关键上下文

Markdown格式输出。
`, existingSummary, formatSessions(sessions))

    summary, err := ds.completer.Complete(ctx, "你是记忆摘要助手", prompt)
    if err != nil {
        return err
    }

    // 4. 保存摘要
    summaryPath := fmt.Sprintf("sessions/%s.summary.md", date.Format("2006-01-02"))
    return ds.writeSummary(summaryPath, summary)
}
```

**触发时机:**
- 每日定时任务 (凌晨2点)
- 或会话结束时

---

#### Day 6-7: Token感知压缩

```go
// internal/memory/context_manager.go
package memory

type ContextManager struct {
    tokenLimit    int  // 100000
    reserveTokens int  // 20000 (预留给响应)
    compressor    *IncrementalCompressor
}

func (cm *ContextManager) PrepareContext(ctx context.Context, messages []Message) ([]Message, error) {
    currentTokens := cm.countTokens(messages)

    if currentTokens > cm.tokenLimit-cm.reserveTokens {
        slog.Warn("context: approaching limit, compressing",
            "current", currentTokens,
            "limit", cm.tokenLimit)

        // 触发压缩
        compressed := cm.compressor.Compress(ctx, messages)
        return compressed, nil
    }

    return messages, nil
}

func (cm *ContextManager) countTokens(messages []Message) int {
    // 简化估算: 1 token ≈ 4 chars
    total := 0
    for _, msg := range messages {
        total += len(msg.Content) / 4
    }
    return total
}
```

**集成点:** `internal/agent/runtime.go` 的LLM调用前

```go
// runtime.go
func (r *Runtime) Run(ctx context.Context, input string) {
    // 新增: 准备context
    messages := r.buildMessages(input)
    messages, _ = r.contextManager.PrepareContext(ctx, messages)

    // 调用LLM
    response := r.provider.Complete(ctx, messages)
    ...
}
```

---

### Priority 2: 遗忘曲线 (2周)

#### Day 8-9: 访问日志

```sql
-- internal/store/migrations/009_access_log.sql
CREATE TABLE IF NOT EXISTS fact_access_log (
    fact_id TEXT NOT NULL,
    accessed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id TEXT,
    PRIMARY KEY (fact_id, accessed_at)
);

CREATE INDEX idx_access_log_fact ON fact_access_log(fact_id);
CREATE INDEX idx_access_log_time ON fact_access_log(accessed_at);

-- 聚合视图(每小时更新)
CREATE TABLE IF NOT EXISTS fact_access_stats (
    fact_id TEXT PRIMARY KEY,
    access_count INTEGER DEFAULT 0,
    last_access DATETIME,
    first_access DATETIME
);
```

```go
// internal/memory/access_log.go
package memory

type AccessLog struct {
    db *store.DB
}

func (al *AccessLog) RecordAccess(ctx context.Context, factID, sessionID string) error {
    _, err := al.db.ExecContext(ctx, `
        INSERT INTO fact_access_log (fact_id, session_id)
        VALUES (?, ?)
    `, factID, sessionID)

    // 异步更新统计
    go al.updateStats(factID)

    return err
}

func (al *AccessLog) GetStats(ctx context.Context, factID string) (count int, lastAccess time.Time, err error) {
    err = al.db.QueryRowContext(ctx, `
        SELECT access_count, last_access
        FROM fact_access_stats
        WHERE fact_id = ?
    `, factID).Scan(&count, &lastAccess)
    return
}
```

---

#### Day 10-12: 强度计算与淡化

```go
// internal/memory/forgetting_curve.go
package memory

func (fc *ForgettingCurveManager) ComputeStrength(ctx context.Context, fact Entry) float64 {
    // 基础重要性
    baseImportance := parseFloat(fact.Metadata["importance"], 1.0)

    // 时间衰减
    elapsedHours := time.Since(fact.CreatedAt).Hours()
    stability := baseImportance * 24  // 重要信息衰减慢
    retention := math.Exp(-elapsedHours / stability)

    // 访问加成
    accessCount, lastAccess, _ := fc.accessLog.GetStats(ctx, fact.ID)
    accessBonus := 1.0 + 0.1*float64(accessCount)

    // 最近访问加成
    if time.Since(lastAccess).Hours() < 24 {
        accessBonus *= 1.5
    }

    return retention * accessBonus
}

// 定期淡化弱记忆
func (fc *ForgettingCurveManager) FadeWeakMemories(ctx context.Context) error {
    facts, _ := fc.store.ListByScope(ctx, ScopeUser, "")

    faded := 0
    for _, fact := range facts {
        strength := fc.ComputeStrength(ctx, fact)

        if strength < 0.3 {
            // 降级到archive
            fact.Scope = "archive"
            fc.store.UpdateFact(ctx, fact.ID, fact.Content, fact.Version+1)
            faded++
        }
    }

    slog.Info("forgetting_curve: faded weak memories", "count", faded)
    return nil
}
```

**触发:** 每日定时任务

---

#### Day 13-14: 集成到检索

```go
// internal/memory/unified_store.go

func (s *UnifiedStore) Search(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
    // 原有检索
    results, err := s.index.Search(ctx, query)
    if err != nil {
        return nil, err
    }

    // 新增: 应用遗忘曲线权重
    for i := range results {
        strength := s.forgettingCurve.ComputeStrength(ctx, results[i].Entry)
        results[i].Score *= strength  // 强度低的记忆降权

        // 记录访问
        go s.accessLog.RecordAccess(ctx, results[i].Entry.ID, query.SessionID)
    }

    // 重新排序
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })

    return results, nil
}
```

---

### Priority 3: 冲突检测 (1周)

#### Day 15-17: LLM冲突判断

```go
// internal/memory/conflict_resolver.go
package memory

func (cr *ConflictResolver) CheckConflict(ctx context.Context, newFact Entry) (*ConflictResolution, error) {
    // 1. 查找相似事实
    similar, _ := cr.store.Search(ctx, SearchQuery{
        Text:   newFact.Content,
        Limit:  3,
        UserID: newFact.UserID,
    })

    if len(similar) == 0 {
        return &ConflictResolution{HasConflict: false}, nil
    }

    // 2. LLM判断
    prompt := fmt.Sprintf(`
新信息: %s (时间: %s)

现有记忆:
%s

判断是否冲突，以及如何处理。

JSON格式:
{
  "has_conflict": bool,
  "action": "update|keep_both|flag_review",
  "reason": "...",
  "conflicting_ids": ["id1", "id2"]
}
`, newFact.Content, newFact.CreatedAt.Format("2006-01-02"), formatSimilar(similar))

    resp, _ := cr.completer.Complete(ctx, "你是记忆冲突检测器", prompt)

    var resolution ConflictResolution
    json.Unmarshal([]byte(extractJSON(resp)), &resolution)

    return &resolution, nil
}
```

---

#### Day 18-19: 自动解决

```go
func (cr *ConflictResolver) Resolve(ctx context.Context, newFact Entry, resolution ConflictResolution) error {
    switch resolution.Action {
    case "update":
        // 更新旧事实
        for _, id := range resolution.ConflictingIDs {
            cr.history.Record(ctx, id, "SUPERSEDED", newFact.ID)
            cr.store.DeleteFact(ctx, id)
        }
        return cr.store.SaveFact(ctx, newFact)

    case "keep_both":
        // 标记关系
        newFact.Metadata["related_to"] = strings.Join(resolution.ConflictingIDs, ",")
        return cr.store.SaveFact(ctx, newFact)

    case "flag_review":
        // 人工审核
        newFact.Metadata["needs_review"] = "true"
        newFact.Metadata["conflicts_with"] = resolution.ConflictingIDs[0]
        return cr.store.SaveFact(ctx, newFact)
    }

    return nil
}
```

---

## 成本收益分析

### 增量压缩

**投入:**
- 开发: 2周 × 1人 = 2人周
- 测试: 3天
- 总成本: ~$5,000

**收益 (年化):**
- LLM成本节省: $50,000/年 × 40% = $20,000/年
- 响应速度提升: 用户满意度 +15%
- **ROI: 400%**

---

### 遗忘曲线

**投入:**
- 开发: 2周 × 1人 = 2人周
- 总成本: ~$5,000

**收益:**
- 检索精度提升: 减少无效召回 30%
- 存储成本节省: $2,000/年
- 用户满意度: +40%
- **ROI: 200%**

---

### 冲突检测

**投入:**
- 开发: 1周 × 1人 = 1人周
- 总成本: ~$2,500

**收益:**
- 记忆准确率: +20%
- 减少用户纠错: 节省支持成本 $5,000/年
- **ROI: 200%**

---

## 推荐方案

### 🎯 最佳路径: 方案A (渐进式)

**Week 1-2:** 增量压缩 ⭐⭐⭐⭐⭐
**Week 3-4:** 遗忘曲线 ⭐⭐⭐⭐
**Week 5:** 冲突检测 ⭐⭐⭐

**总投入:** 5周
**总收益:** ROI 300%+

**优势:**
- 风险低，每步都可独立验证
- 快速见效(Week 2就能看到成本下降)
- 不破坏现有架构
- 为后续优化打基础

---

## 验证指标

### 增量压缩
- [ ] Context token使用率 < 70%
- [ ] LLM成本下降 > 30%
- [ ] 压缩后信息保留率 > 95%

### 遗忘曲线
- [ ] 检索精度(P@5) > 0.85
- [ ] 低强度记忆占比 < 20%
- [ ] 用户反馈相关性评分 > 4.0/5.0

### 冲突检测
- [ ] 冲突检测准确率 > 90%
- [ ] 自动解决成功率 > 80%
- [ ] 人工审核率 < 10%

---

## 下一步行动

1. **立即开始:** 增量压缩 (Day 1-7)
2. **并行准备:** 设计遗忘曲线数据库schema
3. **技术预研:** 评估LLM冲突判断的准确率

需要我开始实现第一个模块(增量压缩)吗？
