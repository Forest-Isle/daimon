# 17 · skill + workflow — 技能与反射底座

> 包路径 `internal/skill`、`internal/workflow` · 蓝图 §4.2「反射弧」、§4.8「蒸馏」

技能是蒸馏环的输出端：情节中反复成功的模式被沉淀成可复用 SKILL.md。workflow 是确定性 DAG 执行器——本应是"反射弧"的免费执行底座，但反射自治链路存在诚实墙（见末节）。

## skill — 技能库

### 核心类型

```go
// internal/skill/skill.go
type Skill struct {
    Name, Description, Version, Author string
    Tags     []string
    Metadata SkillMeta
    Path     string   // SKILL.md 绝对路径
    content  string    // 惰性加载的 markdown 正文
    contentOnce sync.Once
}
type SkillMeta struct {
    OpenClaw        OpenClawMeta
    Distilled       bool      // 蒸馏草稿标记
    SourceCandidate string
    SourceEpisodes  []string  // 来源情节
}
```

`ParseSkill` 只急加载 YAML frontmatter，正文经 `Content()` 惰性读（`contentOnce`）。

### 渐进披露（progressive disclosure）

`Manager`（`manager.go`）的核心设计——**技能正文绝不注入 system prompt**：

- `LoadBuiltin`：embed.FS 内置技能（`//go:embed builtin/*/SKILL.md`）。
- `LoadDir`：扫描目录，急解析 frontmatter，同名跳过（first-loaded wins）。
- `BuildPromptSection`：只把**选中技能的元数据**（name/version/description/tags）写进 prompt，附"用 `read_skill` 工具加载全文"的指引。
- `GetContent(name)`：`read_skill` 工具调用时才惰性读正文。

**这是 replay-canary 无法行为级验证草稿的根因**（[11-replay.md](11-replay.md)）：技能经工具懒加载、非确定性注入，离线重放看不到草稿是否被真正使用。

### 三层目录与转正

`~/.daimon` 下技能分三处（[19-data-layer.md](19-data-layer.md)）：

| 目录 | 状态 | 是否被加载 |
|---|---|---|
| `skills/`（active）| 活跃 | 是，`InitSkills` LoadDir |
| `skills-staging/`（staging）| 草稿 | **否**，绝不加载/执行 |
| builtin（embed）| 内置 | 是，LoadBuiltin |

蒸馏产出的草稿先落 staging（不被加载），经 operator 签名才转正（`promote.go`）：

```go
func PromoteDraft(stagingDir, activeDir, slug) (target string, err error)
func DemoteSkill(activeDir, stagingDir, slug) error
func ListDrafts(stagingDir) ([]DraftInfo, error)
func ValidateDraft(skillMdPath) (*Skill, error)  // 非空 name + distilled marker + 非空正文
```

**§706 安全守卫**（三道）：
1. `ValidSlug`：拒空 / `.` / `..` / 含路径分隔符——草稿 slug 必须是单层目录名。
2. `ensureNotSymlink`：`Lstat` 拒 symlink 源——转正绝不跟链逃出 staging/active root。
3. `ensureWithinRoot`：`EvalSymlinks` 后校验仍在 root 内。

转正流程：校验草稿 → 加载 active+builtin 查重名 → 目标目录不存在 → 三守卫 → `os.Rename`（确定性文件移动，**绝不走 RunInternalEpisode**——LLM 非确定 + §706）。`DemoteSkill` 拒非 distilled 技能（用 `daimon skill remove`），对称回滚。

### 蒸馏闭环（自治受阻）

```
情节成功模式 → distill job (LLM judge 找重复) → 候选
  → distill_screen job (LLM 判质量/安全/结构) → typed promote_skill 提案
  → operator 签名（Telegram [做/不做] / daimon skill promote）→ PromoteDraft 转正
```

