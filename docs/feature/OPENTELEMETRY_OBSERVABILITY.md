# OpenTelemetry 标准化可观测性

**日期**: 2026-04-30  
**范围**: `internal/observability/` + 关键执行路径插桩 + Prometheus `/metrics` 端点

## 概述

在这次改动之前，IronClaw 的运行时可观测性主要依赖两套机制：

1. `slog` 文本日志，适合排查单点问题，但缺少 request-level 关联、缺少跨组件时序、也无法直接对接 Jaeger / Tempo / Grafana 这类标准后端
2. `internal/cogmetrics` 自研聚合指标，适合认知策略健康度分析，但它更偏向**离线统计**和**特定领域指标**，并不是通用 tracing / metrics 基础设施

这带来了几个明显问题：

| 问题 | 旧实现局限 |
|------|-----------|
| LLM 调用难以做端到端关联 | `slog` 只有文本行，没有 span 上下文，没有 parent-child 关系 |
| 工具执行延迟缺少标准 histogram | `cogmetrics` 不覆盖通用工具时延分布，也没有 exporter 生态 |
| 多 provider（Claude / OpenAI）指标口径不统一 | 各自有零散统计逻辑，没有统一 instrument 名称 |
| 外部监控系统接入成本高 | 没有标准 OTLP / Prometheus 接口，只能读日志或自定义 API |
| 生命周期不清晰 | tracing / metrics 初始化、关闭、fallback 逻辑分散在业务代码里 |

本次改动的目标不是替换 `slog` 或 `cogmetrics`，而是补上一层**OpenTelemetry 标准化基础设施**：

- 用 `trace span` 表达关键执行路径
- 用 `metric instrument` 表达延迟、吞吐和计数
- 用统一的 `internal/observability` 包集中管理初始化
- 用 Prometheus `/metrics` 端点对外暴露指标
- 保留 `cogmetrics` 继续承担认知策略健康度统计，二者职责分离

结果上，IronClaw 获得了三种能力：

1. **标准 tracing**：Claude / OpenAI 请求、工具执行、sub-agent spawn 都能进入 OTel trace pipeline
2. **标准 metrics**：LLM latency、token usage、tool latency、cognitive phase latency、sub-agent spawn 统一进入 meter
3. **标准出口**：trace 可发往 `stdout` / OTLP，metric 可被 Prometheus 抓取并进入 Grafana

## 架构设计

### 设计目标

`internal/observability/` 的设计很克制，只负责两类事情：

1. **bootstrap**：初始化全局 tracer provider、meter provider、resource、propagator、exporter
2. **accessor**：向业务代码暴露 `StartSpan()`、`Tracer()`、共享 instruments

它不负责：

- 业务事件定义
- dashboard event bus
- 日志封装
- `cogmetrics` 聚合逻辑

这种拆分让 OpenTelemetry 只承担“标准 telemetry 管道”，而不侵入 Agent 业务模型。

### 包内文件职责

| 文件 | 职责 |
|------|------|
| `internal/observability/doc.go` | 包级说明，声明该包提供 tracing / metrics bootstrap 与共享 instruments |
| `internal/observability/config.go` | 定义最小配置模型 `Config`，并负责默认值与归一化 |
| `internal/observability/tracer.go` | 初始化 global tracer provider、resource、sampler、propagator、trace exporter |
| `internal/observability/meter.go` | 初始化 global meter provider、Prometheus exporter，并注册共享 instruments |

### tracer / meter 分离

这个包把 tracing 和 metrics 明确拆开初始化：

- `InitTracer(ctx, cfg)` 负责 trace pipeline
- `InitMeter(cfg)` 负责 metric pipeline

这样做有三个好处：

1. **失败隔离**：trace exporter 初始化失败不会污染 meter 的实现细节，反之亦然
2. **语义清晰**：span 生命周期与 metric reader 生命周期本来就不同
3. **便于 fallback**：tracer 可以走 `noop`，meter 仍然保留 Prometheus 暴露

### Config 归一化

`internal/observability/config.go` 中的 `Config.normalized()` 做了三件事：

- `ServiceName` 为空时默认写成 `"ironclaw"`
- `SampleRate <= 0` 时归一化为 `1.0`
- `SampleRate > 1.0` 时钳制为 `1.0`

这意味着 sample rate 实际上是一个闭区间 `[0, 1]` 的值，但当前实现将 `<= 0` 视为“回退到全采样”，而不是“零采样”。这在文档和运维配置里需要明确。

### Tracer 初始化路径

`InitTracer()` 的执行流程如下：

```text
Config
  -> normalized()
  -> newResource(service.name, service.version)
  -> newSpanExporter(exporter)
  -> newSampler(sample_rate)
  -> sdktrace.NewTracerProvider(...)
  -> otel.SetTracerProvider(...)
  -> otel.SetTextMapPropagator(...)
```

