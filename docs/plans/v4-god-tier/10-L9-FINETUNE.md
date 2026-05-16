# L9 — Fine-Tuning Pipeline (微调管路)

> 优先级: P9 | 工作量: 2-3 周 | 依赖: L7 (需要 Guardian 质量评分做数据筛选)  
> 闭环自举: 收集高质量交互 → 构建数据集 → 微调小模型 → A/B 测试 → 自动部署。

---

## 一、闭环流程

```
┌─────────────────────────────────────────────────────────────┐
│                 Fine-Tuning Closed Loop                     │
│                                                             │
│  1. COLLECT         2. FILTER         3. FORMAT            │
│  ┌──────────┐      ┌──────────┐      ┌──────────┐         │
│  │ 交互历史  │ ───▶ │ Guardian │ ───▶ │ Instruc- │         │
│  │ + 记忆库  │      │ Judge    │      │ tion     │         │
│  │ + 用户反馈│      │ 筛选高分  │      │ Format   │         │
│  └──────────┘      └──────────┘      └──────────┘         │
│                                              │              │
│  6. DEPLOY          5. A/B TEST        4. TRAIN           │
│  ┌──────────┐      ┌──────────┐      ┌──────────┐         │
│  │ 替换默认  │ ◀─── │ 新模型   │ ◀─── │ LoRA /   │         │
│  │ 模型路由  │      │ vs 旧模型 │      │ QLoRA    │         │
│  │ 自动切换  │      │ on Eval   │      │ Fine-tune│         │
│  └──────────┘      └──────────┘      └──────────┘         │
└─────────────────────────────────────────────────────────────┘
```

---

## 二、数据集构建器

```go
// internal/finetune/dataset_builder.go

// DatasetBuilder 从 Cortex 记忆中构建微调数据集
type DatasetBuilder struct {
    cortex       *cortex.Store
    judge        *guardian.OnlineJudge
    minQuality   float64  // 最低质量分数 (0.0-1.0)，默认 0.7
    format       DatasetFormat
}

// BuildDataset 构建微调数据集
func (db *DatasetBuilder) BuildDataset(ctx context.Context, opts BuildOptions) (*Dataset, error) {
    // 1. 从 Cortex 检索高质量交互
    interactions := db.cortex.QueryInteractions(ctx, cortex.InteractionQuery{
        MinUserRating:  opts.MinUserRating,  // 默认 0.5
        MinSuccessRate: 0.8,
        CreatedAfter:   time.Now().Add(-opts.LookbackPeriod),  // 默认 30 天
        Limit:          opts.MaxSamples,     // 默认 10000
    })

    var samples []*TrainingSample

    for _, inter := range interactions {
        // 2. Guardian Judge 二次筛选
        judgment, err := db.judge.Judge(ctx, inter.ToJudgeSession())
        if err != nil {
            continue
        }
        if judgment.OverallScore < db.minQuality {
            continue
        }

        // 3. 格式化为训练样本
        sample := db.format.Format(ctx, inter, judgment)
        if sample != nil {
            samples = append(samples, sample)
        }
    }

    // 4. 去重 (基于语义相似度)
    samples = db.deduplicate(ctx, samples)

    // 5. 平衡各类别
    samples = db.balanceCategories(samples)

    slog.Info("finetune: dataset built",
        "total_interactions", len(interactions),
        "quality_samples", len(samples),
    )

    return &Dataset{
        Samples:   samples,
        CreatedAt: time.Now(),
        Stats:     db.computeStats(samples),
    }, nil
}

// TrainingSample 单个训练样本
type TrainingSample struct {
    ID        string
    System    string    // 系统提示词
    User      string    // 用户输入
    Assistant string    // Agent 输出 (期望输出)
    Metadata  SampleMetadata
}

type SampleMetadata struct {
    SessionID     string
    QualityScore  float64
    Complexity    string
    ToolsUsed     []string
    Category      string  // "coding", "analysis", "chat", "writing"
}
```

---

## 三、格式支持

