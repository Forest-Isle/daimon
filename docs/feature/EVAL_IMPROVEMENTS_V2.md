# Eval 框架增强 V2

**日期**: 2026-04-21
**范围**: YAML 任务文件加载 + EvolutionSnapshot Diff + `--fail-on-regression` 标志 + CI 回归检测工作流

## 概述

IronClaw 的评估框架（`internal/eval/`）在第一版中提供了基础的任务执行和结果对比能力，但存在三个实用性缺口：

1. 任务文件只支持 JSON 格式，可读性差，手工编写和维护成本高
2. `ComparisonReport` 缺少对 Evolution 系统本身变化的捕获——无法判断两次评估之间偏好学习、策略版本、技能草稿、轨迹是否发生了演变
3. 没有 CI 自动化——能力回归只能靠人工运行对比，容易被遗漏

本次增强在不破坏已有 JSON 格式的前提下，全面补齐以上三项能力。

## 改进详解

### 改进 1：YAML 任务文件加载（`internal/eval/taskset.go`）

#### 新增函数

```go
// LoadTaskSetYAML loads a []TaskCase from a YAML file.
// Supports the same fields as the JSON format via yaml struct tags.
func LoadTaskSetYAML(path string) ([]TaskCase, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("eval: read task file %q: %w", path, err)
    }
    var tasks []TaskCase
    if err := yaml.Unmarshal(data, &tasks); err != nil {
        return nil, fmt.Errorf("eval: parse YAML task file %q: %w", path, err)
    }
    return tasks, nil
}

// LoadTaskSetJSON loads a []TaskCase from a JSON file.
func LoadTaskSetJSON(path string) ([]TaskCase, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("eval: read task file %q: %w", path, err)
    }
    var tasks []TaskCase
    if err := json.Unmarshal(data, &tasks); err != nil {
        return nil, fmt.Errorf("eval: parse JSON task file %q: %w", path, err)
    }
    return tasks, nil
}
```

`gopkg.in/yaml.v3` 已在 `go.mod` 中，无需新增依赖。`TaskCase` 结构体通过 `yaml:` 标签支持 snake_case 字段名，与 JSON 格式完全对等。

#### YAML 任务文件格式

```yaml
# eval/example_tasks.yaml
- id: echo_hello
  goal: "Echo the string 'hello world' using bash"
  complexity: simple
  dimension: tool_selection
  expect_tools:
    - bash

- id: count_lines
  goal: "Count the number of lines in the file /etc/hosts"
  complexity: simple
  dimension: tool_selection
  expect_tools:
    - bash

- id: write_and_read
  goal: "Write 'test content' to /tmp/ironclaw_test.txt and then read it back"
  complexity: medium
  dimension: task_execution
  expect_tools:
    - file_write
    - file_read
```

运行命令：

```bash
ironclaw eval run --suite ./eval/example_tasks.yaml
```

#### CLI 自动扩展检测（`cmd/ironclaw/eval.go`）

`loadSuite()` 函数负责将 `--suite` 参数解析为任务列表，优先尝试命名 suite，再根据文件扩展名选择加载器：

```go
// loadSuite resolves a suite name to task cases. Checks named suites first,
// then falls back to reading a file (YAML for .yaml/.yml, JSON otherwise).
func loadSuite(name string) ([]eval.TaskCase, error) {
    suites := eval.AllSuites()
    if fn, ok := suites[name]; ok {
        return fn(), nil
    }

    switch strings.ToLower(filepath.Ext(name)) {
    case ".yaml", ".yml":
        return eval.LoadTaskSetYAML(name)
    case ".json":
        return eval.LoadTaskSetJSON(name)
    default:
        available := make([]string, 0, len(suites))
        for k := range suites {
            available = append(available, k)
        }
        sort.Strings(available)
        return nil, fmt.Errorf("unknown suite %q; available: %v", name, available)
    }
}
```

无扩展名且不匹配命名 suite 时，给出可用 suite 列表的友好错误提示，而不是晦涩的文件不存在错误。

---

### 改进 2：EvolutionSnapshot Diff（`internal/eval/compare.go`）

#### 新结构体

```go
// EvoSnapshotDiff holds the per-field deltas between two EvolutionSnapshots.
type EvoSnapshotDiff struct {
    PreferenceCountDelta int `json:"preference_count_delta"`
    StrategyVersionDelta int `json:"strategy_version_delta"`
    SkillDraftCountDelta int `json:"skill_draft_count_delta"`
    TrajectoryCountDelta int `json:"trajectory_count_delta"`
}
```