关键实现点：

- propagator 固定为 `TraceContext + Baggage`
- resource 注入 `service.name` 和 `service.version`
- `service.version` 来自 `debug.ReadBuildInfo()`，取不到时回退 `"dev"`
- sampler 使用 `ParentBased(AlwaysSample)` 或 `ParentBased(TraceIDRatioBased(rate))`

### Meter 初始化路径

`InitMeter()` 的执行流程更简单：

```text
Config
  -> normalized()
  -> newResource(service.name, service.version)
  -> prometheus.New()
  -> sdkmetric.NewMeterProvider(WithReader(exporter))
  -> otel.SetMeterProvider(...)
  -> mustInitInstruments(...)
```

这里有一个很重要的语义差异：

- **trace exporter** 受 `cfg.Exporter` 控制
- **metric exporter** 当前版本不受 `cfg.Exporter` 控制，只要 `Enabled` 为 `true` 就固定启用 Prometheus reader

换句话说：

- `exporter: stdout` 表示 trace 打到 stdout，metric 仍然暴露到 `/metrics`
- `exporter: otlp_grpc` 表示 trace 发到 OTLP gRPC，metric 仍然暴露到 `/metrics`
- `exporter: noop` 表示 trace 走 no-op provider，但只要 `enabled: true`，metric 仍然存在
- 只有 `enabled: false` 才会同时关闭 tracing 和 metrics

这不是通用 OTel 约定，而是 IronClaw 当前实现的设计选择。

### noop fallback 设计

这个包大量使用 no-op fallback，目的是让业务代码完全不用关心“观测性是否启用”：

| 场景 | 行为 |
|------|------|
| `InitTracer()` 收到 `enabled=false` | 安装 `tracenoop.NewTracerProvider()` |
| `InitTracer()` 收到 `exporter=""` 或 `"noop"` | 同样安装 no-op tracer provider |
| `InitMeter()` 收到 `enabled=false` | 安装 `metricnoop.NewMeterProvider()` |
| meter 未启用 | `mustInitInstruments()` 仍会绑定到 no-op meter，业务侧调用不会 panic |
| shutdown | 统一返回 `noopShutdown`，上层可直接调用 |

因此业务代码可以始终写成：

```go
ctx, span := observability.StartSpan(ctx, "tool.execute")
defer span.End()

observability.ToolExecutionDuration.Record(ctx, durationMs)
```

不需要显式判断 telemetry 是否开启。

### gateway 初始化逻辑

`internal/gateway/init_observability.go` 负责把配置层的 `config.ObservabilityConfig` 映射成 `observability.Config`，并按以下顺序初始化：

1. `InitTracer(ctx, obsCfg)`
2. `InitMeter(obsCfg)`
3. 成功后记录 `slog.Info("observability initialized", ...)`

失败处理也比较严谨：

- 如果 tracer 初始化失败，直接返回错误
- 如果 meter 初始化失败，会先调用 `tracerShutdown(ctx)` 回收已经成功创建的 tracer provider，再返回错误

最终它返回一个组合 shutdown：

```go
func(ctx context.Context) {
    _ = tracerShutdown(ctx)
    _ = meterShutdown(ctx)
}
```

上层只需要保存一个 `obsShutdown` 即可。

## 配置

### ObservabilityConfig 字段

`internal/config/config.go` 中新增了 `ObservabilityConfig`，字段与 `internal/observability.Config` 一一对应：

| 字段 | 类型 | YAML 键 | 说明 |
|------|------|---------|------|
| `Enabled` | `bool` | `enabled` | 是否启用 OpenTelemetry。`false` 时 tracing 与 metrics 都退化为 no-op |
| `ServiceName` | `string` | `service_name` | OTel `service.name`。为空时默认 `"ironclaw"` |
| `Exporter` | `string` | `exporter` | trace exporter 选择：`otlp_grpc` / `otlp_http` / `stdout` / `noop` |
| `Endpoint` | `string` | `endpoint` | OTLP endpoint，例如 `localhost:4317` 或 `localhost:4318` |
| `SampleRate` | `float64` | `sample_rate` | trace 采样率。`<=0` 和 `>1` 都会被归一化到 `1.0` 或上限 `1.0` |

### 4 种 exporter 对比

