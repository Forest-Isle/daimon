# L3 — Collective Intelligence (群体智能市场)

> 优先级: P4 | 工作量: 2-3 周 | 依赖: L1 (子代理需要 WASM 工具扩展能力)  
> 从父代理 spawn 子代理 → 代理市场竞价协作。涌现角色分工，群体智慧超过单个代理。

---

## 一、核心概念

```
当前模式 (v3):                    目标模式 (v4):
                              
  Parent Agent                    ┌──────────────────────┐
      │                           │    Agent Market      │
      ├─ spawn("coder")           │                      │
      ├─ spawn("analyst")         │  💼 Task Board       │
      └─ spawn("reviewer")        │  📊 Reputation Chain │
                                   │  🎯 Emergent Roles   │
  父控制一切，子被动执行            │  💰 Bid/Award System  │
                                   └──────────────────────┘
                                         ↑    ↑    ↑
                                    Coder  Analyst  Reviewer
                                    (自主竞标，不是被 spawn)
```

---

## 二、代理市场

```go
// internal/collective/market.go

// AgentMarket 管理代理间的任务竞标
type AgentMarket struct {
    board        *TaskBoard
    registry     *AgentRegistry      // 所有可用代理
    reputation   *ReputationSystem
    settlement   *SettlementEngine   // 任务完成后的奖惩
}

// TaskBoard 任务公告板
type TaskBoard struct {
    openTasks    []*MarketTask
    mu           sync.RWMutex
    subscribers  []chan *MarketTask   // 代理订阅新任务通知
}

// MarketTask 市场上的任务
type MarketTask struct {
    ID            string
    Description   string
    RequiredSkills []string           // 需要的技能标签
    EstimatedComplexity string         // simple/moderate/complex
    Budget        float64             // 出价上限（如果代理有费用概念）
    Deadline      time.Time
    Bids          []*Bid
    AwardedTo     string              // 中标代理 ID
    Status        TaskStatus
}

// Bid 代理对任务的竞标
type Bid struct {
    AgentID       string
    AgentName     string
    Confidence    float64             // 代理自己估计的成功率
    EstimatedDuration time.Duration
    Price         float64             // 代理要求的"费用"
    Rationale     string              // 为什么我能做好这个任务
    Reputation    float64             // 代理当前声誉分
}

// PostTask 将任务发布到市场
func (m *AgentMarket) PostTask(ctx context.Context, task *MarketTask) {
    m.board.mu.Lock()
    m.board.openTasks = append(m.board.openTasks, task)
    m.board.mu.Unlock()

    // 通知所有订阅的代理
    for _, sub := range m.board.subscribers {
        select {
        case sub <- task:
        default:
            // 订阅者忙，跳过
        }
    }

    // 启动竞标计时器
    go m.runAuction(ctx, task)
}

// runAuction 运行任务拍卖
func (m *AgentMarket) runAuction(ctx context.Context, task *MarketTask) {
    // 等待一段时间收集出价
    select {
    case <-time.After(30 * time.Second):
    case <-ctx.Done():
        return
    }

    m.board.mu.Lock()
    bids := task.Bids
    m.board.mu.Unlock()

    if len(bids) == 0 {
        // 没有代理竞标 → 强制分配（回退到 spawn 模式）
        m.forceAssign(task)
        return
    }

    // 选择最优出价
    winner := m.selectWinner(bids, task)
    task.AwardedTo = winner.AgentID
    task.Status = TaskAwarded

    // 通知中标代理
    m.notifyWinner(ctx, winner, task)
}

// selectWinner 综合考虑出价质量、声誉、历史成功率
func (m *AgentMarket) selectWinner(bids []*Bid, task *MarketTask) *Bid {
    var best *Bid
    var bestScore float64
    for _, bid := range bids {
        // 综合评分 = 声誉 × 0.4 + 置信度 × 0.3 + 速度因素 × 0.2 + 价格因素 × 0.1
        speedFactor := 1.0 / math.Max(float64(bid.EstimatedDuration.Seconds()), 1)
        priceFactor := 1.0 / math.Max(bid.Price, 0.01)
        score := bid.Reputation*0.4 +
            bid.Confidence*0.3 +
            speedFactor*0.2 +
            priceFactor*0.1
        if score > bestScore {
            bestScore = score
            best = bid
        }
    }
    return best
}
```

---

## 三、声誉系统

