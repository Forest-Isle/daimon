# L2 — True Self-Evolution V2 (真正自主进化)

> 优先级: P3 | 工作量: 3-4 周 | 依赖: L0 (需要统一记忆做数据源)  
> 从参数调优跨越到真正的自主改进：提示词自动优化、工具合成、遗传策略搜索。

---

## 一、现状 vs 目标

| 维度 | 当前 v3 Evolution | V4 True Evolution |
|------|-------------------|-------------------|
| 优化对象 | replan 阈值、工具优先级 | 提示词模板、工具本身、规划策略空间 |
| 方法 | 滑动窗口 + 启发式 | 遗传算法 + Bayesian Optimization + 消融实验 |
| 工具生成 | ❌ 不存在 | 自动发现缺口 → 生成 WASM 模板 |
| 提示词优化 | ❌ 不存在 | DSPy 风格的多候选 A/B + 自动编译 |
| 回滚 | 简单阈值比较 | 多指标综合 + 统计显著性检验 |
| 数据源 | 单次 episode | 全量 Cortex 记忆 + 用户反馈 |

---

## 二、四大子系统

```
┌─────────────────────────────────────────────────────────────┐
│                True Evolution Engine V2                     │
│                                                             │
│  ┌──────────────────┐  ┌──────────────────┐                │
│  │ Prompt Optimizer │  │ Tool Synthesizer │                │
│  │ 提示词自动优化    │  │ 工具自动合成      │                │
│  │ DSPy风格编译     │  │ 缺口检测+WASM生成 │                │
│  └────────┬─────────┘  └────────┬─────────┘                │
│           │                     │                           │
│  ┌────────┴─────────────────────┴─────────┐                │
│  │        Genetic Strategy Search          │                │
│  │        遗传策略搜索                      │                │
│  │        MCTS深度|候选数|温度|搜索宽度      │                │
│  └────────┬────────────────────────────────┘                │
│           │                                                 │
│  ┌────────┴────────────────────────────────┐                │
│  │        Ablation Engine (消融实验)        │                │
│  │   "关掉X会怎样？" → 自动回归测试         │                │
│  └────────┬────────────────────────────────┘                │
│           │                                                 │
│  ┌────────┴────────────────────────────────┐                │
│  │        Rollback Guardian (回滚守卫)      │                │
│  │   p<0.05 退化 → 自动回滚 → 通知用户      │                │
│  └─────────────────────────────────────────┘                │
└─────────────────────────────────────────────────────────────┘
```

---

## 三、Prompt Optimizer — DSPy 风格的提示词编译

### 3.1 核心概念

传统做法：人工写 prompt → 试 → 改 → 试。  
DSPy 做法：定义评分函数 → 自动搜索最优 prompt 模板 → 编译。

```go
// internal/evolution/prompt_optimizer.go

// PromptCandidate 是一个提示词候选版本
type PromptCandidate struct {
    ID          string
    Template    string            // 提示词模板，包含 {{PLACEHOLDER}}
    Version     int
    Metrics     PromptMetrics     // 累积性能指标
    Active      bool              // 是否在 A/B 测试中活跃
}

type PromptMetrics struct {
    Impressions    int
    Successes      int
    AvgConfidence  float64
    AvgUserRating  float64   // -1 到 1
    AvgLatencyMs   int64
}

// PromptOptimizer 管理提示词的进化
type PromptOptimizer struct {
    candidates  map[string]*PromptCandidate  // keyed by prompt role (plan/reflect/etc)
    evaluator   *PromptEvaluator
    compiler    *PromptCompiler
    llm         Completer
    cortex      *cortex.Store
}

// RunOptimizationCycle 运行一个优化周期
func (po *PromptOptimizer) RunOptimizationCycle(ctx context.Context) error {
    for role, candidates := range po.candidates {
        // 1. 找到表现最差的候选
        worst := po.findWorst(candidates)

        // 2. 用 LLM 生成变体
        newTemplate := po.compiler.Mutate(ctx, worst.Template, worst.Metrics)

        // 3. 加入候选池
        newCandidate := &PromptCandidate{
            ID:       uuid.New().String(),
            Template: newTemplate,
            Version:  worst.Version + 1,
        }
        po.candidates[role] = append(po.candidates[role], newCandidate)

        // 4. 如果候选池超过 5 个，淘汰最差的
        if len(po.candidates[role]) > 5 {
            po.candidates[role] = po.prune(po.candidates[role], 5)
        }
    }
    return nil
}

// PromptCompiler 根据反馈改进提示词
type PromptCompiler struct {
    llm Completer
}

func (pc *PromptCompiler) Mutate(ctx context.Context, template string, metrics PromptMetrics) string {
    // 分析失败案例，诊断问题
    // 生成改进版 prompt
    systemPrompt := `You are a prompt engineer. Given a prompt template and its performance metrics,
generate an improved version. 

Diagnose what went wrong based on:
- Low success rate → unclear instructions? wrong examples? 
- Low confidence → ambiguous success criteria?
- High latency → too verbose? unnecessary steps?