| exporter | trace 行为 | metric 行为 | 适用场景 | 注意事项 |
|----------|-----------|------------|---------|---------|
| `otlp_grpc` | 通过 `otlptracegrpc` 发往 OTLP gRPC 后端 | 仍通过 Prometheus `/metrics` 暴露 | 生产接 Jaeger / Tempo / Grafana Agent / OTel Collector | `endpoint` 形如 `host:4317`，当前代码使用 `WithInsecure()` |
| `otlp_http` | 通过 `otlptracehttp` 发往 OTLP HTTP 后端 | 仍通过 Prometheus `/metrics` 暴露 | 某些托管后端或只开了 HTTP ingest 的 Collector | `endpoint` 形如 `host:4318`，同样使用 `WithInsecure()` |
| `stdout` | 通过 `stdouttrace` pretty print 输出 span | 仍通过 Prometheus `/metrics` 暴露 | 本地开发、调试 span attributes、确认 parent-child 结构 | 输出直接写到 `os.Stdout`，高频请求下会很吵 |
| `noop` | tracer provider 退化为 no-op | 只要 `enabled: true`，metrics 仍然启用 | 想先接 metric，不想导出 trace | 这不是“全部关闭”；要完全关闭需 `enabled: false` |

### YAML 示例

#### 1. 本地开发：stdout trace + Prometheus metrics

```yaml
observability:
  enabled: true
  service_name: ironclaw-dev
  exporter: stdout
  sample_rate: 1.0
```

#### 2. 生产：OTLP gRPC trace + Prometheus metrics

```yaml
observability:
  enabled: true
  service_name: ironclaw-prod
  exporter: otlp_grpc
  endpoint: otel-collector.monitoring.svc:4317
  sample_rate: 0.2
```

#### 3. 仅保留 metrics，不导出 trace

```yaml
observability:
  enabled: true
  service_name: ironclaw-metrics-only
  exporter: noop
  sample_rate: 1.0
```

#### 4. 完全关闭

```yaml
observability:
  enabled: false
```

## 插桩覆盖范围

下面的表格覆盖这次 feature 真正接入的关键插桩点。为了避免把 dashboard 事件和 OTel span / metric 混在一起，表格同时标注“OTel 插桩”和“辅助观测事件”。

| 文件 | 插桩点 | span / metric / event 名称 | 关键 attributes / fields |
|------|--------|----------------------------|---------------------------|
| `internal/tool/interceptor.go` | 拦截器链入口 `Execute()` | span `tool.execute` | `tool.name`, `session.id` |
| `internal/tool/interceptor.go` | 工具执行结束 `recordToolExecution()` | histogram `tool.execution.duration` | `tool.name`, `status=success|error` |
| `internal/agent/stream.go` | Claude 同步调用 `Complete()` | span `llm.complete` | `provider=claude`, `model` |
| `internal/agent/stream.go` | Claude 同步调用完成 | histogram `llm.request.duration` | `provider=claude`, `model`, `operation=complete` |
| `internal/agent/stream.go` | Claude token 统计 | counter `llm.tokens.total` | `provider=claude`, `model`, `token_type=input|output` |
| `internal/agent/stream.go` | Claude 流式调用 `Stream()` / iterator 生命周期 | span `llm.complete` | `provider=claude`, `model` |
| `internal/agent/stream.go` | Claude stream finalize | histogram `llm.request.duration` | `provider=claude`, `model`, `operation=stream` |
| `internal/agent/openai.go` | OpenAI 同步调用 `Complete()` | span `llm.complete` | `provider=openai`, `model` |
| `internal/agent/openai.go` | OpenAI 同步调用完成 | histogram `llm.request.duration` | `provider=openai`, `model`, `operation=complete` |
| `internal/agent/openai.go` | OpenAI token 统计 | counter `llm.tokens.total` | `provider=openai`, `model`, `token_type=input|output` |
| `internal/agent/openai.go` | OpenAI 流式调用 `Stream()` / iterator 生命周期 | span `llm.complete` | `provider=openai`, `model` |
| `internal/agent/openai.go` | OpenAI stream finalize | histogram `llm.request.duration` | `provider=openai`, `model`, `operation=stream` |
| `internal/agent/cognitive.go` | `PERCEIVE` 完成 | histogram `cognitive.phases.duration` | `phase=perceive` |
| `internal/agent/cognitive.go` | `PLAN` 完成 | histogram `cognitive.phases.duration` | `phase=plan` |
| `internal/agent/cognitive.go` | `ACT` 完成 | histogram `cognitive.phases.duration` | `phase=act` |
| `internal/agent/cognitive.go` | `OBSERVE` 完成 | histogram `cognitive.phases.duration` | `phase=observe` |
| `internal/agent/cognitive.go` | `REFLECT` 完成 | histogram `cognitive.phases.duration` | `phase=reflect` |
| `internal/agent/cognitive.go` | 认知阶段开始/结束 | dashboard event `phase.start` / `phase.end` | `phase`, `duration_ms` |
| `internal/agent/cognitive.go` | replan 启动 | dashboard event `replan.start` | `attempt`, `reason` |
| `internal/agent/cognitive.go` | plan 生成 | dashboard event `plan.generated` | `task_count`, `complexity`, `has_direct_reply` |
| `internal/agent/cognitive.go` | observe 完成 | dashboard event `observation.result` | `passed`, `failed`, `total`, `overall_progress` |
| `internal/agent/subagent.go` | `Spawn()` 入口 | span `subagent.spawn` | `agent.name`, `parent.id` |
| `internal/agent/subagent.go` | `Spawn()` 结束 | counter `subagent.spawns` | `agent.name`, `status=success|error` |
| `internal/agent/subagent.go` | sub-agent 生命周期 | dashboard event `subagent.spawn` / `subagent.complete` | `parent_session_id`, `agent_name`, `task`, `duration_ms`, `succeeded` |
| `internal/agent/runtime.go` | simple runtime 会话起止 | dashboard event `session.start` / `session.end` | `channel`, `succeeded`, `duration_ms` |
| `internal/agent/runtime.go` | 每次迭代 metrics 推送 | dashboard event `metrics.update` | `iteration`, `max_iterations`, `utilization`, `input_tokens`, `output_tokens`, `cache_create`, `cache_read`, `model`, `provider` |
| `internal/dashboard/server.go` | dashboard HTTP mux | endpoint `/metrics` | 无业务 attributes；由 `promhttp.Handler()` 直接暴露 collector |
| `internal/gateway/gateway.go` | gateway 生命周期 | `obsShutdown` 接入 | 无 attributes；负责统一关闭 tracer/meter provider |

