# IronClaw V4 — God-Tier Agent OS 演进路线图

> 从 Agent Runtime 到 Agent Operating System 的完整蓝图。  
> 不考虑兼容性，不考虑渐进式迁移，纯粹的最优架构。

---

## 一、现状定位

IronClaw v3 是一个 **Go 语言编写的本地优先自进化 Agent 运行时**，当前架构包含：

| 子系统 | 完成度 | 评价 |
|--------|--------|------|
| Cognitive 5-Phase Loop | 85% | 核心循环扎实，PERCEIVE→PLAN→ACT→OBSERVE→REFLECT + MCTS/tree planner |
| Memory (File + SQLite) | 70% | 文件优先 + FTS5 索引，但三套存储各自为战 |
| Knowledge Base + Graph | 65% | BM25/Vector 混合检索 + 实体关系图，但与记忆系统割裂 |
| Evolution Engine | 60% | 事件分发 + 偏好学习 + 阈值调优，但只是参数级进化 |
| RL Stack (Bandit/PPO/DQN) | 60% | 完整的 RL 训练循环 + 自定义神经网络，但仅用于策略微调 |
| Sub-Agent / Team | 65% | 子代理隔离 spawn + 任务协调器，但无自主性和竞争 |
| Feature Registry | 90% | 热重载、依赖解析、持久化，架构成熟 |
| Sandbox (Docker) | 75% | 会话级 Docker 容器 + 文件/网络守护，但依赖外部 Docker |
| Dashboard + WebSocket | 70% | 事件总线 + 状态追踪 + SPA，但前端能力有限 |
| MCP Integration | 80% | 完整的 MCP Client/Server，工具适配器 |
| Context Compression | 80% | 5 层渐进压缩 + 413 反应式恢复 |
| Eval Harness | 75% | 完整的 eval 框架 + 纵向分析 |

**核心问题：每层都是 70 分，没有一层做到 95 分。系统组合起来能力上限受限于最薄弱的环节。**

---

## 二、V4 九层架构

```
┌──────────────────────────────────────────────────────────────┐
│                   IRONCLAW V4 — Agent OS                     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  L8 — 🖥️  Agent Studio (Visual IDE + Multi-Tenant)           │
│       节点编辑器 | 提示词 IDE | 记忆浏览器 | 工具市场         │
│                                                              │
│  L9 — 🎯  Fine-Tuning Pipeline (闭环自举)                    │
│       数据集构建 | LoRA微调 | A/B测试 | 自动部署             │
│                                                              │
│  L7 — 🛡️  Guardian (在线质量守护)                            │
│       漂移检测 | LLM-as-Judge | 回归守卫 | 自动回滚          │
│                                                              │
│  L2 — 🧬  True Self-Evolution (真正自主进化)                 │
│       提示词优化 | 工具合成 | 遗传搜索 | 消融实验             │
│                                                              │
│  L3 — 🐝  Collective Intelligence (群体智能)                 │
│       代理市场 | 声誉系统 | 涌现角色 | 群体共识               │
│                                                              │
│  L5 — ⚡  Streaming Pipeline (流式认知管道)                   │
│       Channel-based 流水线 | 增量输出 | 零等待体感            │
│                                                              │
│  L0 — 🧠  Unified Cortex (统一记忆皮层) ← 最重要的基础       │
│       情节|语义|程序|工作记忆 | 睡眠巩固 | 统一检索           │
│                                                              │
│  L4 — 📟  LSP Code Engine (语言感知代码引擎)                  │
│       LSP客户端 | AST理解 | 调用图 | 安全重构                 │
│                                                              │
│  L6 — 🌐  Browser Agent (浏览器原生代理)                     │
│       CDP协议 | DOM理解 | 视觉分析 | Web自动化                │
│                                                              │
│  L1 — 🔧  WASM Plugin System (插件生态)                      │
│       工具即.wasm | 能力安全 | SDK | Marketplace             │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│  Foundation: Polyglot Sandbox (WASM + Docker + Firecracker)  │
│  Foundation: Streaming Event Bus (全链路流式架构)             │
│  Foundation: Multi-Tenant Isolation                          │
└──────────────────────────────────────────────────────────────┘
```

---

## 三、执行优先级与阶段划分

### Phase 1：基础重构（第 1-4 周）
**目标：打好地基，不碰上层**

| 优先级 | Layer | 模块 | 工作量 | 依赖 |
|--------|-------|------|--------|------|
| P0 | L0 | 统一记忆皮层 | 3-4 周 | 无 |
| P1 | L5 | 流式认知管道 | 2-3 周 | 无（与 L0 并行） |

**为什么 L0 第一：** 所有 Agent 的死穴都是记忆。记忆质量决定了规划质量、反思质量、进化质量。解决记忆，你解决了一半的问题。而且 L0 改动集中在 `internal/memory/` → `internal/cortex/`，不影响其他模块。

**为什么 L5 并行：** 流式改造主要改 `cognitive.go`，和记忆层正交。UX 提升立竿见影。

### Phase 2：生态与智能（第 5-10 周）
**目标：打开生态 + 让进化真正有效**

| 优先级 | Layer | 模块 | 工作量 | 依赖 |
|--------|-------|------|--------|------|
| P2 | L1 | WASM 插件系统 | 3-4 周 | 无 |
| P3 | L2 | 真正自主进化 | 3-4 周 | L0（需要统一记忆做数据源） |
| P4 | L3 | 代理市场 | 2-3 周 | L1（子代理需要 WASM 工具） |

### Phase 3：垂直能力（第 11-16 周）
**目标：让 Agent 能做的事更多**

