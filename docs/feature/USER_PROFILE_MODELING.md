# 用户建模与个性化

**日期**: 2026-04-19
**范围**: 结构化用户画像、分区独立更新、System Prompt 注入适配

## 概述

将现有 Profiler 从"全量重写单文件"模式改造为"多 Section 独立文件、按需增量更新"模式。Profile 仍然作为 memory 文件存储（`type: profile`），复用 file-first 基础设施，但通过专用加载路径注入 system prompt，与普通记忆搜索解耦。

**设计约束**:
- 个人助理场景，单用户，不需要 multi-user 隔离
- 全维度画像，分优先级逐步学习
- 纯注入式适配 — Profile 注入 system prompt，靠 LLM 自然适配行为
- 分区独立更新 — 每个 section 有独立的触发条件和更新周期

## 1. Profile Schema

### Section 定义

| 优先级 | Section ID | 名称 | 内容 | 学习来源 |
|--------|-----------|------|------|---------|
| P0 | `communication` | 沟通偏好 | 语言偏好、详略程度、格式偏好、解释风格 | 用户纠正、显式指令、反馈模式 |
| P0 | `tech_stack` | 技术栈画像 | 主力语言、框架、工具链、编辑器、OS | 项目交互、工具调用模式、显式提及 |
| P1 | `work_pattern` | 工作模式 | 时区、活跃时段、任务偏好、执行偏好 | 交互时间分布、任务流模式 |
| P1 | `projects` | 项目上下文 | 当前活跃项目列表，每个项目的技术栈和状态 | 会话内容、项目切换模式 |
| P2 | `feedback` | 反馈模式 | 对 agent 输出的满意/不满意模式，常见纠正类型 | 显式反馈、重试/纠正行为 |
| P2 | `identity` | 身份画像 | 角色、专业领域、关注方向、长期目标 | 自我介绍、长期交互积累 |

### 文件格式

每个 section 是一个独立的 memory 文件，存储在 `user/` 目录下：

```yaml
---
id: profile_communication
type: profile
scope: user
section: communication
priority: 0
created_at: 2026-04-19T10:00:00Z
updated_at: 2026-04-19T15:30:00Z
strength: 1.0
confidence: 0.8
evidence_count: 12
---

## 沟通偏好

- **语言**: 中文为主，技术术语可用英文
- **详略**: 偏好简洁直接，不需要过多铺垫
- **格式**: 喜欢结构化输出（列表、表格），代码块用 markdown
- **解释风格**: 关键决策需要说明理由，常规操作不需要
```

### 关键字段说明

- **`type: profile`**: 标识为画像文件，用于搜索过滤和专用加载
- **`section`**: section ID，对应 schema 中的定义
- **`priority`**: 数值越小优先级越高，影响注入顺序和触发频率
- **`confidence`**: 0-1 之间，由 evidence_count 计算: `min(1.0, evidence_count * 0.1)`。低 confidence（< 0.5）注入时标注"初步观察"
- **`evidence_count`**: 累计支撑该 section 的事实/反思数量
- **`strength: 1.0`**: 固定值，profile 文件不参与遗忘曲线衰减

## 2. 事实路由与分区更新

### 事实 → Section 路由

两层路由机制：

**第一层：Category 直接映射**

| Fact Category | 默认路由 Section |
|--------------|-----------------|
| `preference` | `communication` |
| `identity` | `identity` |
| `relationship` | `identity` |
| `task` | `projects` |
| `fact` | 进入第二层 |

**第二层：LLM 轻量分类**

对无法直接映射的事实，用 LLM 判断归属维度。返回 section ID 或 `none`。返回 `none` 的事实不进入任何 section buffer，但仍作为普通记忆由 LifecycleManager 正常处理。

### 缓冲与触发

每个 section 维护独立的 `pending_facts` 缓冲区，按优先级设定不同触发阈值：

| 优先级 | 事实数阈值 | 时间阈值 |
|--------|-----------|---------|
| P0 | >= 3 条 | 距上次更新 > 1 小时 |
| P1 | >= 5 条 | 距上次更新 > 4 小时 |
| P2 | >= 8 条 | 距上次更新 > 24 小时 |

满足任一条件即触发该 section 的更新。

### Section 更新流程

```
输入:
  - current_section_content: 当前 section 的 markdown 正文
  - pending_facts: 待整合的新事实列表
  - confidence: 当前置信度

处理:
  1. LLM 接收当前画像 + 新观察，执行增量更新
  2. 保留仍然成立的信息，整合新观察
  3. 矛盾信息以更近期的观察为准

输出:
  - 更新后的 section markdown 正文
  - 更新 frontmatter: updated_at, evidence_count += len(pending_facts)
  - confidence 重算
  - 旧版本归档到 archived/
```