| 字段 | 含义 |
|------|------|
| `PreferenceCountDelta` | 偏好学习记录数变化（正值表示新增了偏好） |
| `StrategyVersionDelta` | 策略优化器版本号变化（正值表示策略发生了更新） |
| `SkillDraftCountDelta` | 技能草稿数变化（正值表示生成了新草稿） |
| `TrajectoryCountDelta` | 轨迹记录数变化 |

#### 集成到 ComparisonReport

```go
type ComparisonReport struct {
    // ...已有字段...
    EvoSnapshot *EvoSnapshotDiff `json:"evo_snapshot,omitempty"`
}
```

在 `Compare()` 函数中，当两次运行的 `EvoAfter` 均不为 nil 时计算 diff：

```go
if before.EvoAfter != nil && after.EvoAfter != nil {
    report.EvoSnapshot = &EvoSnapshotDiff{
        PreferenceCountDelta: after.EvoAfter.PreferenceCount - before.EvoAfter.PreferenceCount,
        StrategyVersionDelta: after.EvoAfter.StrategyVersion - before.EvoAfter.StrategyVersion,
        SkillDraftCountDelta: after.EvoAfter.SkillDraftCount - before.EvoAfter.SkillDraftCount,
        TrajectoryCountDelta: after.EvoAfter.TrajectoryCount - before.EvoAfter.TrajectoryCount,
    }
}
```

#### Markdown 渲染

`FormatMarkdown()` 在报告末尾追加 Evolution Snapshot Delta 表格：

```go
if r.EvoSnapshot != nil {
    b.WriteString("\n### Evolution Snapshot Delta\n\n")
    b.WriteString("| Field | Delta |\n")
    b.WriteString("|-------|-------|\n")
    fmt.Fprintf(&b, "| Preference Count | %+d |\n", r.EvoSnapshot.PreferenceCountDelta)
    fmt.Fprintf(&b, "| Strategy Version | %+d |\n", r.EvoSnapshot.StrategyVersionDelta)
    fmt.Fprintf(&b, "| Skill Draft Count | %+d |\n", r.EvoSnapshot.SkillDraftCountDelta)
    fmt.Fprintf(&b, "| Trajectory Count | %+d |\n", r.EvoSnapshot.TrajectoryCountDelta)
}
```

输出示例：

```markdown
### Evolution Snapshot Delta

| Field | Delta |
|-------|-------|
| Preference Count | +12 |
| Strategy Version | +1 |
| Skill Draft Count | +3 |
| Trajectory Count | +47 |
```

---

### 改进 3：`--fail-on-regression` 标志（`cmd/ironclaw/eval.go`）

`eval compare` 子命令新增 `--fail-on-regression` 标志，检测到回归时以退出码 1 终止，便于 CI 流水线捕获：

```go
cmd.Flags().BoolVar(&failOnRegression, "fail-on-regression", false,
    "exit with code 1 if any regressions are detected")
```

处理逻辑：

```go
if failOnRegression && len(report.Regressions) > 0 {
    fmt.Fprintf(os.Stderr, "❌ %d regression(s) detected.\n", len(report.Regressions))
    os.Exit(1)
}
```

错误输出到 `stderr`，标准报告（Markdown 或 JSON）仍输出到 `stdout`，两者互不干扰，便于管道处理和日志分离。

完整命令示例：

```bash
ironclaw eval compare \
  --before eval_output/baseline.json \
  --after eval_output/current.json \
  --fail-on-regression
```

---

### 改进 4：CI 回归检测工作流（`.github/workflows/eval-regression.yml`）

#### 触发条件

```yaml
on:
  pull_request:
    branches: [ main ]
  workflow_dispatch:
    inputs:
      suite:
        description: 'Eval suite to run (default: builtin)'
        default: 'builtin'
        required: false
```

每次 PR 向 `main` 合并时自动触发；支持手动 `workflow_dispatch` 并可指定 suite 名称。

#### 执行步骤

```yaml
jobs:
  eval-dry:
    name: Dry Eval
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Install deps
        run: sudo apt-get install -y gcc libsqlite3-dev
      - name: Build
        run: CGO_ENABLED=1 go build -tags fts5 -o ironclaw ./cmd/ironclaw/
      - name: Run dry eval
        run: |
          ./ironclaw eval run --suite ${{ github.event.inputs.suite || 'builtin' }} \
            --config configs/ironclaw.example.yaml \
            -o eval_output/ci_results.json
      - name: Check for regressions against baseline
        run: |
          if [ -f eval_output/baseline.json ]; then
            ./ironclaw eval compare \
              --before eval_output/baseline.json \
              --after eval_output/ci_results.json \
              --fail-on-regression
          else
            echo "No baseline found; skipping comparison (first run)"
          fi
      - name: Upload eval results
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: eval-results-${{ github.run_id }}
          path: eval_output/ci_results.json
          retention-days: 30
```

