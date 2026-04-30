package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/observability"
)

func initObservability(ctx context.Context, cfg config.Config) (shutdown func(context.Context), err error) {
	obsCfg := observability.Config{
		Enabled:     cfg.Observability.Enabled,
		ServiceName: cfg.Observability.ServiceName,
		Exporter:    cfg.Observability.Exporter,
		Endpoint:    cfg.Observability.Endpoint,
		SampleRate:  cfg.Observability.SampleRate,
	}

	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return nil, fmt.Errorf("init tracer: %w", err)
	}

	meterShutdown, err := observability.InitMeter(obsCfg)
	if err != nil {
		_ = tracerShutdown(ctx)
		return nil, fmt.Errorf("init meter: %w", err)
	}

	slog.Info("observability initialized", "exporter", obsCfg.Exporter, "service", obsCfg.ServiceName)

	return func(ctx context.Context) {
		if err := tracerShutdown(ctx); err != nil {
			slog.Warn("tracer shutdown error", "err", err)
		}
		if err := meterShutdown(ctx); err != nil {
			slog.Warn("meter shutdown error", "err", err)
		}
	}, nil
}