Output ONLY the improved template, no explanation.`

    userPrompt := fmt.Sprintf(`
Current template:
---
%s
---
Metrics:
- Success rate: %.2f
- Avg confidence: %.2f
- Avg user rating: %.2f
`, template, float64(metrics.Successes)/float64(metrics.Impressions),
        metrics.AvgConfidence, metrics.AvgUserRating)

    newTemplate, _ := pc.llm.Complete(ctx, CompletionRequest{
        System: systemPrompt,
        Messages: []CompletionMessage{{Role: "user", Content: userPrompt}},
        MaxTokens: 2048,
    })
    return newTemplate
}
```

### 3.2 A/B 测试与选择

```go
// SelectBestPrompt 使用 Thompson Sampling 选择当前最优 prompt
func (po *PromptOptimizer) SelectBestPrompt(role string) *PromptCandidate {
    candidates := po.candidates[role]
    if len(candidates) == 1 {
        return candidates[0]
    }

    // Thompson Sampling: 每个候选的成功率视为 Beta 分布
    // 采样 → 选最高样本值 → 自动平衡探索与利用
    var best *PromptCandidate
    var bestSample float64
    for _, c := range candidates {
        alpha := float64(c.Metrics.Successes + 1)           // Beta(α, β) prior
        beta := float64(c.Metrics.Impressions - c.Metrics.Successes + 1)
        sample := sampleBeta(alpha, beta)
        if sample > bestSample {
            bestSample = sample
            best = c
        }
    }
    return best
}
```

---

## 四、Tool Synthesizer — 自动工具合成

```go
// internal/evolution/tool_synthesizer.go

type ToolSynthesizer struct {
    llm          Completer
    wasmHost     *wasm.PluginHost
    cortex       *cortex.Store
    gapDetector  *ToolGapDetector
}

// ToolGapDetector 检测工具缺口
// 当 agent 反复遇到 "需要 X 工具但不存在" 的情况，触发生成
type ToolGapDetector struct {
    // 统计分析: 哪些类型的任务经常失败？
    // 失败的共同特征: "tool not found", "cannot perform", "no tool available"
    failurePatterns map[string]*FailureCluster
}

// SynthesizeTool 基于需求描述自动生成 WASM 工具
func (ts *ToolSynthesizer) SynthesizeTool(ctx context.Context, need *ToolNeed) (*PluginManifest, error) {
    // 1. LLM 生成工具代码
    codeGenPrompt := fmt.Sprintf(`Generate a complete Go plugin (using TinyGo for WASM compilation) 
that implements a tool with the following requirements:

Name: %s
Description: %s
Inputs: %s
Expected output: %s

Use the github.com/ironclaw/wasm-sdk package. Only use allowed capabilities: %v.

Output ONLY the Go source code, fully functional.`, 
        need.Name, need.Description, need.Inputs, need.Output, need.AllowedCapabilities)

    sourceCode, _ := ts.llm.Complete(ctx, CompletionRequest{...})

    // 2. 编译为 WASM (TinyGo)
    wasmBytes, err := ts.compileTinyGo(ctx, sourceCode)
    if err != nil {
        return nil, fmt.Errorf("compile failed: %w", err)
    }

    // 3. 生成 manifest
    manifest := &PluginManifest{
        Name:         need.Name,
        Version:      "0.1.0-auto",
        Description:  need.Description,
        Capabilities: need.Capabilities,
        Runtime:      RuntimeConfig{WasmFile: fmt.Sprintf("%s.wasm", need.Name)},
    }

    // 4. 写入本地插件目录，等待用户审核
    dir := filepath.Join("~/.IronClaw/plugins/generated/", need.Name)
    os.MkdirAll(dir, 0755)
    os.WriteFile(filepath.Join(dir, "plugin.yaml"), manifest.ToYAML(), 0644)
    os.WriteFile(filepath.Join(dir, manifest.Runtime.WasmFile), wasmBytes, 0644)

    return manifest, nil
}
```

---

## 五、Genetic Strategy Search — 遗传算法策略搜索