## 3. System Prompt 注入

### 防重复注入

Profile 文件存在 memory 体系中，为防止 `LoadProfileSections` 和普通 memory search 重复注入同一内容：

- `SearchQuery` 新增 `ExcludeTypes []string` 字段
- 默认搜索自动排除 `type: profile`，profile 只通过专用通道注入

### 两个注入点改造

| 模式 | 当前行为 | 改造后 |
|------|---------|--------|
| Simple mode (`runtime.go`) | `LoadUserProfile(baseDir, "default")` | `LoadProfileSections(baseDir)` |
| Cognitive mode (`perceive.go`) | 依赖 memory search 排名 | 显式加载，写入 `CognitiveState.UserProfile` |

### 拼接规则

1. **按优先级排序**: P0 在前，P2 在后（LLM 注意力对前面内容更敏感）
2. **Confidence 标注**: confidence < 0.5 的 section 标注"初步观察"
3. **空 section 跳过**: 未学习到的 section 不注入，不占 token
4. **Token 预算**: 默认上限 800 tokens，超限时按优先级从低到高截断

### Cognitive 模式扩展

`CognitiveState` 新增 `UserProfile string` 字段。PERCEIVE 阶段加载 profile sections，PLAN 阶段通过 `{{USER_PROFILE}}` 模板变量替换注入。

## 4. 冷启动策略

### 主动探查模式

当 profile 为空或极度不完整时，在 system prompt 中注入隐式指令，引导 agent 从自然对话中提取画像信息，而非直接向用户提问。

**触发条件**: 无任何 `type: profile` 文件，或连续 3 个会话未产生新 section。

### 各 Section 冷启动速度

| Section | 速度 | 原因 |
|---------|------|------|
| `communication` | 极快（1-2 轮） | 第一条消息就暴露语言和风格 |
| `tech_stack` | 快（3-5 轮） | 讨论项目时自然暴露技术栈 |
| `projects` | 中等（5-10 轮） | 需要几次不同话题建立全貌 |
| `work_pattern` | 慢（跨多会话） | 需要长期观察时间模式 |
| `feedback` | 慢（跨多会话） | 需要积累足够反馈样本 |
| `identity` | 最慢（自然积累） | 很少主动提及，需长期推断 |

P0 section 的低触发阈值（3 条事实）确保 `communication` 和 `tech_stack` 在前几轮对话中快速建立。

## 5. 与现有系统的关系

### 改造范围

| 文件 | 变更类型 | 内容 |
|------|---------|------|
| `internal/memory/profile_schema.go` | **新增** | Section 定义、优先级、Category 映射、触发阈值 |
| `internal/memory/profiler.go` | **重构** | 拆分为 RouteFactToSection + SectionBuffer + UpdateSection + LoadProfileSections |
| `internal/memory/store.go` | **修改** | SearchQuery 新增 ExcludeTypes 字段 |
| `internal/memory/file_store.go` | **修改** | Search 实现支持 ExcludeTypes 过滤 |
| `internal/agent/runtime.go` | **修改** | buildSystemPrompt 改用 LoadProfileSections |
| `internal/agent/perceive.go` | **修改** | PERCEIVE 阶段加载 profile sections |
| `internal/agent/cognitive_types.go` | **修改** | CognitiveState 新增 UserProfile 字段 |
| `internal/agent/plan.go` | **修改** | 支持 {{USER_PROFILE}} 模板变量 |

### 不动的部分

- `evolution.preference` — 关注工具选择优化，与 profile 系统定位不同，各司其职
- `ReflectionTracker` — 继续作为事实/反思的积累引擎，通过 ProfilerCallback 向 Profiler 传递数据
- `LifecycleManager` — 继续负责普通记忆的 CRUD 决策
- Memory 文件格式 — 完全兼容现有 MemoryFile 规范，只是新增 section/confidence/evidence_count 字段

## 6. 实施阶段

### Phase 1: Schema + 存储（基础）
- 定义 ProfileSection schema 和 section 注册表
- 实现 profile 文件的读写（复用 FileMemoryStore）
- SearchQuery 支持 ExcludeTypes

### Phase 2: 路由 + 更新（核心）
- 实现事实 → section 路由（两层）
- SectionBuffer 缓冲和触发机制
- 单 section 增量更新逻辑

### Phase 3: 注入（闭环）
- LoadProfileSections 拼接和 token 预算
- Simple mode 和 Cognitive mode 注入改造
- CognitiveState.UserProfile + {{USER_PROFILE}} 模板

### Phase 4: 冷启动 + 打磨
- 主动探查模式
- 现有单文件 profile 迁移到多文件
- 端到端测试