### 认知阶段的 5 个 Record 调用位置

`internal/agent/cognitive.go` 里的 `CognitivePhasesDuration.Record(...)` 出现在 5 个明确阶段结束点：

| 阶段 | 记录时机 | 属性 |
|------|---------|------|
| `PERCEIVE` | `ca.perceiver.Run()` 返回后 | `phase=perceive` |
| `PLAN` | `ca.planner.Run()` 返回后 | `phase=plan` |
| `ACT` | `ca.executor.RunWithContext()` 返回后 | `phase=act` |
| `OBSERVE` | `ca.observer.Run()` 返回后 | `phase=observe` |
| `REFLECT` | `ca.reflector.Run()` 返回后 | `phase=reflect` |

这意味着它统计的是**阶段 wall-clock latency**，而不是阶段内部子步骤的细分耗时。

## 预定义 Instruments

`internal/observability/meter.go` 里集中注册了 6 个共享 instrument：

| Instrument 名称 | 类型 | 单位 | labels / attributes | 用途 |
|-----------------|------|------|---------------------|------|
| `llm.request.duration` | `Int64Histogram` | `ms` | `provider`, `model`, `operation` | 统计同步/流式 LLM 调用总时延 |
| `llm.tokens.total` | `Int64Counter` | 无 | `provider`, `model`, `token_type` | 统计 input / output token 累积量 |
| `tool.execution.duration` | `Int64Histogram` | `ms` | `tool.name`, `status` | 统计工具执行时延和错误分布 |
| `cognitive.phases.duration` | `Int64Histogram` | `ms` | `phase` | 统计五阶段 cognitive loop 的阶段耗时 |
| `subagent.spawns` | `Int64Counter` | 无 | `agent.name`, `status` | 统计 sub-agent spawn 尝试次数及成功/失败 |
| `active.sessions` | `Int64UpDownCounter` | 无 | 当前版本未写入 attributes | 预留给在线会话数；**当前代码已注册但尚未在执行路径中 `Add()` / `Sub()`** |

### label 设计说明

#### `llm.request.duration`

- `provider`：当前实现有 `claude`、`openai`
- `model`：直接写入实际请求模型名
- `operation`：`complete` 或 `stream`

这让同一个 provider 可以同时分析同步与流式请求。

#### `llm.tokens.total`

- `token_type=input`
- `token_type=output`

当前没有把 cache token 单独做成独立 metric，而是继续通过 runtime snapshot / dashboard event 暴露给前端。

#### `tool.execution.duration`

- `tool.name`：工具名
- `status`：`success` 或 `error`

这允许直接在 Prometheus / Grafana 按工具名和状态聚合 P95 / P99。

#### `active.sessions`

虽然它已经是稳定注册的 instrument，但目前执行路径没有任何 `observability.ActiveSessions.Add(...)` 调用，因此 `/metrics` 中不会产生有意义的活动会话数据。文档里需要把这点明确写出来，避免运维误以为“少了某个 exporter 配置”。

## 数据流

下面是本次 OpenTelemetry 观测链路的完整数据流。为了贴近实现，trace 和 metric 分开画，但都汇聚到 `internal/observability` 的 bootstrap 层。

