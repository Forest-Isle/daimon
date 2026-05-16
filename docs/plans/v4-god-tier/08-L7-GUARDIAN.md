# L7 — Guardian System (在线质量守护)

> 优先级: P7 | 工作量: 2-3 周 | 依赖: L0, L2  
> 持续监控 Agent 质量，检测漂移，自动回滚，LLM-as-Judge 在线评分。

---

## 一、系统概览

```
┌──────────────────────────────────────────────────────────┐
│                 Guardian System                          │
│                                                          │
│  ┌──────────────────┐  ┌──────────────────────────────┐ │
│  │ Drift Detector   │  │ Online Judge                 │ │
│  │ 漂移检测          │  │ LLM-as-Judge 持续评分        │ │
│  │ 滑动窗口统计      │  │ 每个完成的会话自动评估        │ │
│  │ 多指标综合判断    │  │ 准确性/完整性/效率 三维评分   │ │
│  └────────┬─────────┘  └──────────────┬───────────────┘ │
│           │                           │                  │
│  ┌────────┴───────────────────────────┴───────────────┐ │
│  │              Regression Guard                       │ │
│  │  任何策略/提示词/配置改动 → 自动回归测试              │ │
│  │  如果 p<0.05 退化 → 自动回滚 + 通知                  │ │
│  └────────┬────────────────────────────────────────────┘ │
│           │                                              │
│  ┌────────┴────────────────────────────────────────────┐ │
│  │              Quality Dashboard                       │ │
│  │  实时质量指标 | 趋势图 | 告警历史 | 回滚记录          │ │
│  └─────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

---

## 二、漂移检测器

```go
// internal/guardian/drift_detector.go

// DriftDetector 监控 Agent 质量是否在退化
type DriftDetector struct {
    window     *SlidingWindow
    baseline   *BaselineStats
    config     DriftConfig
    alertCh    chan DriftAlert
}

type DriftConfig struct {
    WindowSize        int           // 滑动窗口大小，默认 100 个样本
    BaselinePeriod    time.Duration // 基线采集周期，默认 7 天
    DriftThreshold    float64       // 漂移告警阈值，默认 2.0 (2σ)
    CheckInterval     time.Duration // 检查间隔，默认 1 小时
}

type SlidingWindow struct {
    samples []*QualitySample
    size    int
    idx     int
    mu      sync.RWMutex
}

type QualitySample struct {
    Timestamp       time.Time
    Success         bool
    Confidence      float64
    UserFeedback    float64   // -1 到 1
    DurationMs      int64
    ToolSuccessRate float64   // 工具执行成功率
    ReplanCount     int
    Complexity      string    // simple/moderate/complex
}

// BaselineStats 基线统计
type BaselineStats struct {
    SuccessRate      float64
    MeanConfidence   float64
    StdConfidence    float64
    MeanDuration     float64
    MeanToolSuccess  float64
    MeanReplanCount  float64
    SampleCount      int
    Period           TimeRange
}

// CheckDrift 检查当前窗口是否偏离基线
func (dd *DriftDetector) CheckDrift(ctx context.Context) *DriftReport {
    dd.window.mu.RLock()
    recent := dd.window.samples
    dd.window.mu.RUnlock()

    if len(recent) < 10 {
        return &DriftReport{Status: DriftStatusInsufficientData}
    }

    // 计算当前窗口的统计量
    current := dd.computeStats(recent)

    // 计算每个指标的 Z-score
    zScores := map[string]float64{
        "success_rate":    zScore(current.SuccessRate, dd.baseline.SuccessRate, dd.baseline.StdSuccessRate),
        "confidence":      zScore(current.MeanConfidence, dd.baseline.MeanConfidence, dd.baseline.StdConfidence),
        "tool_success":    zScore(current.MeanToolSuccess, dd.baseline.MeanToolSuccess, dd.baseline.StdToolSuccess),
    }

    // 综合漂移分数 = 所有 Z-score 的加权绝对值和
    driftScore := 0.0
    for _, z := range zScores {
        driftScore += math.Abs(z)
    }
    driftScore /= float64(len(zScores))

    report := &DriftReport{
        Status:     DriftStatusOK,
        DriftScore: driftScore,
        ZScores:    zScores,
        Current:    current,
        Baseline:   dd.baseline,
    }

    if driftScore > dd.config.DriftThreshold {
        report.Status = DriftStatusDrifting
        // 发送告警
        select {
        case dd.alertCh <- DriftAlert{
            Severity: "warning",
            Message:  fmt.Sprintf("Agent quality drift detected (score: %.2f, threshold: %.2f)", driftScore, dd.config.DriftThreshold),
            Report:   report,
        }:
        default:
        }
    }

    return report
}

