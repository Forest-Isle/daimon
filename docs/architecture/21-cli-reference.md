# 21 · CLI 参考 — daimon 子命令

> 入口 `cmd/daimon` · cobra root `daimon`

主二进制 `daimon` 既是常驻运行时（`start`）也是治理运维工具。运维命令对 `~/.daimon` 只读或确定性操作——变更/不可逆动作保留人签（宪法第 4 条）。

## 顶层命令

```
daimon <command> [flags]
```

| 命令 | 职责 | 文件 |
|---|---|---|
| `start` | 启动常驻运行时（首次交互触发 setup 向导）| `main.go`/`setup.go` |
| `version` | 版本 | `main.go` |
| `tui` | 启动 TUI 控制台 | `tui.go` |
| `skill` | 技能管理（list/install/remove/drafts/promote/demote/update）| `skill_promote.go` |
| `memory` | 记忆查/重建索引 | `memory.go` |
| `mcp serve` | 作为 MCP server 暴露能力 | `mcp_serve.go` |
| `replay` | 离线回放评测（重打分/回归/金丝雀）| `replay.go` |
| `correct <session-id>` | 标记回放 session 为已纠正（回归门控）| `correct.go` |
| `proposals` | 提案队列查看 | `proposals.go` |
| `costs` | 成本/ROI 月报 | `costs.go` |
| `undo` | 撤销可逆行动 | `undo.go` |
| `holds` | 补偿性 hold 队列（list/recall）| `holds.go` |
| `world` | 世界模型（history/revert 身份）| `world.go` |
| `attention` | 路由规则（history/revert）| `attention.go` |
| `trust` | 信任等级（list/correct）| `trust.go` |

## start — 运行时

```bash
daimon start [-c <config>] [-d|--dev]
```

- `-c, --config`：配置路径（空则自动发现 `FindConfigPath`）。
- `-d, --dev`：dev 模式用 `configs/daimon.yaml`。
- 首次交互运行触发 `runSetupWizard`（`setup.go`）生成配置。

## replay — 回放评测（[11-replay.md](11-replay.md)）

```bash
daimon replay [--replays <dir>] [--session <id>]              # 离线分析录制
daimon replay --against <config> [--judge-model M] [--max-exchanges N]  # 重打分
daimon replay --against <config> --canary [-c <config>] [--dev]         # 金丝雀门控
```

| flag | 作用 |
|---|---|
| `--replays` | 录制目录（默认 `~/.daimon/replays`）|
| `--session` | 限单 session |
| `--against` | 候选配置重跑录制 exchange（耗 token）|
| `--canary` | 对 must-pass 回归集（corrected ∪ salvaged）门控，**fail 退出码非零**；需 `--against` |
| `-c, --config` | 回归纠正的主配置（自动发现）|
| `--judge-model` | judge 模型（默认候选配置 model）|
| `--max-exchanges` | 重打分 exchange 上限（默认 20，0=无上限）|

## 治理运维命令

### trust（[08-action.md](08-action.md)）
```bash
daimon trust list                       # 列自治/信任等级
daimon trust correct <class> <context-key>  # 经纠正撤销自治（冻结升级）
```

### holds（[08-action.md](08-action.md)）
```bash
daimon holds list        # 列待执行补偿性 hold
daimon holds recall <id> # 撤回（<1s）
```

### undo（[08-action.md](08-action.md)）
```bash
daimon undo list             # 列可撤销回执
daimon undo <receipt-id>     # 撤销单个
daimon undo --episode <id>   # 撤销整个情节的全部可逆动作（LIFO）
```
三者互斥。预览路径 + 人签确认（宪法第 4 条）。

### world / attention（自我修改回滚，[13-selfops.md](13-selfops.md)）
```bash
daimon world history [file]      # 身份文件 git 历史
daimon world revert <file>       # 回滚身份到上一版本
daimon attention history [file]  # 路由规则 git 历史
daimon attention revert <file>   # 回滚 rules.yaml
```

### skill（[17-skills-workflow.md](17-skills-workflow.md)）
```bash
daimon skill list / install <slug> / remove <name> / update [slug]
daimon skill drafts              # 列 staging 蒸馏草稿
daimon skill promote <slug>      # 草稿转正（staging→active，人签）
daimon skill demote <slug>       # 活跃技能降级（active→staging，可逆）
```

### costs（[12-economy.md](12-economy.md)）
```bash
daimon costs [...]   # 成本/ROI 月报：by-class $ + value_created + 节流建议
```

### memory / proposals / correct
```bash
daimon memory search <query> / reindex
daimon proposals list
daimon correct <session-id>      # 标记 session 已纠正 → 入回归 must-pass 集
```

## 全局约定

- 配置自动发现 `FindConfigPath`；`--dev` 全 CLI 齐备用 `configs/daimon.yaml`。
- 运维命令对 `~/.daimon` 只读或确定性文件操作，不触认知路径（零 LLM，除 `replay --against` 显式重跑）。
- 不可逆/核心操作（undo/revert/promote）走预览 + 人签确认。

## 典型运维流程

```bash
# 评估换模型是否回归
daimon correct <bad-session-id>            # 标记问题 session
daimon replay --against candidate.yaml --canary   # 金丝雀门控，非零退出=回归

# 撤销一个坏自治情节
daimon holds list                          # 看待执行
daimon undo --episode <episode-id>         # 整情节回滚

# 回滚自我修改的身份
daimon world history identity.md
daimon world revert identity.md
```

下一篇：[22-glossary.md](22-glossary.md) — 术语表与北极星指标。