```go
// internal/evolution/genetic.go

// StrategyGene 定义规划策略的一个基因组
type StrategyGene struct {
    MCTSSearchDepth    int       `json:"mcts_search_depth"`    // 10-100
    MCTSExplorationC   float64   `json:"mcts_exploration_c"`   // 0.5-2.0
    TreeExpansionWidth int       `json:"tree_expansion_width"` // 3-10
    PlannerTemperature float64   `json:"planner_temperature"`  // 0.1-2.0
    MaxParallelTools   int       `json:"max_parallel_tools"`   // 1-10
    ReplanThreshold    float64   `json:"replan_threshold"`     // 0.4-0.9
    ContextBudgetPct   float64   `json:"context_budget_pct"`   // 0.5-0.95
    ToolCacheEnabled   bool      `json:"tool_cache_enabled"`
    SpeculativeEnabled bool      `json:"speculative_enabled"`
}

// StrategyFitness 是基因的适应度
type StrategyFitness struct {
    SuccessRate    float64
    AvgDurationMs  int64
    AvgConfidence  float64
    UserSatisfaction float64
    CompositeScore float64  // 加权综合
}

// GeneticOptimizer 遗传优化器
type GeneticOptimizer struct {
    population    []*StrategyGene
    evalRunner    *eval.Runner      // 在 eval 套件上跑适应度
    generation    int
    populationSize int  // 默认 20
    eliteCount    int   // 保留前 3
}

// Evolve 运行一代进化
func (go *GeneticOptimizer) Evolve(ctx context.Context) error {
    // 1. 评估当前种群适应度
    for _, gene := range go.population {
        gene.Fitness = go.evaluate(ctx, gene)
    }

    // 2. 排序，选择 elite
    sort.Slice(go.population, func(i, j int) bool {
        return go.population[i].Fitness.CompositeScore > go.population[j].Fitness.CompositeScore
    })
    elites := go.population[:go.eliteCount]

    // 3. 交叉和变异生成下一代
    newPop := make([]*StrategyGene, 0, go.populationSize)
    newPop = append(newPop, elites...)  // 保留精英

    for len(newPop) < go.populationSize {
        parent1 := go.tournamentSelect()
        parent2 := go.tournamentSelect()

        child := go.crossover(parent1, parent2)
        child = go.mutate(child)

        newPop = append(newPop, child)
    }

    go.population = newPop
    go.generation++

    // 4. 记录最佳基因
    best := elites[0]
    slog.Info("genetic: generation complete",
        "gen", go.generation,
        "best_score", best.Fitness.CompositeScore,
        "best_success_rate", best.Fitness.SuccessRate,
    )

    return nil
}

// crossover 基因交叉
func (go *GeneticOptimizer) crossover(a, b *StrategyGene) *StrategyGene {
    // 均匀交叉: 每个基因位随机继承自父本或母本
    child := &StrategyGene{}
    if rand.Float64() < 0.5 {
        child.MCTSSearchDepth = a.MCTSSearchDepth
    } else {
        child.MCTSSearchDepth = b.MCTSSearchDepth
    }
    // ... 其他基因位
    return child
}

// mutate 基因突变
func (go *GeneticOptimizer) mutate(g *StrategyGene) *StrategyGene {
    mutationRate := 0.1
    if rand.Float64() < mutationRate {
        g.MCTSSearchDepth += rand.Intn(21) - 10  // ±10
        g.MCTSSearchDepth = clamp(g.MCTSSearchDepth, 10, 100)
    }
    // ... 其他基因位突变
    return g
}

// evaluate 在 eval 套件上评估基因适应度
func (go *GeneticOptimizer) evaluate(ctx context.Context, gene *StrategyGene) *StrategyFitness {
    // 创建一个临时 cognitive agent，使用该基因配置
    // 在本地 eval 套件上跑 → 收集指标
    results := go.evalRunner.RunWithConfig(ctx, gene.ToConfig())
    return computeFitness(results)
}
```

---

## 六、Ablation Engine — 消融实验

```go
// internal/evolution/ablation.go

// AblationEngine 自动测试"关掉 X 会怎样？"
type AblationEngine struct {
    components   []AblatableComponent
    evalRunner   *eval.Runner
    baseline     *AblationResult
}

type AblatableComponent struct {
    Name         string
    Description  string
    Enabled      bool
    CanDisable   bool  // 不是所有组件都可以安全关闭
}

// RunAblationStudy 对当前启用的所有组件做消融实验
func (ae *AblationEngine) RunAblationStudy(ctx context.Context) (*AblationReport, error) {
    report := &AblationReport{Components: make(map[string]*AblationResult)}

    // 基线: 当前配置跑一次
    ae.baseline = ae.runEval(ctx, nil)

    for _, comp := range ae.components {
        if !comp.CanDisable || !comp.Enabled {
            continue
        }

        // 关闭这个组件
        disabled := []string{comp.Name}
        result := ae.runEval(ctx, disabled)

        // 计算影响
        result.DeltaSuccessRate = result.SuccessRate - ae.baseline.SuccessRate
        result.DeltaConfidence = result.AvgConfidence - ae.baseline.AvgConfidence
        result.Significant = math.Abs(result.DeltaSuccessRate) > 0.05

        report.Components[comp.Name] = result

        // 如果关闭后反而更好 → 告警（这个组件有负贡献）
        if result.DeltaSuccessRate > 0.05 {
            slog.Warn("ablation: component has negative contribution!",
                "component", comp.Name,
                "delta_success", result.DeltaSuccessRate,
            )
        }
    }

    return report, nil
}
```

---

## 七、验收标准

1. **提示词优化**: 优化后的 planner prompt 相比初始版本，任务成功率提升 > 10%
2. **工具合成**: 给定自然语言需求描述，生成可工作的 WASM 工具（编译通过即可，功能准确性 > 60%）
3. **遗传搜索**: 自动找到的策略配置比默认配置好 > 5%（eval 套件测量）
4. **消融实验**: 自动检测负贡献组件并告警
5. **回滚**: 任何策略改动如果导致 eval 成功率下降 > 5%，24 小时内自动回滚