```text
┌──────────────────────────────────────────────────────────────────────────┐
│                           IronClaw 执行路径                              │
│                                                                          │
│  ClaudeProvider / OpenAIProvider                                         │
│        │                                                                 │
│        ├── span: llm.complete                                            │
│        ├── metric: llm.request.duration                                  │
│        └── metric: llm.tokens.total                                      │
│                                                                          │
│  Tool Interceptor Chain                                                  │
│        │                                                                 │
│        ├── span: tool.execute                                            │
│        └── metric: tool.execution.duration                               │
│                                                                          │
│  CognitiveAgent                                                          │
│        │                                                                 │
│        ├── metric: cognitive.phases.duration                             │
│        └── dashboard events: phase / plan / observe / replan             │
│                                                                          │
│  SubAgentManager                                                         │
│        │                                                                 │
│        ├── span: subagent.spawn                                          │
│        ├── metric: subagent.spawns                                       │
│        └── dashboard events: subagent.spawn / subagent.complete          │
└───────────────────────────────┬──────────────────────────────────────────┘
                                │
                                ▼
                 ┌──────────────────────────────────┐
                 │     internal/observability       │
                 │                                  │
                 │  InitTracer()    InitMeter()     │
                 │  StartSpan()     shared metrics   │
                 └───────────────┬───────────┬──────┘
                                 │           │
                 trace pipeline   │           │   metric pipeline
                                 ▼           ▼
                     ┌────────────────┐  ┌─────────────────────┐
                     │ TracerProvider │  │  MeterProvider      │
                     │ + Resource     │  │  + PrometheusReader │
                     │ + Sampler      │  │  + shared instruments│
                     └───────┬────────┘  └──────────┬──────────┘
                             │                      │
               ┌─────────────┼─────────────┐        │
               │             │             │        │
               ▼             ▼             ▼        ▼
        stdouttrace     OTLP gRPC      OTLP HTTP   /metrics
           │               │              │          │
           ▼               ▼              ▼          ▼
      本地终端日志      OTel Collector  OTel Collector Prometheus Server
           │               │              │          │
           └───────────────┴──────┬───────┘          │
                                   ▼                  ▼
                               Jaeger / Tempo      Grafana
```

## Prometheus `/metrics` 端点

### 端点位置

`internal/dashboard/server.go` 在 `NewServerMux()` 里新增：

```go
mux.Handle("/metrics", promhttp.Handler())
```

这有两个直接含义：

1. `/metrics` 挂在 dashboard HTTP server 上，而不是单独再起一个 telemetry server
2. 它没有套 `authMiddleware`，因此与 `/api/*` 不同，当前实现默认是**公开端点**

如果 dashboard 绑定在对外地址上，这个行为需要额外关注。

### 为什么 `promhttp.Handler()` 能看到 OTel 指标

`InitMeter()` 里使用的是 `go.opentelemetry.io/otel/exporters/prometheus` exporter。这个 exporter 会把 OTel metric reader 暴露成 Prometheus collector，并由 `promhttp.Handler()` 所服务的 registry 输出。

因此链路是：

```text
business code
  -> observability.<Instrument>.Record/Add(...)
  -> OTel MeterProvider
  -> Prometheus exporter / collector
  -> promhttp.Handler()
  -> /metrics exposition
```

### Prometheus 抓取配置

最小抓取示例：

```yaml
scrape_configs:
  - job_name: ironclaw
    metrics_path: /metrics
    static_configs:
      - targets:
          - 127.0.0.1:8080
```

如果 dashboard 服务不是监听在 `:8080`，把 target 改成实际 `dashboard.addr`。

### 示例 metric 输出

下面给出典型 Prometheus exposition 片段。名称会经过 Prometheus exporter 规范化，例如：

- dot 命名会转成 underscore
- counter 会带 `_total`
- 带 `ms` 单位的 histogram 通常会带 `_milliseconds`

```text
# HELP llm_request_duration_milliseconds LLM request latency.
# TYPE llm_request_duration_milliseconds histogram
llm_request_duration_milliseconds_bucket{provider="claude",model="claude-sonnet-4",operation="stream",le="100"} 3
llm_request_duration_milliseconds_bucket{provider="claude",model="claude-sonnet-4",operation="stream",le="500"} 8
llm_request_duration_milliseconds_sum{provider="claude",model="claude-sonnet-4",operation="stream"} 2874
llm_request_duration_milliseconds_count{provider="claude",model="claude-sonnet-4",operation="stream"} 8

# HELP llm_tokens_total Total LLM tokens by type.
# TYPE llm_tokens_total counter
llm_tokens_total{provider="openai",model="gpt-5.4",token_type="input"} 18432
llm_tokens_total{provider="openai",model="gpt-5.4",token_type="output"} 2617

# HELP tool_execution_duration_milliseconds Tool execution latency.
# TYPE tool_execution_duration_milliseconds histogram
tool_execution_duration_milliseconds_bucket{tool_name="bash",status="success",le="50"} 4
tool_execution_duration_milliseconds_bucket{tool_name="bash",status="success",le="250"} 11

# HELP subagent_spawns Sub-agent spawn attempts.
# TYPE subagent_spawns counter
subagent_spawns_total{agent_name="researcher",status="success"} 12
subagent_spawns_total{agent_name="researcher",status="error"} 1
```

