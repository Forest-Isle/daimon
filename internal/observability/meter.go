package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var (
	instrumentsMu sync.Mutex

	LLMRequestDuration      metric.Int64Histogram
	LLMTokensTotal          metric.Int64Counter
	ToolExecutionDuration   metric.Int64Histogram
	CognitivePhasesDuration metric.Int64Histogram
	SubAgentSpawns          metric.Int64Counter
	ActiveSessions          metric.Int64UpDownCounter
)

func init() {
	mustInitInstruments(metricnoop.NewMeterProvider().Meter(defaultLibraryName))
}

// InitMeter initializes the global meter provider and shared instruments.
func InitMeter(cfg Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		mustInitInstruments(otel.Meter(defaultLibraryName))
		return noopShutdown, nil
	}

	cfg = cfg.normalized()

	res, err := newResource(cfg.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	exporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)
	mustInitInstruments(mp.Meter(defaultLibraryName))
	return mp.Shutdown, nil
}

// Meter returns a global meter from the configured provider.
func Meter(name string) metric.Meter {
	if name == "" {
		name = defaultLibraryName
	}
	return otel.Meter(name)
}

func mustInitInstruments(m metric.Meter) {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()

	var err error

	LLMRequestDuration, err = m.Int64Histogram(
		"llm.request.duration",
		metric.WithDescription("LLM request latency."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		slog.Warn("observability: create llm.request.duration failed", "err", err)
	}

	LLMTokensTotal, err = m.Int64Counter(
		"llm.tokens.total",
		metric.WithDescription("Total LLM tokens by type."),
	)
	if err != nil {
		slog.Warn("observability: create llm.tokens.total failed", "err", err)
	}

	ToolExecutionDuration, err = m.Int64Histogram(
		"tool.execution.duration",
		metric.WithDescription("Tool execution latency."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		slog.Warn("observability: create tool.execution.duration failed", "err", err)
	}

	CognitivePhasesDuration, err = m.Int64Histogram(
		"cognitive.phases.duration",
		metric.WithDescription("Cognitive phase latency."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		slog.Warn("observability: create cognitive.phases.duration failed", "err", err)
	}

	SubAgentSpawns, err = m.Int64Counter(
		"subagent.spawns",
		metric.WithDescription("Sub-agent spawn attempts."),
	)
	if err != nil {
		slog.Warn("observability: create subagent.spawns failed", "err", err)
	}

	ActiveSessions, err = m.Int64UpDownCounter(
		"active.sessions",
		metric.WithDescription("Currently active sessions."),
	)
	if err != nil {
		slog.Warn("observability: create active.sessions failed", "err", err)
	}
}