```go
// internal/finetune/formats.go

// DatasetFormat 支持多种微调格式
type DatasetFormat interface {
    Format(ctx context.Context, inter *Interaction, judgment *JudgeResult) *TrainingSample
    Export(dataset *Dataset, w io.Writer) error
}

// OpenAI Chat Format (用于官方 fine-tuning API)
type OpenAIChatFormat struct{}

func (f *OpenAIChatFormat) Export(dataset *Dataset, w io.Writer) error {
    // {"messages": [{"role": "system", "content": "..."}, {"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]}
    for _, s := range dataset.Samples {
        record := map[string]interface{}{
            "messages": []map[string]string{
                {"role": "system", "content": s.System},
                {"role": "user", "content": s.User},
                {"role": "assistant", "content": s.Assistant},
            },
        }
        json.NewEncoder(w).Encode(record)
    }
    return nil
}

// Alpaca Format (用于 LLaMA-Factory / unsloth)
type AlpacaFormat struct{}

func (f *AlpacaFormat) Export(dataset *Dataset, w io.Writer) error {
    // {"instruction": "...", "input": "...", "output": "...", "system": "..."}
    for _, s := range dataset.Samples {
        record := map[string]string{
            "instruction": s.User,
            "input":       "",
            "output":      s.Assistant,
            "system":      s.System,
        }
        json.NewEncoder(w).Encode(record)
    }
    return nil
}

// ShareGPT Format (用于 Axolotl / LLaMA-Factory)
type ShareGPTFormat struct{}

func (f *ShareGPTFormat) Export(dataset *Dataset, w io.Writer) error {
    // {"conversations": [{"from": "system", "value": "..."}, {"from": "human", "value": "..."}, {"from": "gpt", "value": "..."}]}
    for _, s := range dataset.Samples {
        record := map[string]interface{}{
            "conversations": []map[string]string{
                {"from": "system", "value": s.System},
                {"from": "human", "value": s.User},
                {"from": "gpt", "value": s.Assistant},
            },
        }
        json.NewEncoder(w).Encode(record)
    }
    return nil
}
```

---

## 四、训练执行器

```go
// internal/finetune/trainer.go

// TrainConfig 微调配置
type TrainConfig struct {
    BaseModel     string  // "Qwen/Qwen2.5-7B-Instruct" 或本地路径
    Method        string  // "lora", "qlora", "full"
    LoRARank      int     // 默认 16
    LoRAAlpha     int     // 默认 32
    LearningRate  float64
    NumEpochs     int
    BatchSize     int
    MaxSeqLength  int     // 默认 4096
    OutputDir     string
}

// Trainer 封装微调过程
type Trainer struct {
    config   TrainConfig
    executor TrainExecutor  // 接口: 支持 llama.cpp / unsloth / OpenAI API
}

// TrainExecutor 训练执行接口 — 支持多种后端
type TrainExecutor interface {
    Train(ctx context.Context, dataset *Dataset, config TrainConfig) (*TrainResult, error)
    Cancel() error
    Progress() *TrainProgress
}

// UnslothExecutor 使用 unsloth (Python) 在本地微调
type UnslothExecutor struct {
    pythonBin string
    scriptDir string
}

func (e *UnslothExecutor) Train(ctx context.Context, dataset *Dataset, cfg TrainConfig) (*TrainResult, error) {
    // 1. 导出数据集为 Alpaca JSONL
    datasetPath := filepath.Join(cfg.OutputDir, "dataset.jsonl")
    f, _ := os.Create(datasetPath)
    (&AlpacaFormat{}).Export(dataset, f)
    f.Close()

    // 2. 生成 unsloth 训练脚本
    trainScript := e.generateUnslothScript(datasetPath, cfg)

    // 3. 执行训练
    cmd := exec.CommandContext(ctx, e.pythonBin, "-c", trainScript)
    cmd.Dir = cfg.OutputDir
    output, err := cmd.CombinedOutput()

    if err != nil {
        return nil, fmt.Errorf("training failed: %w\nOutput: %s", err, output)
    }

    return &TrainResult{
        ModelPath: filepath.Join(cfg.OutputDir, "lora_adapter"),
        Config:    cfg,
    }, nil
}

// OpenAIExecutor 使用 OpenAI Fine-tuning API
type OpenAIExecutor struct {
    apiKey string
}

func (e *OpenAIExecutor) Train(ctx context.Context, dataset *Dataset, cfg TrainConfig) (*TrainResult, error) {
    // 1. 上传数据集
    // 2. 创建 fine-tuning job
    // 3. 轮询直到完成
    // ...
}
```

---

## 五、A/B 测试与部署

