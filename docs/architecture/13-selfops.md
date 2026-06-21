# 13 · selfops — 自我运维

> 包路径 `internal/selfops` · gateway 接线 `selfops.go` · 蓝图 §4.12

## 职责

让"运维它"这件事也消失。timer 每日发 `internal.health` 事件 → 健康看门狗巡检 → 异常写提案或直接 WakeUser。自我修改一律走金丝雀回放 + 单独 git commit，回滚 = revert。

## 核心类型

```go
// internal/selfops/health.go
type Signals struct {
    OutcomesTotal int
    Salvaged      int
    RoutingMisses int
    HoldsPending  int
    DiskFreePct   float64
    ErrorClusters []ErrorCluster
}
func (s Signals) SalvagedRate() float64  // Salvaged / OutcomesTotal

type Thresholds struct {  // 零阈值 = 禁用该检查
    MaxSalvagedRate     float64
    MaxRoutingMisses    int
    MaxHoldsPending     int
    MinDiskFreePct      float64
    MaxErrorClusterSize int
}

type Finding struct { Severity Severity; Title, Detail string }  // warn | critical
func Evaluate(sig Signals, th Thresholds) []Finding  // 纯函数，按严重度排序
```

## 五个信号

`Evaluate`（`health.go:80`，纯确定性函数）检查：

| 信号 | 来源 | 阈值 | 严重度 |
|---|---|---|---|
| 磁盘空间 | `Statfs` 磁盘 % | `MinDiskFreePct` | critical |
| WakeUser 路由漏报 | `heart.feedback` | `MaxRoutingMisses`（默认 0 禁用待信号成熟）| critical |
| salvage 率 | `world.ListJournal` + `ClassifyOutcome` | `MaxSalvagedRate` | warn |
| holds 积压 | `action.CountPendingHolds` | `MaxHoldsPending` | warn |
| error 聚类 | `ErrorRing` logsink | `MaxErrorClusterSize`（默认 5）| warn |

零阈值禁用对应检查——信号成熟前可保守关闭。

## 巡检（离认知路径）

gateway `selfops.go`：`internal.health` timer 事件 → 确定性巡检（镜像 daily-brief，#5/#7 零 LLM，离认知路径）：

```
gatherSignals: ListJournal(salvage 率) + heart.feedback(路由漏报)
             + CountPendingHolds(积压) + Statfs(磁盘 %) + ClusterErrors(ErrorRing)
Evaluate(signals, thresholds) → findings
  critical → WakeUser（推用户）
  warn     → 去重提案
```

**失败安全（最关键）**：监测读取失败 → **良性默认**（磁盘 100%、计数 0）→ **绝不 WakeUser**。看门狗自身的故障绝不制造误唤醒。

双门控：feature `selfops`（默认关）+ `health_interval_minutes > 0`。关时逐字节不变。`/selfops` 只读命令查看 top 信号。

## 错误日志聚类（第 5 信号）

```go
// internal/selfops/logsink.go
ErrorRing  // 有界（256）线程安全错误环
teeHandler // slog 包 base 忠实委托，仅额外把 r.Level >= Error 入环 → 实际日志输出不变
func ClusterErrors(msgs []string) []ErrorCluster  // 按 message 精确计数，确定序
```

`setupLogging` + `setupTUILogging` 都装 `teeHandler`（TUI 有独立 file logger，两入口都包）。`ClusterErrors` 顶簇 `> MaxErrorClusterSize` → Warn finding。只读诊断，零行为变更。

## 自我修改可逆性（金丝雀 + git）

蓝图要求"任何自我修改可单独回滚"。三类自我修改各有 revert CLI（[20-security-governance.md](20-security-governance.md)）：

| 自我修改 | 落地 | 回滚 |
|---|---|---|
| 身份 edit | `world_edit` 写 identity.md + `vcs.Commit` | `daimon world revert` |
| 技能转正 | `skill.PromoteDraft` 文件移动 | `daimon skill demote` |
| 路由规则 | `synthesize` 写 rules.yaml + `vcs.Commit` | `daimon attention revert` |

`~/.daimon` 整目录 git 化，回滚 = revert。自我修改金丝雀回放（git commit + 回滚，忠实 action 判分基底已就位）为后续切片。

## 数据

读 world journal / action holds / heart feedback；无独立表（`ErrorRing` 内存）。

## 跨包接缝

- **← world / action / heart**：gatherSignals 读各子系统。
- **→ channel**：critical → WakeUser 通知。
- **→ proposals**：warn → 去重提案。
- **← slog**：`teeHandler` 包 base handler 捕错。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 巡检离认知路径 | 宪法 5/7 | 确定性巡检零 LLM，不阻塞事件 |
| 失败安全良性默认 | 安全 | 看门狗故障绝不误唤醒用户 |
| 纯 Evaluate 函数 | 可测 | 零副作用，确定性可断言 |
| 自我修改各有 revert | §563/宪法 4 | 任何自我修改可单独回滚 |

蓝图验收：注入故障（断网/磁盘满/provider 5xx）后代理能自报症状；任何自我修改可单独回滚。

下一篇：[14-gateway.md](14-gateway.md) — 组合根与子系统装配。
