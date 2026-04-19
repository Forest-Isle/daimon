# 用户建模与个性化

**日期**: 2026-04-19
**范围**: 结构化用户画像、分区独立更新、System Prompt 注入适配

## 概述

本次改进将 Profiler 从"全量重写单文件"模式改造为"多 Section 独立文件、按需增量更新"模式，实现了对用户的深度理解和个性化适配。Profile 作为 memory 文件（`type: profile`）存储，复用 file-first 基础设施，通过专用加载路径注入 system prompt，与普通记忆搜索完全解耦。

改动涉及 21 个文件（+2,841 行），零新增外部依赖。新增 7 个源文件（含 4 个测试文件），修改 14 个已有文件。

## 核心架构

### 多 Section Profile 存储

每个用户画像维度作为独立的 Markdown 文件存储在 `~/.ironclaw/memory/user/` 目录下：

| 优先级 | Section ID | 名称 | 内容 | 学习来源 |
|--------|-----------|------|------|---------|
| P0 | `communication` | 沟通偏好 | 语言偏好、详略程度、格式偏好 | 用户纠正、显式指令 |
| P0 | `tech_stack` | 技术栈画像 | 主力语言、框架、工具链 | 项目交互、工具调用模式 |
| P1 | `work_pattern` | 工作模式 | 时区、活跃时段、任务偏好 | 交互时间分布 |
| P1 | `projects` | 项目上下文 | 当前活跃项目列表及状态 | 会话内容、项目切换 |
| P2 | `feedback` | 反馈模式 | 满意/不满意模式，常见纠正类型 | 显式反馈、重试行为 |
| P2 | `identity` | 身份画像 | 角色、专业领域、长期目标 | 自我介绍、长期交互 |

文件格式示例（`user/profile_communication.md`）：

```yaml
---
id: profile_communication
type: profile
scope: user
section: communication
priority: 0
confidence: 0.80
evidence_count: 12
strength: 1.0
created_at: 2026-04-19T10:00:00Z
updated_at: 2026-04-19T15:30:00Z
---

- **语言**: 中文为主，技术术语可用英文
- **详略**: 偏好简洁直接，不需要过多铺垫
- **格式**: 喜欢结构化输出（列表、表格），代码块用 markdown
```

关键字段：

- **`type: profile`** — 标识画像文件，用于搜索排除和专用加载
- **`confidence`** — 由 `min(1.0, evidence_count * 0.1)` 计算，低于 0.5 注入时标注"初步观察"
- **`strength: 1.0`** — 固定值，profile 不参与遗忘曲线衰减

### Section 注册表（ProfileSectionRegistry）

`ProfileSectionRegistry` 集中管理所有 section 的定义、优先级、触发阈值和事实路由映射。

```go
type ProfileSection struct {
    ID            string
    Name          string        // 中文显示名
    Priority      int           // 0 = 最高
    FactThreshold int           // 触发更新的最小事实数
    TimeThreshold time.Duration // 触发更新的最大间隔
}
```

支持的操作：`Get(id)`, `All()`, `ByPriority()`, `RouteCategory(category)`。

**涉及文件**: `internal/memory/profile_schema.go`, `internal/memory/profile_schema_test.go`

## 事实路由与分区更新

### 两层事实路由

从对话中提取的事实（`ExtractedFact`）通过两层机制路由到对应的 section 缓冲区：

```
ExtractedFact
│
├── 第一层: Category 直接映射
│   ├── preference   → communication
│   ├── identity     → identity
│   ├── relationship → identity
│   ├── task         → projects
│   └── 其他         → 进入第二层
│
└── 第二层: LLM 轻量分类
    ├── 返回 section ID → 路由到对应 buffer
    └── 返回 "none"     → 不进入任何 section（仍由 LifecycleManager 正常处理）
```

事实路由集成在 Runtime 的 fact extraction 流程中：每次 `lifecycleMgr.Process()` 处理完一个事实后，同步调用 `profiler.RouteFact()` 进行画像路由。

**涉及文件**: `internal/memory/profiler.go`（`RouteFact`, `classifyFactByLLM`）, `internal/agent/runtime.go`

### SectionBuffer 缓冲与触发

每个 section 维护独立的 `SectionBuffer`，按优先级设定不同的触发阈值：