// zScore 计算 Z-score（负值表示退化）
// 注意: 对于成功率等指标，我们希望越高越好
// 所以 Z-score 为负 → 质量下降
func zScore(current, baseline, stdDev float64) float64 {
    if stdDev == 0 {
        return 0
    }
    return (current - baseline) / stdDev
}
```

---

## 三、在线裁判 (LLM-as-Judge)

```go
// internal/guardian/online_judge.go

// OnlineJudge 对每次 Agent 交互进行自动质量评估
type OnlineJudge struct {
    llm        Completer
    sampleRate float64  // 采样率 (0.0-1.0)，不是所有会话都评估
    criteria   []JudgingCriterion
}

type JudgingCriterion struct {
    Name        string   // "accuracy", "completeness", "efficiency", "safety"
    Description string
    Weight      float64
}

// JudgeResult 单次评判结果
type JudgeResult struct {
    SessionID     string
    Scores        map[string]float64  // criterion name → score (0-1)
    OverallScore  float64
    Strengths     []string
    Weaknesses    []string
    IsHallucination bool
    Explanation   string
}

// Judge 评判一次 Agent 交互的质量
func (oj *OnlineJudge) Judge(ctx context.Context, session *JudgeSession) (*JudgeResult, error) {
    // 构建评判 prompt
    prompt := oj.buildJudgePrompt(session)

    resp, err := oj.llm.Complete(ctx, CompletionRequest{
        System: `You are an objective quality judge for an AI agent. 
Rate the agent's response on these criteria (0.0-1.0):

1. Accuracy: Did the agent correctly understand the user's request? Is the answer factually correct?
2. Completeness: Did the agent address all parts of the request?
3. Efficiency: Did the agent solve the problem with minimal unnecessary steps?
4. Helpfulness: Was the response actionable and useful?

Output ONLY valid JSON:
{
  "scores": {"accuracy": 0.0, "completeness": 0.0, "efficiency": 0.0, "helpfulness": 0.0},
  "overall_score": 0.0,
  "strengths": ["..."],
  "weaknesses": ["..."],
  "is_hallucination": false,
  "explanation": "..."
}`,
        Messages: []CompletionMessage{{Role: "user", Content: prompt}},
        MaxTokens: 1024,
    })

    if err != nil {
        return nil, err
    }

    var result JudgeResult
    json.Unmarshal([]byte(resp.Text), &result)
    result.SessionID = session.ID

    // 记录到 Cortex 用于趋势分析
    oj.recordJudgment(ctx, &result)

    return &result, nil
}

func (oj *OnlineJudge) buildJudgePrompt(session *JudgeSession) string {
    return fmt.Sprintf(`
User request: %s

Agent's final answer: %s

Execution summary:
- Mode: %s
- Complexity: %s
- Duration: %s
- Tools used: %v
- Replan count: %d
- Tool success rate: %.0f%%

Please judge the quality of this interaction.`, 
        session.UserRequest,
        session.FinalAnswer,
        session.Mode,
        session.Complexity,
        time.Duration(session.DurationMs).String(),
        session.ToolsUsed,
        session.ReplanCount,
        session.ToolSuccessRate*100,
    )
}
```

---

## 四、回归守卫

```go
// internal/guardian/regression_guard.go