提案式人签（宪法第 4 条永人签）。自治转正被 Canary 阻塞（诚实墙），见 [11-replay.md](11-replay.md) 与 [09-sleep.md](09-sleep.md)。

## workflow — 确定性 DAG 执行器

### 核心类型

```go
// internal/workflow/spec.go
type Spec struct {
    Version, Name, Description string
    FailureStrategy FailureStrategy  // stop | best_effort
    Budget Budget                    // MaxSteps / MaxTokens
    Stages []Stage
}
type Stage struct { ID string; Parallel bool; Steps []Step }
type Step struct {
    ID string; Type StepType  // agent | tool
    Agent, Tool, Task string
    Input map[string]any
    Cache *bool; Budget Budget
}
```

`NormalizeAndValidate`：版本默认 v1、stage/step id 去重补全、agent step 需 agent+task、tool step 需 tool。`Digest()` = canonical spec 的 sha256（replay cache 键）。

### 执行器

```go
// internal/workflow/executor.go
type Executor struct { Runner StepRunner; Cache ReplayCache; Observer Observer; MaxParallel int }
func (e *Executor) Execute(ctx, spec *Spec) (*Run, error)
```

- **stage 串行 / step 可并行**（`executeParallelStage` 用 semaphore 限 `MaxParallel`）。
- **replay cache**（`sqlite_cache.go`）：step 成功结果按 `cacheKey(spec,stage,step,prior)` 缓存，命中跳过执行（确定性可重放）。
- **预算追踪**（`budgetTracker`）：MaxSteps/MaxTokens 超限 → step 标 error。
- **失败策略**：`FailureStop`（默认）遇错即停；`FailureBest` 尽力跑完。
- **Observer**：每 step started/completed 事件可观测。

这是 multi-agent `workflow` 工具的编排底座（[16-channels-agent.md](16-channels-agent.md) 子代理）。

## 反射弧

蓝图设想：蒸馏出的高频模式 → 注册为 reflex 规则 → attention 路由 `Reflex` 动作 → 免 LLM 执行 workflow（"反射免费"）。当前实现已闭合手工配置链路：

- `agent.heart.reflexes` 是 workflow-by-id registry；`rules.yaml` 的 `action: reflex` 必须带 `reflex_id`。
- `heart_dispatch` 调 `reflexExecutor.Execute`，按 `reflex_id` 载入 YAML/JSON workflow。
- reflex workflow 只执行 deterministic `type: tool` step；`type: agent` step 被拒绝，避免自治反射偷偷花 LLM。
- 工具调用走现有权限链并标记为 `ToolChannelInternal`：只读工具可跑，写/网络/破坏性工具在无人审批场景下 fail-closed。

仍未自动化的部分：

- `synthesize.go` 明确不产 reflex 规则。
- distill→reflex 自治需 §706 行为 canary。

因此反射执行器已可用，但“从技能草稿自动晋升为自治 reflex 规则”仍保持人签/提案路径。

## 跨包接缝

- **← episode/sleep**：蒸馏 job 产候选 → staging 草稿。
- **→ gateway**：`InitSkills` LoadDir(active)+LoadBuiltin；`skill.PromoteDraft` 被 proposals coordinator typed accept 调用。
- **→ tool**：`read_skill` 工具调 `GetContent`；`workflow` 工具用 Executor。
- **→ vcs**：转正/降级是文件移动，`~/.daimon` git 化 = 天然可逆（[13-selfops.md](13-selfops.md)）。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 渐进披露（元数据进 prompt，正文惰性）| 成本/上下文 | 不撑爆 prompt，按需加载 |
| staging 绝不加载 | §706/宪法 4 | 草稿零执行风险，转正需人签 |
| 转正是确定性文件移动 | 宪法 4 | 绝不 LLM，可逆（demote）|
| workflow replay cache | 确定性 | 同 spec 可重放，step 幂等 |

下一篇：[18-supporting.md](18-supporting.md) — 支撑包。