| 优先级 | 事实数阈值 | 时间阈值 | 设计理由 |
|--------|-----------|---------|---------|
| P0 | >= 3 条 | > 1 小时 | 沟通/技术栈信息需快速建立 |
| P1 | >= 5 条 | > 4 小时 | 工作模式/项目需要更多样本 |
| P2 | >= 8 条 | > 24 小时 | 反馈/身份是长期积累 |

满足**任一**条件即触发该 section 的增量更新。`SectionBuffer` 线程安全（`sync.Mutex`），支持并发路由。

**涉及文件**: `internal/memory/section_buffer.go`, `internal/memory/section_buffer_test.go`

### 单 Section 增量更新

`UpdateSection` 执行单个 section 的 LLM 驱动增量更新：

```
UpdateSection(ctx, sectionID, userID)
│
├── Drain buffer → 获取 pending facts
├── 读取当前 section 文件（如存在）
├── 构建 LLM prompt:
│   ├── 当前画像内容（或"首次建立"）
│   └── 新观察列表
├── LLM 增量更新
│   └── 失败时: 重新入队所有 facts → 返回错误
├── 重算 metadata:
│   ├── evidence_count += len(facts)
│   ├── confidence = min(1.0, evidence_count * 0.1)
│   └── updated_at = now
├── 归档旧版本 → archived/
├── 原子写入新文件
└── 同步更新 SQLite 索引
```

矛盾信息处理策略：LLM prompt 中明确指示"矛盾信息以更近期的观察为准"。

**涉及文件**: `internal/memory/profiler.go`（`UpdateSection`, `CheckAndUpdateSections`）

## System Prompt 注入

### 防重复注入机制

Profile 文件存在 memory 体系中，为防止专用加载和普通记忆搜索重复注入同一内容：

- `SearchQuery` 新增 `ExcludeTypes []string` 字段
- `FileMemoryStore.Search` 中增加 `memory_type NOT IN (...)` SQL 过滤
- Runtime 和 Perceiver 的记忆搜索均设置 `ExcludeTypes: []string{"profile"}`

**涉及文件**: `internal/memory/store.go`, `internal/memory/file_store.go`, `internal/memory/exclude_types_test.go`

### Simple 模式注入

在 `Runtime.buildSystemPromptUncached` 中，profile 作为独立段落注入：

```
System Prompt 结构:
│
├── §1 Personality (Soul.md)
├── §2 Core System Prompt
├── §3 Persistent Rules
├── ── DYNAMIC_CONTEXT ──
├── §4 Relevant Memories (ExcludeTypes: ["profile"])
├── §5 User Profile ← LoadProfileSections(baseDir)
├── §5b Cold-start Prompt ← profiler.ColdStartPrompt()
├── §6 Skills
└── §7 Available Agents
```

**涉及文件**: `internal/agent/runtime.go`

### Cognitive 模式注入

认知 Agent 通过 `CognitiveState.UserProfile` 字段传递，在 PLAN 阶段通过模板变量替换：

```
PERCEIVE:
  memStore.Search(..., ExcludeTypes: ["profile"])  ← 普通记忆
  LoadProfileSections(memBaseDir)                   ← 专用加载
  → state.UserProfile = profileContent

PLAN:
  {{USER_PROFILE}} → state.UserProfile
```

**涉及文件**: `internal/agent/cognitive_types.go`, `internal/agent/perceive.go`, `internal/agent/cognitive_prompts.go`, `internal/agent/plan.go`

### LoadProfileSections 拼接规则

`LoadProfileSections` 扫描 `user/profile_*.md` 文件，按以下规则拼接：

1. **按优先级排序** — P0 在前（LLM 注意力对前面内容更敏感）
2. **Confidence 标注** — `< 0.5` 的 section 标注"(初步观察)"
3. **中文显示名** — 使用 `ProfileSectionRegistry` 的 `Name` 字段（如"沟通偏好"、"技术栈画像"）
4. **空 section 跳过** — 未学习到的 section 不注入
5. **Token 预算** — 上限约 800 tokens（3200 字符），超限时从低优先级截断

**涉及文件**: `internal/memory/profiler.go`（`LoadProfileSections`）

## 冷启动策略

当用户画像尚不完善时（高置信度 section < 3 个），`ColdStartPrompt` 在 system prompt 中注入隐式指令：