// RegressionGuard 在每次策略/配置变更后自动触发回归测试
type RegressionGuard struct {
    evalRunner   *eval.Runner
    baseline     *eval.SuiteResult
    drift         *DriftDetector
    autoRollback  bool
}

// Guard 守卫一个变更
func (rg *RegressionGuard) Guard(ctx context.Context, change *StrategyChange) error {
    // 1. 跑 eval suite
    result, err := rg.evalRunner.RunSuite(ctx, "regression")
    if err != nil {
        return fmt.Errorf("eval failed: %w", err)
    }

    // 2. 与基线比较
    comparison := rg.evalRunner.Compare(result, rg.baseline)

    // 3. 判断是否退化
    if comparison.SuccessRateDelta < -0.05 {  // 成功率下降超过 5%
        slog.Error("regression-guard: significant regression detected",
            "delta_success_rate", comparison.SuccessRateDelta,
            "delta_confidence", comparison.ConfidenceDelta,
            "change", change.Description,
        )

        if rg.autoRollback {
            // 自动回滚
            if err := rg.rollback(ctx, change); err != nil {
                return fmt.Errorf("auto-rollback failed: %w", err)
            }

            // 通知用户
            rg.notify(fmt.Sprintf(
                "Auto-rollback: strategy change '%s' caused %.1f%% success rate drop. Reverted.",
                change.Description, math.Abs(comparison.SuccessRateDelta)*100,
            ))
        }

        return fmt.Errorf("regression detected: success rate dropped %.1f%%",
            math.Abs(comparison.SuccessRateDelta)*100)
    }

    // 4. 如果改善了，更新基线
    if comparison.SuccessRateDelta > 0.05 {
        slog.Info("regression-guard: improvement detected, updating baseline")
        rg.baseline = result
    }

    return nil
}
```

---

## 五、告警与通知

```go
// internal/guardian/alert.go

type AlertManager struct {
    rules    []AlertRule
    notifier AlertNotifier
}

type AlertRule struct {
    Name        string
    Condition   func(*QualitySnapshot) bool
    Severity    string  // "info", "warning", "critical"
    Cooldown    time.Duration  // 相同告警的最小间隔
}

func DefaultAlertRules() []AlertRule {
    return []AlertRule{
        {
            Name: "success-rate-drop",
            Condition: func(s *QualitySnapshot) bool {
                return s.DriftScore > 2.0
            },
            Severity: "warning",
            Cooldown: 6 * time.Hour,
        },
        {
            Name: "critical-quality-loss",
            Condition: func(s *QualitySnapshot) bool {
                // 比 baseline 低 3σ
                return s.DriftScore > 3.0
            },
            Severity: "critical",
            Cooldown: 1 * time.Hour,
        },
        {
            Name: "hallucination-spike",
            Condition: func(s *QualitySnapshot) bool {
                return s.HallucinationRate > 0.15  // 15% 幻觉率
            },
            Severity: "critical",
            Cooldown: 1 * time.Hour,
        },
        {
            Name: "latency-spike",
            Condition: func(s *QualitySnapshot) bool {
                return s.P95LatencyMs > s.BaselineP95LatencyMs * 2
            },
            Severity: "info",
            Cooldown: 12 * time.Hour,
        },
    }
}
```

---

## 六、验收标准

1. **漂移检测**: 成功率下降 5%+ 时，2 小时内检测到并告警
2. **在线裁判**: 评分与人工评分的相关系数 > 0.7
3. **自动回滚**: 策略变更导致退化时，自动回滚的成功率 100%
4. **误报率**: 正常波动被误判为漂移的概率 < 5%
5. **幻觉检测**: 对明显幻觉的检测率 > 80%
