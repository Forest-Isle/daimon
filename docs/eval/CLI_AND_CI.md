# CLI 命令全览、Fixture 格式与 CI/CD

> 源文件：`cmd/ironclaw/eval.go`、`cmd/ironclaw/eval_visualize.go`、`internal/eval/fixtures.go`（及 `fixtures_*.go`）、`internal/eval/taskset.go`、`.github/workflows/eval-regression.yml`

---

## 目录

1. [CLI 命令全览](#1-cli-命令全览)
2. [eval run](#2-eval-run)
3. [eval compare](#3-eval-compare)
4. [eval list](#4-eval-list)
5. [eval longitudinal](#5-eval-longitudinal)
6. [eval visualize](#6-eval-visualize)
7. [eval diagnose](#7-eval-diagnose)
8. [eval adaptive](#8-eval-adaptive)
9. [eval benchmark](#9-eval-benchmark)
10. [eval self-learning](#10-eval-self-learning)
11. [内置 Fixture 套件](#11-内置-fixture-套件)
12. [外部任务文件格式](#12-外部任务文件格式)
13. [CI/CD 工作流](#13-cicd-工作流)

---

## 1. CLI 命令全览

```
ironclaw eval
├── run           单次套件运行
├── compare       两次运行结果对比
├── list          列出套件中的任务
├── longitudinal  多轮纵向学习评测
├── visualize     纵向结果 HTML 可视化
├── diagnose      套件运行 + 弱点诊断 + 雷达图
├── adaptive      多轮自适应弱点补强
├── benchmark     标准基准测试（swe-bench/humaneval/gaia）
└── self-learning 自学习能力综合评测
```

所有命令的 `--live` 为 false 时使用 `DryRunner`（无 LLM），`--live` 为 true 时通过 `initEvalGateway` 启动真实 CognitiveAgent。

---

## 2. eval run

```bash
ironclaw eval run [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--suite` | — | `builtin` | 套件名（见 §11）或文件路径（.yaml/.json） |
| `--live` | — | `false` | 启用真实 LLM 评测 |
| `--judge` | — | `false` | 启用 LLM Judge 评分（需要 Rubric） |
| `--output` | `-o` | — | JSON 结果输出路径 |
| `--run-id` | — | 自动生成 | 运行 ID（UUID） |
| `--config` | `-c` | — | 配置文件路径（live 模式必需） |

### 输出

```
  [1/8] bash-echo — PASS (0.0s)
  [2/8] bash-multi-step — PASS (0.1s)
  [3/8] file-write-read — FAIL (0.2s)
  ...

Suite: 7/8 passed (87.5%) in 1.2s
```

若指定 `-o`，结果写入 JSON 文件（`SuiteResult` 格式）。

### 内部流程

```
eval run
├─ loadSuite(suite)
├─ if live: initEvalGateway(config) → gateway.NewEvalRunner()
│  else: &DryRunner{}
├─ if judge: NewLLMJudge(provider)
├─ RunSuiteWithOptions(tasks, runner, {Judge})
└─ SuiteResult.SaveJSON(output)
```

---

## 3. eval compare

```bash
ironclaw eval compare --before baseline.json --after current.json [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--before` | — | — | 基线 JSON 路径（必需） |
| `--after` | — | — | 对比 JSON 路径（必需） |
| `--fail-on-regression` | — | `false` | 存在回归时 exit 1 |
| `--json` | — | `false` | 输出 JSON 格式（否则 Markdown） |

### 输出示例（Markdown）

```markdown
## Comparison Report

| Metric           | Before | After  | Delta  |
|------------------|--------|--------|--------|
| Success Rate     | 75.0%  | 87.5%  | +12.5% |
| Avg Final Score  | 0.68   | 0.79   | +0.11  |

## Regressions (1)
- task-03: bash-multi-step

## Improvements (3)
- task-05: file-write-read
- task-07: bash-pipeline
- task-08: multi-tool-compose

## Evolution Snapshot Delta
| Field                  | Before | After | Delta |
|------------------------|--------|-------|-------|
| Preference Count       | 8      | 15    | +7    |
| Strategy Version       | 2      | 3     | +1    |
| Skill Draft Count      | 2      | 4     | +2    |
| Trajectory Count       | 5      | 12    | +7    |
```

---

## 4. eval list

```bash
ironclaw eval list [flags]
```

### 标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `--suite` | `all` | 套件名或 `all`（列出所有套件的任务） |

### 输出示例

```
Suite: builtin (8 tasks)
  [simple]   bash-echo        [bash]
  [moderate] bash-multi-step  [bash]
  [moderate] file-write-read  [file_write, file_read]
  ...
```

---

## 5. eval longitudinal

```bash
ironclaw eval longitudinal [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--suite` | — | `builtin` | 基础套件 |
| `--live` | — | `false` | 真实 LLM 模式 |
| `--judge` | — | `true` | 启用 LLM Judge |
| `--output-dir` | — | `./eval_output/longitudinal` | 输出目录 |
| `--iterations` | `-n` | `5` | 迭代轮次数 |
| `--with-workload` | — | `false` | 轮次间注入工作负载任务（增加轨迹量） |
| `--force-insights` | — | `true` | 每轮结束后强制触发进化洞见生成 |
| `--config` | `-c` | — | 配置文件路径 |

### 输出文件

```
{output-dir}/
├── iteration_0.json    ← 第 0 轮 SuiteResult
├── iteration_1.json
├── ...
├── longitudinal.json   ← []IterationPoint（用于可视化）
└── learning_curve.html ← 学习曲线 HTML（自动生成）
```

### 控制台摘要

```
Longitudinal Eval: 5 iterations, suite=builtin
─────────────────────────────────────────
Iter 0:  SuccessRate=70.0%  AvgReward=0.61  Preferences=8   Skills=2
Iter 1:  SuccessRate=75.0%  AvgReward=0.68  Preferences=12  Skills=3  [insights ran]
Iter 2:  SuccessRate=80.0%  AvgReward=0.73  Preferences=15  Skills=4  [insights ran]
...
─────────────────────────────────────────
Learning Velocity:  Improving ↑
Strategy Converged: Yes (oscillation=0.015)
Composite Score:    0.78 / 1.00
```

---

## 6. eval visualize

```bash
ironclaw eval visualize -i longitudinal.json -o learning_curve.html
```

### 标志

| 标志 | 简写 | 说明 |
|------|------|------|
| `--input` | `-i` | 纵向 JSON 文件路径（`[]IterationPoint`） |
| `--output` | `-o` | 输出 HTML 路径 |

生成包含 6 个 Chart.js 图表的独立 HTML 文件（无外部依赖，CDN 引入 Chart.js）。

---

## 7. eval diagnose

```bash
ironclaw eval diagnose [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--suite` | — | `builtin` | 套件名 |
| `--live` | — | `false` | 真实 LLM 模式 |
| `--judge` | — | `true` | LLM Judge（用于 LLM 分类器） |
| `--output` | `-o` | `./eval_output` | 输出目录 |
| `--run-id` | — | 自动生成 | 运行 ID |
| `--config` | `-c` | — | 配置文件路径 |

### 输出文件

```
{output}/
├── suite_results.json      ← SuiteResult JSON
├── weakness_report.md      ← Markdown 弱点报告
└── radar.html              ← 维度雷达图 HTML
```

---

## 8. eval adaptive

```bash
ironclaw eval adaptive [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--suite` | — | `builtin` | 初始套件 |
| `--rounds` | `-n` | `3` | 自适应轮次 |
| `--tasks-per-round` | — | `5` | 每轮生成的新任务数 |
| `--live` | — | `false` | 真实 LLM 模式 |
| `--output` | `-o` | — | 输出 JSON 路径 |
| `--config` | `-c` | — | 配置文件路径 |

### 输出示例

```
Adaptive Eval: 3 rounds, 5 tasks/round

Round 0: 14/20 passed (70.0%)
  Weaknesses: W-001 [critical] planning_error, W-002 [major] tool_misuse
  Generating 5 adaptive tasks...

Round 1 (adaptive): 4/5 passed (80.0%)
  Improved: W-001 [planning_error]

Round 2 (adaptive): 4/5 passed (80.0%)
  Improved: W-002 [tool_misuse]

Summary:
  Initial Success Rate: 70.0%
  Final Success Rate:   80.0%
  Improved Weaknesses:  [W-001, W-002]
  Remaining:            [W-003]
```

---

## 9. eval benchmark

```bash
ironclaw eval benchmark [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--name` | — | — | 基准名：`swe`, `humaneval`, `gaia` |
| `--data` | — | — | 数据集文件路径 |
| `--live` | — | `false` | 真实 LLM 模式 |
| `--judge` | — | `true` | LLM Judge |
| `--output` | `-o` | — | 输出 JSON 路径 |
| `--config` | `-c` | — | 配置文件路径 |

### 支持的基准

| 基准 | 适配器文件 | 参考分数 |
|------|------------|---------|
| SWE-bench | `bench_swe.go` | 工业级软件工程任务 |
| HumanEval | `bench_humaneval.go` | 代码生成任务（164 题） |
| GAIA | `bench_gaia.go` | 通用 AI 助手评测 |

`BenchmarkAdapter` 接口：

```go
type BenchmarkAdapter interface {
    Name() string
    LoadTasks(dataPath string) ([]TaskCase, error)
    FormatResult(suite *SuiteResult) string
    ReferenceScore() float64
}
```

---

## 10. eval self-learning

```bash
ironclaw eval self-learning [flags]
```

### 标志

| 标志 | 简写 | 默认值 | 说明 |
|------|------|--------|------|
| `--live` | — | `false` | 真实 LLM 模式 |
| `--judge` | — | `true` | LLM Judge |
| `--output` | `-o` | — | 输出 JSON 路径 |
| `--longitudinal-in` | — | — | 历史纵向数据输入（用于生成学习曲线分析） |
| `--config` | `-c` | — | 配置文件路径 |

使用 `SelfLearningSuite()` 套件，涵盖技能迁移（`DimSkillLearning`）、偏好遵循（`DimPreferenceAdherence`）、记忆留存（`DimMemoryRetention`）三个自学习维度。

---

## 11. 内置 Fixture 套件

### 11.1 套件注册表（AllSuites()）

| 套件名 | 函数 | 任务数 | 特点 |
|--------|------|--------|------|
| `builtin` | `BuiltinSuite()` | 8 | 工具调用基础能力（bash/file） |
| `evolution` | `EvolutionSuite()` | ~10 | 压测重规划和错误恢复能力 |
| `planning` | `PlanningDimensionSuite()` | ~8 | 多步骤任务规划与分解 |
| `tool` | `ToolDimensionSuite()` | ~8 | 工具选择准确性 |
| `memory` | `MemoryDimensionSuite()` | ~8 | 跨任务记忆注入与检索 |
| `knowledge` | `KnowledgeDimensionSuite()` | ~8 | 知识库检索 |
| `conversation` | `ConversationSuite()` | ~8 | 对话理解 |
| `error` | `ErrorSuite()` | ~8 | 错误处理与恢复 |
| `team` | `TeamSuite()` | ~8 | 多 Agent 团队协作 |
| `preference` | `PreferenceSuite()` | ~8 | 偏好学习与应用 |
| `self_learning` | `SelfLearningSuite()` | ~15 | 技能/偏好/记忆三维自学习 |
| `workload` | `WorkloadSuite()` | ~10 | 纵向评测工作负载（增加轨迹量） |
| `full` | `FullSuite()` | ~80+ | 全部套件合并 |

### 11.2 BuiltinSuite 任务列表（示例）

```
bash-echo           [simple]   Run 'echo hello world'
bash-multi-step     [moderate] 创建目录/写文件/读文件验证
file-write-read     [moderate] 写入后读取验证内容
bash-error-recovery [moderate] 访问不存在目录后降级恢复
bash-pipeline       [moderate] 生成文件、计行数、求和
multi-tool-compose  [complex]  写 JSON + bash 验证
bash-script-gen     [complex]  生成脚本 + 执行
file-edit-flow      [complex]  写入/读取/sed 修改/再验证
```

### 11.3 EvolutionSuite 设计理念

任务故意包含模糊性、错误条件、多步依赖，**倾向触发重规划**。跨进化周期运行时，如果 `StrategyOptimizer` 有效地改善了重规划策略，`ReplanCount` 应该下降，成功率应该上升。

---

## 12. 外部任务文件格式

### 12.1 YAML 格式（推荐）

```yaml
# eval/example_tasks.yaml
- id: echo_hello
  goal: "Echo the string 'hello world' using bash"
  complexity: simple
  dimension: tool_selection
  verify_method: deterministic
  expect_tools:
    - bash
  reference:
    must_contain:
      - "hello world"
  tags:
    - bash
    - simple

- id: write_and_verify
  goal: "Write 'test content' to /tmp/eval_test.txt and read it back"
  complexity: moderate
  dimension: task_execution
  verify_method: hybrid
  expect_tools:
    - file_write
    - file_read
  reference:
    file_checks:
      - path: /tmp/eval_test.txt
        must_exist: true
        contains: "test content"
  rubric:
    criteria:
      - name: correctness
        description: "File was correctly written and read"
        weight: 0.7
      - name: efficiency
        description: "Task completed with minimal steps"
        weight: 0.3
  user_feedback: 0.8
```

### 12.2 JSON 格式

```json
[
  {
    "id": "json-task-01",
    "goal": "List files in /tmp",
    "complexity": "simple",
    "dimension": "tool_selection",
    "verify_method": "deterministic",
    "expect_tools": ["bash"],
    "reference": {
      "must_contain": ["/tmp"]
    }
  }
]
```

### 12.3 加载方式

```bash
# YAML 文件（自动按扩展名识别）
ironclaw eval run --suite ./eval/my_tasks.yaml

# JSON 文件
ironclaw eval run --suite ./eval/my_tasks.json

# 命名套件
ironclaw eval run --suite builtin
ironclaw eval run --suite full
```

---

## 13. CI/CD 工作流

### 13.1 eval-regression.yml

```yaml
# .github/workflows/eval-regression.yml
on:
  pull_request:
    branches: [ main ]
  workflow_dispatch:
    inputs:
      suite:
        description: 'Eval suite to run'
        default: 'builtin'

jobs:
  eval-dry:
    runs-on: ubuntu-latest
    steps:
      # 1. 安装 SQLite CGO 依赖
      - run: sudo apt-get install -y gcc libsqlite3-dev

      # 2. 编译（CGO_ENABLED=1，FTS5 必须）
      - run: CGO_ENABLED=1 go build -tags fts5 -o ironclaw ./cmd/ironclaw/

      # 3. Dry eval（不需要 LLM Key）
      - run: |
          ./ironclaw eval run \
            --suite ${{ github.event.inputs.suite || 'builtin' }} \
            --config configs/ironclaw.example.yaml \
            -o eval_output/ci_results.json

      # 4. 回归检测（baseline 存在时）
      - run: |
          if [ -f eval_output/baseline.json ]; then
            ./ironclaw eval compare \
              --before eval_output/baseline.json \
              --after eval_output/ci_results.json \
              --fail-on-regression
          else
            echo "No baseline; skipping comparison"
          fi

      # 5. 上传 Artifact（保留 30 天）
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: eval-results-${{ github.run_id }}
          path: eval_output/ci_results.json
          retention-days: 30
```

### 13.2 工作流设计要点

| 设计决策 | 原因 |
|----------|------|
| 默认 dry run | 无需 LLM Key，所有 PR 都可运行 |
| CGO_ENABLED=1 + fts5 | SQLite FTS5 为 build 必要条件 |
| `configs/ironclaw.example.yaml` | 示例配置不含真实 Key，dry 模式不需要 |
| baseline 可选 | 首次运行无基线时跳过比较，不阻塞 |
| `--fail-on-regression` | exit 1 触发 CI 失败，阻止回归合并 |
| Artifact 保留 30 天 | 方便手动检查历史评测数据 |

### 13.3 建立和更新基线

```bash
# 建立初始基线（本地生成后提交到仓库）
ironclaw eval run --suite builtin \
  -o eval_output/baseline.json

git add eval_output/baseline.json
git commit -m "chore: update eval baseline"

# 或在 CI 手动触发后下载 Artifact，更新 baseline.json
```

### 13.4 Live 模式 CI（可选扩展）

若需要在 CI 中运行 live 模式（需要 LLM Key），可添加如下 Step：

```yaml
- name: Run live eval (optional)
  if: ${{ secrets.ANTHROPIC_API_KEY != '' }}
  env:
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
  run: |
    ./ironclaw eval run \
      --suite evolution \
      --live \
      --judge \
      -c configs/ironclaw.yaml \
      -o eval_output/live_results.json
```