```go
// internal/finetune/deploy.go

// ABTester 对微调前后的模型进行 A/B 测试
type ABTester struct {
    evalRunner   *eval.Runner
    oldModel     string
    newModelPath string
    trafficSplit float64  // 新模型的流量比例，默认 0.2
}

// RunABTest 运行 A/B 测试
func (ab *ABTester) RunABTest(ctx context.Context) (*ABTestResult, error) {
    // 1. 在两个模型上各跑 eval suite
    oldResult := ab.evalRunner.RunWithModel(ctx, ab.oldModel, "regression")
    newResult := ab.evalRunner.RunWithModel(ctx, ab.newModelPath, "regression")

    // 2. 统计对比
    comparison := ab.evalRunner.Compare(newResult, oldResult)

    // 3. 判断是否提升
    var decision DeployDecision
    if comparison.SuccessRateDelta > 0.05 {  // 提升 > 5%
        decision = DeployPromote  // 全量切换
    } else if comparison.SuccessRateDelta > 0 {
        decision = DeployShadow   // 影子模式 (新模型并行运行但不影响用户)
    } else {
        decision = DeployReject   // 拒绝
    }

    return &ABTestResult{
        OldModel:       ab.oldModel,
        NewModel:       ab.newModelPath,
        Comparison:     comparison,
        Decision:       decision,
    }, nil
}

// AutoDeploy 自动部署逻辑
func (ab *ABTester) AutoDeploy(ctx context.Context, result *ABTestResult) error {
    switch result.Decision {
    case DeployPromote:
        slog.Info("finetune: promoting new model", "path", ab.newModelPath)
        // 更新 ModelRouter: 将某个 complexity 类型的流量路由到新模型
        ab.updateModelRouter(ab.newModelPath, 1.0)  // 100% 流量

    case DeployShadow:
        slog.Info("finetune: deploying in shadow mode")
        ab.updateModelRouter(ab.newModelPath, 0.1)  // 10% 影子流量
        // 继续收集 A/B 数据

    case DeployReject:
        slog.Info("finetune: rejecting new model (no improvement)")
        // 保留旧模型，清理新模型
    }
    return nil
}
```

---

## 六、调度与触发

```go
// Fine-tuning 触发条件 (可配置):
type FinetuneTrigger struct {
    MinNewSamples     int           // 累积多少新样本后触发, 默认 500
    MinQualityThreshold float64     // 平均质量阈值, 默认 0.75
    CooldownPeriod    time.Duration // 两次微调的最小间隔, 默认 7 天
    AutoDeployEnabled bool          // 是否自动部署, 默认 false (需要人工审核)
}

// 集成到 evolution engine 的后台循环
func (gw *Gateway) startFinetuneCycle(ctx context.Context) {
    trigger := gw.cfg.Finetune.Trigger
    ticker := time.NewTicker(24 * time.Hour)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // 检查是否满足触发条件
            newSamples := gw.datasetBuilder.CountNewSamples(ctx, trigger.CooldownPeriod)
            if newSamples < trigger.MinNewSamples {
                continue
            }

            avgQuality := gw.datasetBuilder.AvgQuality(ctx, trigger.CooldownPeriod)
            if avgQuality < trigger.MinQualityThreshold {
                continue
            }

            slog.Info("finetune: trigger conditions met, starting cycle",
                "new_samples", newSamples,
                "avg_quality", avgQuality,
            )

            // 运行完整闭环
            gw.runFinetuneCycle(ctx)
        }
    }
}
```

---

## 七、CLI

```bash
# 构建数据集
ironclaw finetune build-dataset \
  --min-quality 0.7 \
  --format alpaca \
  --output ./datasets/latest.jsonl

# 查看数据集统计
ironclaw finetune stats --dataset ./datasets/latest.jsonl
# → 1,247 samples
# → Categories: coding(423), analysis(312), chat(289), writing(223)
# → Avg quality: 0.83
# → Complexity distribution: simple(45%), moderate(38%), complex(17%)

# 启动微调
ironclaw finetune train \
  --base-model Qwen/Qwen2.5-7B-Instruct \
  --method qlora \
  --dataset ./datasets/latest.jsonl \
  --output ./models/ironclaw-v1

# A/B 测试
ironclaw finetune ab-test \
  --old claude-sonnet-4-20250514 \
  --new ./models/ironclaw-v1 \
  --suite regression

# 部署
ironclaw finetune deploy --model ./models/ironclaw-v1 --traffic 0.2
```

---

## 八、验收标准

1. **数据集质量**: 构建的数据集中 Guardian Judge 评分 > 0.7 的样本占比 > 80%
2. **格式兼容**: 支持 OpenAI Chat / Alpaca / ShareGPT 三种格式导出
3. **微调成功**: LoRA/QLoRA 微调完成率 > 95%（不会 OOM 或崩溃）
4. **A/B 有效**: A/B 测试能正确检测 5% 以上的成功率差异（p < 0.05）
5. **自动部署**: 满足触发条件后，全自动完成 收集→训练→测试→部署 闭环