这里的样例重点是说明标签和序列结构，不是固定数值。

## Span 设计

### 1. 工具执行 span：`tool.execute`

`internal/tool/interceptor.go` 在整个 interceptor chain 外层创建统一 span：

```go
ctx, span := observability.StartSpan(ctx, "tool.execute",
    trace.WithAttributes(
        attribute.String("tool.name", call.ToolName),
        attribute.String("session.id", call.SessionID),
    ))
```

这意味着：

- 无论链上有多少个 interceptor，span 都覆盖**完整工具执行生命周期**
- 如果未来有 sandbox、approval、retry、cache 命中等拦截器，都能落在同一个 tool span 内

错误处理分两层：

1. 返回 `error` 时：
   - `span.RecordError(err)`
   - `span.SetStatus(codes.Error, err.Error())`
   - metric `status=error`
2. `result.Error != ""` 但函数没有返回 Go `error` 时：
   - metric 仍记成 `status=error`
   - span 不会自动 `RecordError`

这个细节很重要：当前实现对“软失败”更偏向 metric 维度，而不是 span status 维度。

### 2. LLM span：统一使用 `llm.complete`

Claude 和 OpenAI provider 都采用同一个 span 名称 `llm.complete`，只通过 attributes 区分：

| attribute | 含义 |
|-----------|------|
| `provider` | `claude` 或 `openai` |
| `model` | 实际请求模型 |

这样做的好处是跨 provider 查询时更统一，例如：

- “所有 LLM 调用的总时延”
- “按 `provider` 拆分的错误率”
- “某个模型的慢请求比例”

同步和流式没有拆成两个 span 名称，而是通过 metric `operation=complete|stream` 区分。span 层保持统一，metric 层表达调用模式。

### 3. Sub-agent span：`subagent.spawn`

`internal/agent/subagent.go` 在 `Spawn()` 最外层创建 span：

| attribute | 含义 |
|-----------|------|
| `agent.name` | 子代理规格名 |
| `parent.id` | 父会话 / 父代理 ID |

这个 span 覆盖：

- session ID 生成
- scoped tool registry 构造
- sub runtime 执行
- result 汇总
- recovery retry 前的首次执行路径

最终是否成功，则通过 `recordSubAgentSpawn()` 把结果写入 counter label `status`。

### 4. 错误处理约定

本次实现中，span 的错误处理基本遵循同一模式：

```go
span.RecordError(err)
span.SetStatus(codes.Error, err.Error())
```

应用位置包括：

- Claude `Complete()`
- Claude stream iterator `finish(err)`
- OpenAI `Complete()`
- OpenAI stream iterator `finish(err)`
- `recordToolExecution()` 的返回错误分支
- `recordSubAgentSpawn()` 的错误分支

因此 trace backend 里不仅能看到异常结束的 span，还能保留 message 级错误原因。

### 5. 流式 LLM span 的特殊处理

流式调用最大的设计点是：**span 不能在 `Stream()` 返回 iterator 时立即结束**。

本次实现采用 iterator 持有 span 的方式：

- Claude：
  - `Stream()` 创建 span，但不结束
  - `claudeStreamIterator.finish()` 负责记录时延并 `span.End()`
  - `MessageStopEvent`、stream error、`Close()` 都会触发 finalize
- OpenAI：
  - `Stream()` 创建 span，但不结束
  - `openaiStreamIterator.finish()` 负责记录时延并 `span.End()`
  - `finish()` 通常在 stream error、finish reason、`Close()` 时执行
  - 运行时在消费循环结束后显式调用 `stream.Close()`，从而保证最终收口

这让流式 span 的持续时间真实覆盖：

```text
请求发出 -> 首个 token 到达 -> 中间所有 delta -> tool call 聚合 -> iterator 关闭
```

而不是只覆盖“HTTP 建连成功”。

## 生命周期管理

### InitTracer / InitMeter 的 shutdown 函数

`InitTracer()` 和 `InitMeter()` 都返回：

```go
func(context.Context) error
```

这种签名的优点是：

- 可以传入 shutdown deadline
- 与 OTel SDK 原生 `Shutdown(ctx)` 签名一致
- 组合时不需要额外适配层

### gateway 如何接入 `obsShutdown`

`internal/gateway/gateway.go` 在 `Gateway` 结构体中增加：

```go
obsShutdown func(context.Context)
```

并在 `New()` 中：

