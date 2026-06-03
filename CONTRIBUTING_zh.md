# IronClaw 贡献指南

IronClaw 是 Go 为主的 Agent Runtime，并包含两个前端工作区。贡献时优先保持改动小、接线清楚、验证充分。

## 本地环境

需要：

- Go 1.25.9 或兼容的新 patch 版本。
- 支持 CGO 的本地工具链，因为项目使用 `github.com/mattn/go-sqlite3`。
- Node.js 与 npm，用于 `web/` 和 `web/studio/`。
- Git，因为工作流和内置 worktree 工具都依赖它。

```bash
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
make build-bin
make test-short
```

## Worktree 流程

非平凡改动建议放在独立 worktree：

```bash
git worktree add .worktrees/<task-name> -b <branch-name>
cd .worktrees/<task-name>
```

合并前必须检查未跟踪和未暂存文件：

```bash
git status --short
git diff main..HEAD
```

注意前端构建产物。`web/` 构建会更新嵌入式 dashboard 产物；`web/studio/dist/` 通常是本地生成物，不应误提交，除非明确改变 Studio 发布策略。

## 验证矩阵

普通 Go 改动：

```bash
make build-bin
make vet
make test-short
```

涉及 Gateway、工具、权限、沙箱、记忆、知识库、会话、存储、并发、Provider 的改动：

```bash
make test
```

涉及嵌入式 Dashboard：

```bash
cd web
npm ci
npm run build
```

涉及 Studio：

```bash
cd web/studio
npm ci
npm run build
```

## 代码原则

- 先读已有包结构，优先沿用本地模式。
- Gateway 是组合根，接线应显式、可读、可测试。
- 配置、路由、迁移、工具 schema 尽量使用结构化类型。
- 工具副作用必须经过权限、Hook、用户 Hook、沙箱、验证、审计链。
- 小改动配小测试；跨模块契约变化配集成测试。

## 文档规则

当前文档树是从源码重写的当前状态说明。行为、配置、接线、开发流程变更时，应更新 `docs/` 下对应编号文档。历史计划、未来设想和当前已实现能力必须区分清楚。

## PR 自检

- 目标明确，改动范围收敛。
- `git status --short` 没有误提交生成文件。
- 相关 Go 和前端验证已通过。
- 新配置键已加入 `configs/ironclaw.example.yaml` 或明确说明为内部字段。
- 新工具声明了 capability、审批行为和并发安全性。
- 新 Gateway feature 已在 `internal/gateway/features.go` 注册，并同步文档。