```go
// internal/collective/reputation.go

// ReputationSystem 基于链上记录管理代理声誉
type ReputationSystem struct {
    store      *cortex.Store  // 持久化到统一记忆皮层
    decayRate  float64        // 声誉衰减率 (旧记录权重降低)
}

// ReputationRecord 声誉记录
type ReputationRecord struct {
    AgentID        string
    TaskID         string
    TaskCategory   string    // "coding", "analysis", "debate", "review"
    Outcome        string    // "success", "failure", "timeout"
    QualityScore   float64   // 0-1, 由任务发起方评分
    CompletedAt    time.Time
}

// GetReputation 计算代理当前声誉分
func (rs *ReputationSystem) GetReputation(ctx context.Context, agentID string) float64 {
    records := rs.store.GetReputationRecords(ctx, agentID, 30*24*time.Hour) // 最近 30 天

    if len(records) == 0 {
        return 0.5  // 新代理初始声誉
    }

    var weightedSum, totalWeight float64
    for _, rec := range records {
        // 时间衰减: 越近的记录权重越大
        age := time.Since(rec.CompletedAt)
        weight := math.Exp(-rs.decayRate * age.Hours())

        score := 0.0
        switch rec.Outcome {
        case "success":
            score = rec.QualityScore
        case "failure":
            score = 0.0
        case "timeout":
            score = 0.1
        }

        weightedSum += score * weight
        totalWeight += weight
    }

    return weightedSum / totalWeight
}

// GetCategoryReputation 获取分类声誉（代理可能编码强但分析弱）
func (rs *ReputationSystem) GetCategoryReputation(ctx context.Context, agentID, category string) float64 {
    records := rs.store.GetReputationRecordsByCategory(ctx, agentID, category, 30*24*time.Hour)
    // 同上逻辑，但只过滤该分类
}
```

---

## 四、涌现角色分工

```go
// internal/collective/specialization.go

// SpecializationEngine 监测代理的专长形成
type SpecializationEngine struct {
    market     *AgentMarket
    reputation *ReputationSystem
}

// DetectEmergentRoles 检测涌现的角色
func (se *SpecializationEngine) DetectEmergentRoles(ctx context.Context) []*EmergentRole {
    agents := se.market.registry.All()
    var roles []*EmergentRole

    for _, agent := range agents {
        // 获取各分类声誉
        categories := []string{"coding", "analysis", "debate", "review", "browser", "data"}
        var bestCat string
        var bestScore float64
        for _, cat := range categories {
            score := se.reputation.GetCategoryReputation(ctx, agent.ID, cat)
            if score > bestScore {
                bestScore = score
                bestCat = cat
            }
        }

        // 如果某分类明显突出 → 角色涌现
        if bestScore > 0.7 {
            roles = append(roles, &EmergentRole{
                AgentID:    agent.ID,
                Role:       bestCat,
                Confidence: bestScore,
                SuggestedLabel: se.roleLabel(bestCat),
            })
        }
    }
    return roles
}

func (se *SpecializationEngine) roleLabel(category string) string {
    labels := map[string]string{
        "coding":   "Code Architect",
        "analysis": "Data Analyst",
        "debate":   "Devil's Advocate",
        "review":   "Quality Guardian",
        "browser":  "Web Explorer",
        "data":     "Data Engineer",
    }
    return labels[category]
}
```

---

## 五、群体共识与辩论

```go
// internal/collective/consensus.go

// ConsensusEngine 多代理共识协议
// 当需要做关键决策时，多个代理各自给出判断，投票决定
type ConsensusEngine struct {
    market     *AgentMarket
    quorum     int     // 最少参与代理数
    threshold  float64 // 通过阈值 (0.5 = 简单多数, 0.67 = 绝对多数)
}

// ReachConsensus 就一个问题寻求群体共识
func (ce *ConsensusEngine) ReachConsensus(ctx context.Context, question string) (*ConsensusResult, error) {
    // 1. 广播问题到所有具备评判能力的代理
    voters := ce.market.registry.FilterBySkill("debate")
    if len(voters) < ce.quorum {
        return nil, fmt.Errorf("insufficient voters: %d < %d", len(voters), ce.quorum)
    }

    // 2. 收集投票
    votes := make([]*Vote, len(voters))
    var wg sync.WaitGroup
    for i, voter := range voters {
        wg.Add(1)
        go func(idx int, ag *Agent) {
            defer wg.Done()
            votes[idx] = ag.Vote(ctx, question)
        }(i, voter)
    }
    wg.Wait()

    // 3. 加权计票（声誉加权）
    var forWeight, againstWeight float64
    for _, v := range votes {
        rep := ce.market.reputation.GetReputation(ctx, v.AgentID)
        if v.Decision {
            forWeight += rep
        } else {
            againstWeight += rep
        }
    }

    totalWeight := forWeight + againstWeight
    var approved bool
    if totalWeight > 0 {
        approved = forWeight/totalWeight >= ce.threshold
    }

    return &ConsensusResult{
        Approved:    approved,
        ForWeight:   forWeight,
        AgainstWeight: againstWeight,
        VoterCount:  len(votes),
        Votes:       votes,
    }, nil
}
```

---

## 六、验收标准

1. **市场竞标**: 任务被发布后，多个代理在 30 秒内提交出价，最优出价中标
2. **声誉分化**: 运行 100 个任务后，代理在不同分类的声誉分出现显著差异（标准差 > 0.2）
3. **涌现角色**: 不用人工指定，代理自动获得 "Code Architect" / "Data Analyst" 等标签
4. **共识决策**: 群体投票的准确率 > 单个代理的准确率
5. **市场效率**: 竞标机制选出的代理比随机分配好 > 15%（成功率）