1. 先给它一个默认 no-op
2. 调用 `initObservability(context.Background(), *cfg)`
3. 如果初始化失败，只记 warning，不阻断 gateway 启动
4. 成功后把返回值保存到 `gw.obsShutdown`

这里的策略是“**观测性失败不影响主功能**”，与前面 no-op fallback 的思路一致。

### Stop 时的回收顺序

`Gateway.Stop(ctx)` 在停止 channel、scheduler、dashboard、memory background task、docker session 等组件之后，统一执行：

```go
gw.obsShutdown(ctx)
```

然后才关闭数据库：

```go
_ = gw.db.Close()
```

这个顺序是合理的，因为：

- telemetry shutdown 期间仍可能 flush 少量内存数据
- flush 过程不应该依赖已关闭的业务 DB

### meter 初始化失败时的回滚

`initObservability()` 里最值得写进文档的一点，是 meter 初始化失败后的回滚逻辑：

```go
meterShutdown, err := observability.InitMeter(obsCfg)
if err != nil {
    _ = tracerShutdown(ctx)
    return nil, fmt.Errorf("init meter: %w", err)
}
```

也就是说，gateway 不会留下一个“tracer 已半初始化、meter 没起来”的残缺状态。

## 使用指南

### 开发调试：stdout exporter

本地排查 span attributes、stream 生命周期和错误路径时，最简单的方式是：

```yaml
observability:
  enabled: true
  service_name: ironclaw-dev
  exporter: stdout
  sample_rate: 1.0
```

你会看到 pretty-printed trace 直接输出到终端，同时 `/metrics` 仍可本地抓取：

```bash
curl http://127.0.0.1:8080/metrics
```

适合验证：

- `tool.execute` 有没有打到正确的 `tool.name`
- `llm.complete` 的 `provider/model` 是否符合预期
- stream span 是否在 iterator 关闭后才结束

### 生产接 Jaeger / Tempo：OTLP gRPC

生产环境推荐模式是：

1. trace 通过 `otlp_grpc` 发到 OTel Collector
2. Collector 再转发到 Jaeger / Tempo
3. metrics 由 Prometheus 直接抓 `/metrics`
4. Grafana 同时读取 Tempo + Prometheus

配置示例：

```yaml
observability:
  enabled: true
  service_name: ironclaw
  exporter: otlp_grpc
  endpoint: otel-collector.monitoring.svc:4317
  sample_rate: 0.1
```

Prometheus 抓取：

```yaml
scrape_configs:
  - job_name: ironclaw
    metrics_path: /metrics
    static_configs:
      - targets:
          - ironclaw.monitoring.svc:8080
```

Grafana 中可以直接做三类图：

| 图表 | 数据源 | 查询方向 |
|------|--------|----------|
| LLM P95 latency | Prometheus | `llm_request_duration_*` 按 `provider/model/operation` 聚合 |
| Tool error rate | Prometheus | `tool_execution_duration_*` 按 `tool_name,status` 分组 |
| 单次请求调用链 | Tempo / Jaeger | 查 `llm.complete` / `tool.execute` / `subagent.spawn` spans |

### 仅接 Grafana metrics，不接 trace backend

如果当前阶段只想把指标接进 Grafana，不想部署 trace backend，可以这样配：

```yaml
observability:
  enabled: true
  service_name: ironclaw
  exporter: noop
```

效果是：

- traces 不导出
- metrics 仍然走 `/metrics`

这是一个很实用的渐进式接入路径。

### 推荐排障顺序

| 现象 | 优先检查 |
|------|---------|
| `/metrics` 没有 OTel 指标 | `observability.enabled` 是否为 `true`，dashboard server 是否已启动 |
| trace backend 收不到 span | `exporter` 是否不是 `noop`，`endpoint` 是否正确 |
| 本地 stdout 没有输出 | 请求路径是否真的走到了 Claude / OpenAI / tool / subagent 插桩代码 |
| `active.sessions` 没数据 | 当前版本未写入，是代码限制，不是配置错误 |

## 涉及文件

下面按“新增 / 修改”列出这次 feature 的完整文件集合。除了题目要求阅读的文件，也包含了为了让观测能力真正落地而必须配套变更的桥接文件。