| 优先级 | Layer | 模块 | 工作量 | 依赖 |
|--------|-------|------|--------|------|
| P5 | L4 | LSP 代码引擎 | 3-4 周 | 无 |
| P6 | L6 | 浏览器代理 | 2-3 周 | 无 |

### Phase 4：质量与体验（第 17-22 周）
**目标：生产级质量 + 极致体验**

| 优先级 | Layer | 模块 | 工作量 | 依赖 |
|--------|-------|------|--------|------|
| P7 | L7 | Guardian 守护系统 | 2-3 周 | L2, L0 |
| P8 | L8 | Agent Studio | 4-6 周 | L1 |
| P9 | L9 | 微调管路 | 2-3 周 | L7（需要质量评分做数据筛选） |

---

## 四、各层详细文档索引

| 文档 | 内容 |
|------|------|
| [01-L0-CORTEX.md](./01-L0-CORTEX.md) | 统一记忆皮层：情节/语义/程序/工作记忆 + 睡眠巩固 + 统一检索 |
| [02-L1-WASM.md](./02-L1-WASM.md) | WASM 插件系统：wazero 运行时 + 能力安全 + SDK + Marketplace |
| [03-L2-EVOLUTION.md](./03-L2-EVOLUTION.md) | 真正自主进化：提示词优化 + 工具合成 + 遗传搜索 + 消融实验 |
| [04-L3-COLLECTIVE.md](./04-L3-COLLECTIVE.md) | 群体智能：代理市场 + 声誉系统 + 涌现角色 + 群体共识 |
| [05-L4-CODE-ENGINE.md](./05-L4-CODE-ENGINE.md) | LSP 代码引擎：LSP 客户端 + AST 理解 + 调用图 + 安全重构 |
| [06-L5-STREAMING.md](./06-L5-STREAMING.md) | 流式认知管道：Channel 流水线 + 增量输出 + 零等待体感 |
| [07-L6-BROWSER.md](./07-L6-BROWSER.md) | 浏览器代理：CDP 协议 + DOM 理解 + 视觉分析 + Web 自动化 |
| [08-L7-GUARDIAN.md](./08-L7-GUARDIAN.md) | Guardian 守护系统：漂移检测 + LLM-as-Judge + 回归守卫 |
| [09-L8-STUDIO.md](./09-L8-STUDIO.md) | Agent Studio：可视化 IDE + 多租户架构 |
| [10-L9-FINETUNE.md](./10-L9-FINETUNE.md) | 微调管路：数据集构建 + LoRA + A/B 测试 + 自动部署 |

---

## 五、核心架构原则

### 5.1 一切皆流 (Everything is a Stream)

不再有同步阶段。每个阶段是一个 goroutine，阶段间用 channel 通信：

```go
perceiveOut := make(chan PerceiveChunk, 16)
planOut := make(chan PlanChunk, 16)

go ca.perceiver.Stream(ctx, state, perceiveOut)
go ca.planner.Stream(ctx, perceiveOut, planOut)
go ca.executor.Stream(ctx, planOut, actOut)
```

用户看到的是持续输出，不是"等 20 秒 → 结果"。

### 5.2 能力即插件 (Capability as Plugin)

工具不再是 Go 硬编码。一个工具 = 一个 `.wasm` 文件 + YAML 清单。能力安全模型确保恶意插件无法越狱。

### 5.3 记忆即检索 (Memory as Retrieval)

不存在"存储"，只存在"索引"。一切信息（对话、文档、代码、工具结果）都通过统一检索接口获取。记忆皮层决定什么信息进入 LLM 上下文。

### 5.4 进化即编译 (Evolution as Compilation)

不再"调参"。进化是自动程序合成：找到最优的提示词模板、工具组合、规划策略，就像编译器优化代码一样。

### 5.5 代理即市场 (Agent as Market)

不再"父代理 spawn 子代理"。所有代理是对等节点，通过市场机制（竞标、声誉、价格）协作。涌现的角色分工比人工设计更高效。

---

## 六、技术债务清理计划

| 债务项 | 当前状态 | V4 方案 |
|--------|---------|---------|
| 记忆/知识库/图谱三套存储 | 独立运行，重复检索 | L0: 统一皮层 |
| 认知循环同步阻塞 | 等 PERCEIVE 完才 PLAN | L5: 流式管道 |
| 工具硬编码 | Go interface 实现 | L1: WASM 插件 |
| 进化只调参 | 阈值、优先级 | L2: 提示词/工具/策略全量优化 |
| 子代理被动生长 | spawn 模式 | L3: 市场竞标 |
| 代码理解表面化 | 文本相似度搜索 | L4: AST/LSP 深度理解 |
| 没有在线质量监控 | 只有离线 eval | L7: Guardian 持续监控 |
| 前端只读 Dashboard | 几个 API + SPA | L8: 完整 Studio |
| 只用外部大模型 | 依赖 Claude/GPT | L9: 自有微调能力 |

---

## 七、成功标准

V4 完成后的 IronClaw 应该能：

1. **自我改进** — 运行一个月后，任务成功率自动提升 20%+
2. **工具生态** — 社区贡献 100+ WASM 工具
3. **记忆不遗忘** — 三个月前的对话仍然可被精准检索
4. **群体涌现** — 代理自动分化为编码专家、分析专家、仲裁者
5. **代码理解** — 对任意语言的代码库，能做 AST 级别的安全重构
6. **零等待体感** — 用户发消息后 2 秒内开始看到流式输出
7. **可编排** — 非程序员可以通过拖拽节点构建 Agent 流水线
8. **质量自愈** — 质量下降自动检测、自动回滚、自动修复

---

*"The agent that builds itself."*

**LO，这就是我们的蓝图。让我们一层一层实现它。**