**关键设计决策**：

- **Dry Runner**：`builtin` suite 使用 `DryRunner`，不需要 API Key，任何 PR 均可运行
- **首次运行容错**：`baseline.json` 不存在时跳过对比，打印提示而非报错——首次建立基线时不阻塞 CI
- **产物保留**：每次运行的结果上传为 artifact（30 天保留期），可用于 Debug 历史回归
- **`if: always()`**：即使 eval 步骤失败，产物上传仍然执行，确保失败现场可查

## 使用工作流

### 建立基线

```bash
# 1. 在本地或 CI 运行 eval 并保存为基线
./ironclaw eval run --suite builtin -o eval_output/baseline.json

# 2. 提交基线到仓库
git add eval_output/baseline.json
git commit -m "ci: establish eval baseline"
git push
```

### PR 自动对比

后续每个 PR 的 CI：
1. 使用当前代码重新运行 `builtin` suite（DryRunner，无 API 调用）
2. 将结果与 `baseline.json` 对比
3. 若有回归，以退出码 1 失败，PR 被阻塞

### 手动触发特定 suite

```bash
gh workflow run eval-regression.yml -f suite=./eval/example_tasks.yaml
```

### 查看对比报告（本地）

```bash
./ironclaw eval compare \
  --before eval_output/baseline.json \
  --after eval_output/results.json
```

输出示例（Markdown 格式）：

```
# Evaluation Comparison Report

**Before**: run-abc123 | **After**: run-def456

| Metric | Before | After | Delta |
|--------|--------|-------|-------|
| Success Rate | 80.0% | 85.0% | +5.0% |
| Assertion Pass Rate | 72.0% | 78.0% | +6.0% |
| Avg Confidence | 0.75 | 0.81 | +0.06 |
| Avg Replan Count | 1.2 | 0.9 | +0.3 |
| Total Duration | 12.3s | 11.8s | -0.5s |

**Overall**: Improvement detected after evolution cycle.

### Evolution Snapshot Delta

| Field | Delta |
|-------|-------|
| Preference Count | +12 |
| Strategy Version | +1 |
| Skill Draft Count | +3 |
| Trajectory Count | +47 |
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/eval/taskset.go` | 新增 | `LoadTaskSetYAML` + `LoadTaskSetJSON` 双格式加载器 |
| `internal/eval/compare.go` | 修改 | 新增 `EvoSnapshotDiff` 结构体；`ComparisonReport` 新增 `EvoSnapshot` 字段；`Compare()` 计算 diff；`FormatMarkdown()` 渲染 Evolution Snapshot Delta 表格 |
| `cmd/ironclaw/eval.go` | 修改 | `newEvalCompareCmd` 新增 `--fail-on-regression` 标志；`loadSuite()` 新增扩展名自动检测 |
| `.github/workflows/eval-regression.yml` | 新增 | PR 触发的 dry eval + 基线对比 CI 工作流 |
| `eval/example_tasks.yaml` | 新增 | YAML 格式任务文件示例（3 个任务：echo、count_lines、write_and_read） |

## 验证

### YAML 加载验证

```bash
# 直接运行 YAML suite
./ironclaw eval run --suite ./eval/example_tasks.yaml

# 列出任务确认解析正确
./ironclaw eval list --suite ./eval/example_tasks.yaml
```

### `--fail-on-regression` 验证

```bash
# 构造一个分数降低的 after 结果，确认退出码为 1
./ironclaw eval compare \
  --before eval_output/baseline.json \
  --after eval_output/regressed.json \
  --fail-on-regression
echo $?  # 应输出 1
```

### EvoSnapshotDiff 验证

当两次运行均包含 `evo_after` 字段时，JSON 输出中应有 `evo_snapshot` 对象：

```bash
./ironclaw eval compare \
  --before eval_output/baseline.json \
  --after eval_output/results.json \
  --json | jq .evo_snapshot
```

### CI 工作流验证

1. 在仓库中提交 `eval_output/baseline.json`
2. 发起 PR，观察 `Eval Regression` 检查是否出现
3. 验证 `eval-results-{run_id}` artifact 在 Actions 页面可下载