| 文件 | 类型 | 说明 |
|------|------|------|
| `internal/observability/doc.go` | 新增 | 新 package 的包级说明 |
| `internal/observability/config.go` | 新增 | OTel bootstrap 配置与默认值归一化 |
| `internal/observability/tracer.go` | 新增 | tracer provider、resource、propagator、sampler、trace exporter 初始化 |
| `internal/observability/meter.go` | 新增 | meter provider、Prometheus exporter 与 6 个共享 instruments |
| `internal/gateway/init_observability.go` | 新增 | gateway 层的统一初始化与组合 shutdown |
| `internal/config/config.go` | 修改 | 新增 `ObservabilityConfig` |
| `internal/tool/interceptor.go` | 修改 | 工具执行 span 与 latency histogram |
| `internal/agent/stream.go` | 修改 | Claude `Complete/Stream` 的 span、duration、token metrics 插桩 |
| `internal/agent/openai.go` | 修改 | OpenAI `Complete/Stream` 的 span、duration、token metrics 插桩 |
| `internal/agent/cognitive.go` | 修改 | 五阶段认知耗时 histogram，以及 dashboard phase / plan / observe / replan 事件 |
| `internal/agent/subagent.go` | 修改 | `subagent.spawn` span、`subagent.spawns` counter、sub-agent 生命周期事件 |
| `internal/agent/runtime.go` | 修改 | simple runtime 的 session start/end 与 metrics.update 事件 |
| `internal/agent/dashboard_emitter.go` | 修改 | dashboard emitter 接口扩展到 session / metrics / plan / replan / observation / subagent |
| `internal/dashboard/emitter.go` | 修改 | 把新增事件真正发布到 dashboard bus |
| `internal/dashboard/state_tracker.go` | 修改 | 接收新增事件并维护 session / sub-agent / compression 状态 |
| `internal/dashboard/server.go` | 修改 | 注册 `/metrics` 端点 |
| `internal/gateway/gateway.go` | 修改 | 持有 `obsShutdown`，在 `Stop()` 中统一回收 |
| `docs/feature/OPENTELEMETRY_OBSERVABILITY.md` | 新增 | 本文档 |

## 后续扩展方向

这次实现已经把“标准 OTel 基础设施”搭起来了，但还有很多可以继续扩展的方向：

1. **接通 `active.sessions`**  
   现在它只注册未写入。可以在 `session.start` / `session.end` 或 runtime 入口出口处做 `Add(+1/-1)`，补上实时会话 gauge。

2. **把 cache token 独立成标准 metric**  
   当前 cache creation / read token 只通过 dashboard snapshot 暴露，没有标准 OTel instrument。可以增加 `llm.cache.tokens.total` 之类的 counter。

3. **给 cognitive phases 增加更多 attributes**  
   例如 `complexity`、`replan_attempt`、`session.mode`，让 phase latency 不只是单维度的 `phase`。

4. **把 context compression 也接入标准 metric / span**  
   当前压缩事件主要走 dashboard event。可以增加 `context.compress.duration`、`context.compress.count`、`context.compress.ratio` 等指标。

5. **补 distributed trace 传播到外部工具 / MCP**  
   现在 propagator 已安装，但执行路径还没有把 trace context 注入到外部调用边界。后续可以在 HTTP / MCP / tool sandbox 边界做 context propagation。

6. **为失败分类增加语义化 attributes**  
   例如给 LLM span 增加 `error.type`、`retry.count`、`stop_reason`，给 tool span 增加 `approval.required`、`sandbox.mode`。

7. **拆分独立 telemetry server**  
   当前 `/metrics` 跟 dashboard 共用一个 HTTP server，而且没有 auth。生产环境可以考虑单独监听内网地址，降低暴露面。

8. **加入 OTLP metrics / logs exporter**  
   当前 metric 固定走 Prometheus pull。后续如果要统一进入 OTel Collector，也可以增加 push 模式并保留 Prometheus 兼容层。

9. **在 trace 中补 runtime / cognitive parent-child 层级**  
   目前最清晰的是 provider、tool、sub-agent 这些局部 span。后续可以增加顶层 `runtime.handle_message` 或 `cognitive.turn` span，把整条链路串起来。

10. **为 dashboard 事件和 OTel span 建立 trace correlation**  
   目前 dashboard state 与 OTel trace 是两条平行链路。后续可以把 trace ID 写进 dashboard event，做到“前端状态面板直接跳转到 trace backend”。

## 总结

这次 OpenTelemetry 可观测性改动，核心价值不是“多了几个埋点”，而是把 IronClaw 从 `slog + 自研聚合` 提升到了**标准 telemetry 架构**：

- `internal/observability` 统一封装 bootstrap 与 fallback
- 关键执行路径已经具备可查询、可聚合、可导出的 span / metric
- `/metrics` 让 Prometheus / Grafana 接入门槛大幅降低
- `obsShutdown` 让生命周期收口到 gateway 统一管理

同时，这个版本也保留了清晰边界：

- `cogmetrics` 继续负责认知健康度，不强行并入 OTel
- `active.sessions` 等未来指标先注册、后逐步落地
- trace exporter 与 metric exporter 当前刻意采用“OTLP + Prometheus pull”双轨制

对于 IronClaw 来说，这是从“能看日志”迈向“能做标准化运行观测”的基础一步。