```
[Profile Building Mode]
你对当前用户的了解还很少。在自然对话中，注意观察并记录以下信息：
- 用户使用的语言和沟通风格
- 提到的技术栈和工具
- 工作方式和偏好
不要直接询问这些信息，而是从交互中自然提取。
```

**触发条件**: `confidence >= 0.5` 的 section 数量 < 3

**退出条件**: 3 个以上 section 的 confidence 达到 0.5（约需 5 条以上事实支撑）

各 section 的预期冷启动速度：

| Section | 速度 | 原因 |
|---------|------|------|
| `communication` | 极快（1-2 轮） | 第一条消息就暴露语言和风格 |
| `tech_stack` | 快（3-5 轮） | 讨论项目时自然暴露技术栈 |
| `projects` | 中等（5-10 轮） | 需要几次不同话题建立全貌 |
| `work_pattern` | 慢（跨多会话） | 需要长期观察时间模式 |
| `feedback` | 慢（跨多会话） | 需要积累足够反馈样本 |
| `identity` | 最慢（自然积累） | 很少主动提及，需长期推断 |

**涉及文件**: `internal/memory/profiler.go`（`ColdStartPrompt`）, `internal/agent/runtime.go`

## 旧版 Profile 迁移

启动时自动检测并迁移旧版单文件 Profile（`profile_default.md`）到新的多 Section 格式：

```
MigrateLegacyProfile(ctx, "default")
│
├── 读取 user/profile_default.md
├── 检查是否已有 section metadata → 跳过
├── 按旧版 header 分拆:
│   ├── "## Identity"      → profile_identity.md
│   ├── "## Preferences"   → profile_communication.md
│   └── "## Current Focus" → profile_projects.md
├── 每个新 section 设置:
│   ├── confidence: 0.30 (初步)
│   └── evidence_count: 3
└── 归档原文件 → archived/profile_default.md
```

**涉及文件**: `internal/memory/profiler.go`（`MigrateLegacyProfile`）, `internal/gateway/init_memory.go`

## 集成点

### Gateway 初始化

在 `initMemorySystem` 中（需 `FactExtraction` 启用）：

1. `NewProfiler(...)` 创建 Profiler（初始化 registry + 6 个 section buffer）
2. `reflector.SetProfilerCallback(profiler)` 将 L1 反思事件传递给 Profiler
3. `runtime.SetProfiler(profiler)` 将 Profiler 注入 Runtime
4. `profiler.MigrateLegacyProfile(ctx, "default")` 执行启动迁移
5. `gw.memoryDir = storageDir` 保存 memory 基础路径
6. `cognitiveAgent.SetMemBaseDir(gw.memoryDir)` 传递给认知 Agent

### 数据流全链路

```
用户消息 → 对话 → FactExtractor.Extract()
                    │
                    ├── LifecycleManager.Process(fact) ← 普通记忆 CRUD
                    │
                    └── Profiler.RouteFact(fact)
                        │
                        ├── 直接映射 / LLM 分类 → SectionBuffer.Add()
                        │
                        └── (反思时) CheckAndUpdateSections()
                            │
                            └── SectionBuffer.ShouldUpdate()? → UpdateSection()
                                │
                                ├── LLM 增量更新
                                ├── 归档旧版本
                                └── 写入 profile_*.md + SQLite 索引

System Prompt 构建时:
  LoadProfileSections(baseDir) → 读取 profile_*.md → 排序 → 拼接 → 注入
  ColdStartPrompt()           → 早期学习引导（如需要）
  Search(ExcludeTypes=profile) → 普通记忆（不含 profile）
```

### 与现有系统的边界

