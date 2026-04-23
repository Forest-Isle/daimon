# Cognitive Health Circuit Breaker

## Overview

Adds a health monitoring and automatic intervention system that detects degraded agent performance and triggers corrective actions. The Circuit Breaker evaluates sliding-window metrics against configurable thresholds, computing a health score (0.0-1.0) and triggering registered callbacks when violations occur.

## Problem

The existing `cogmetrics` package collected metrics (replan counts, tool durations, confidence scores) but had no thresholds, no health assessment, and no automatic response to degraded conditions. A failing agent would continue operating at degraded quality without any self-protective behavior.

## Architecture

### Two-Layer Design

```
Layer 1: HealthChecker              Layer 2: Breaker
┌─────────────────────────┐        ┌─────────────────────────┐
│ Record("metric", value) │        │ Evaluate()              │
│         │               │        │         │               │
│    MetricWindow         │───────→│    Check() violations   │
│    (sliding window)     │        │         │               │
│         │               │        │    trigger callbacks    │
│    Check() → HealthStatus│       │    return actions       │
└─────────────────────────┘        └─────────────────────────┘
```

### Health Checker (`internal/cogmetrics/health.go`)

**MetricWindow** — Sliding window per metric:
- `Add(value)` — Append value + timestamp
- `Average()` / `Last()` / `Count()` — Aggregation
- Fixed max size (100 by default) with FIFO eviction

**HealthChecker** — Thread-safe metric recording and evaluation:
- `Record(metric, value)` — Adds observation to the metric's window
- `Check()` — Evaluates all rules, returns `HealthStatus`

**HealthStatus**:
```go
type HealthStatus struct {
    Score       float64              // 0.0 (critical) to 1.0 (healthy)
    Indicators  map[string]float64   // latest value per metric
    Violations  []Violation          // active threshold violations
    LastChecked time.Time
}
```

Score starts at 1.0 and is reduced by each violation's `Severity` weight.

### Circuit Breaker (`internal/cogmetrics/breaker.go`)

**6 Intervention Actions**:

| Action | Trigger | Effect |
|--------|---------|--------|
| `ActionTriggerCompression` | Context > 85% full | Force compression pipeline |
| `ActionDegradeToSimple` | 3+ consecutive replans | Switch from cognitive to simple mode |
| `ActionPauseAndAskUser` | 50%+ tool failure rate | Stop execution, request user input |
| `ActionSwitchModel` | Confidence avg < 0.3 | Try a different/smaller model |
| `ActionDegradeToSyncWrite` | Memory write > 30s | Switch async writes to synchronous |
| `ActionDisableEvolution` | 20%+ hook timeouts | Turn off evolution hooks |

**Health Rules**:
```go
type HealthRule struct {
    Metric     string         // metric name to monitor
    Threshold  float64        // threshold value
    Action     BreakerAction  // intervention when exceeded
    Severity   float64        // health score reduction (0.0-1.0)
    UseAverage bool           // use window average vs last value
    MinSamples int            // minimum observations before rule activates
}
```

**Inverse thresholds**: For confidence metrics, "exceeding" means value is *below* threshold (low confidence = bad). The `Exceeds()` method handles this automatically based on metric name.

**MinSamples**: Rules with `MinSamples > 0` are inactive until enough data has been collected, preventing false alarms during startup.

**Breaker**:
```go
breaker := NewBreaker(checker)
breaker.OnAction(ActionTriggerCompression, func() {
    contextManager.ReactiveCompress(ctx, sess, prompt)
})
breaker.OnAction(ActionPauseAndAskUser, func() {
    channel.RequestUserIntervention("High tool failure rate detected")
})

// Called each cognitive phase:
actions := breaker.Evaluate()
```

### Default Rules

```go
DefaultHealthRules() returns:
- context_utilization   > 0.85  → TriggerCompression    (severity: 0.15)
- consecutive_replans   > 3     → DegradeToSimple       (severity: 0.25)
- tool_failure_rate     > 0.5   → PauseAndAskUser       (severity: 0.3, avg, min 5)
- reflect_confidence    < 0.3   → SwitchModel           (severity: 0.2, avg, min 3)
- memory_write_latency  > 30s   → DegradeToSyncWrite    (severity: 0.1)
- evolution_timeout_rate > 0.2  → DisableEvolution       (severity: 0.1, avg, min 10)
```

## Files

| File | Lines | Description |
|---|---|---|
| `internal/cogmetrics/health.go` | 143 | MetricWindow + HealthChecker + HealthStatus |
| `internal/cogmetrics/breaker.go` | 168 | BreakerAction + HealthRule + Breaker + DefaultRules |
| `internal/cogmetrics/health_test.go` | 157 | 8 tests: window, checker, violations, inverse thresholds |
| `internal/cogmetrics/breaker_test.go` | 142 | 8 tests: callbacks, multi-violations, score, defaults |

## Testing

```bash
go test ./internal/cogmetrics/...
# 25 tests pass (9 existing + 16 new)
```

## Integration Guide

```go
// In cognitive agent initialization:
checker := cogmetrics.NewHealthChecker()
breaker := cogmetrics.NewBreaker(checker)

// Register intervention callbacks
breaker.OnAction(cogmetrics.ActionTriggerCompression, func() { ... })
breaker.OnAction(cogmetrics.ActionPauseAndAskUser, func() { ... })

// During cognitive loop — record metrics:
checker.Record("context_utilization", contextManager.Utilization(sess, prompt))
checker.Record("reflect_confidence", reflection.OverallConfidence)
checker.Record("tool_failure_rate", float64(failures)/float64(total))

// Before each phase — evaluate health:
actions := breaker.Evaluate()
if len(actions) > 0 {
    slog.Warn("coghealth: breaker triggered", "actions", actions)
}
```