| 组件 | 关系 |
|------|------|
| `ReflectionTracker` | 上游 — 通过 `ProfilerCallback` 触发 `CheckAndUpdateSections` |
| `LifecycleManager` | 并行 — 同一事实分别处理：普通记忆 CRUD + 画像路由 |
| `evolution.preference` | 独立 — 关注工具选择优化，与 profile 定位不同 |
| `FileMemoryStore` | 下游 — profile 文件遵循 MemoryFile 格式规范 |
| `ForgettingCurve` | 不参与 — profile 的 `strength` 固定为 1.0 |

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/memory/profile_schema.go` | **新增** | `ProfileSection` + `ProfileSectionRegistry`（6 sections, 4 category mappings） |
| `internal/memory/profile_schema_test.go` | **新增** | Registry 创建、排序、路由映射测试（3 个） |
| `internal/memory/section_buffer.go` | **新增** | `SectionBuffer`（Add/ShouldUpdate/Drain/PendingCount） |
| `internal/memory/section_buffer_test.go` | **新增** | 计数阈值、时间阈值、排空测试（3 个） |
| `internal/memory/profiler.go` | **重构** | 新增 RouteFact, UpdateSection, LoadProfileSections, ColdStartPrompt, MigrateLegacyProfile；保留 legacy GenerateProfile |
| `internal/memory/profiler_test.go` | **新增** | 路由、更新、加载、冷启动、迁移测试（11 个） |
| `internal/memory/store.go` | **修改** | `SearchQuery` 新增 `ExcludeTypes` 字段 |
| `internal/memory/file_store.go` | **修改** | `Search` 增加 `NOT IN` 过滤 |
| `internal/memory/exclude_types_test.go` | **新增** | ExcludeTypes 字段 + 集成过滤测试（2 个） |
| `internal/agent/runtime.go` | **修改** | profiler 字段 + SetProfiler + RouteFact + LoadProfileSections + ColdStartPrompt 注入 |
| `internal/agent/cognitive_types.go` | **修改** | `CognitiveState.UserProfile` 字段 |
| `internal/agent/perceive.go` | **修改** | memBaseDir + LoadProfileSections + ExcludeTypes |
| `internal/agent/cognitive_prompts.go` | **修改** | `{{USER_PROFILE}}` 模板 |
| `internal/agent/plan.go` | **修改** | `{{USER_PROFILE}}` 替换 |
| `internal/agent/cognitive.go` | **修改** | `SetMemBaseDir` 方法 |
| `internal/gateway/gateway.go` | **修改** | `memoryDir` 字段 |
| `internal/gateway/init_memory.go` | **修改** | SetProfiler + MigrateLegacyProfile |
| `internal/gateway/init_cognitive.go` | **修改** | memoryDir → SetMemBaseDir |
| `CLAUDE.md` | **修改** | User Profile 子系统文档 |
| `docs/feature/USER_PROFILE_MODELING.md` | **新增** | 本文档 |
| `docs/feature/USER_PROFILE_MODELING_PLAN.md` | **新增** | 实现计划（10 Tasks, TDD） |

## 测试

19 个新增测试覆盖所有关键路径：

**Profile Schema（3 个）**:
- `TestProfileSectionRegistry` — 6 sections 创建、优先级、阈值
- `TestProfileSectionRegistry_ByPriority` — 排序正确性
- `TestRouteCategoryToSection` — 4 个映射 + 无映射返回 not-ok

**SectionBuffer（3 个）**:
- `TestSectionBuffer_AddAndShouldUpdate` — 计数阈值触发
- `TestSectionBuffer_TimeThreshold` — 时间阈值触发
- `TestSectionBuffer_Drain` — 排空后 buffer 重置

**Profiler（11 个）**:
- `TestProfiler_RouteFactToSection_DirectMapping` — preference → communication, task → projects
- `TestProfiler_RouteFactToSection_LLMFallback` — LLM 返回 tech_stack
- `TestProfiler_RouteFactToSection_LLMReturnsNone` — 无关事实不路由
- `TestProfiler_UpdateSection` — LLM 更新 + 元数据验证
- `TestProfiler_UpdateSection_RequeuesOnError` — 失败时事实重新入队
- `TestLoadProfileSections_Empty` — 空目录返回空
- `TestLoadProfileSections_SortsByPriority` — 排序 + confidence 标注
- `TestProfiler_ColdStartPrompt_EmptyDir` — 空目录触发冷启动
- `TestProfiler_ColdStartPrompt_PopulatedProfile` — 3 个高置信度 section 不触发
- `TestProfiler_MigrateLegacyProfile` — 完整迁移流程 + 归档验证
- `TestProfiler_MigrateLegacyProfile_SkipsNewFormat` — 新格式文件跳过

**ExcludeTypes（2 个）**:
- `TestSearchQuery_ExcludeTypes` — 字段存在性
- `TestExcludeTypes_FiltersProfileFromSearch` — FileMemoryStore.Search 实际过滤验证
