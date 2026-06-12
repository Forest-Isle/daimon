package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/telemetry"
)

type TelemetrySubsystem struct {
	Exporter           *telemetry.JSONLExporter
	ReplayRecorder     *telemetry.ReplayRecorder
	traceSubscription  agent.Subscription
	replaySubscription agent.Subscription
}

func (ts *TelemetrySubsystem) Name() string { return "telemetry" }

func (ts *TelemetrySubsystem) Start(_ context.Context) error { return nil }

func (ts *TelemetrySubsystem) Stop(_ context.Context) error {
	if ts == nil {
		return nil
	}
	if ts.traceSubscription != nil {
		ts.traceSubscription.Unsubscribe()
	}
	if ts.replaySubscription != nil {
		ts.replaySubscription.Unsubscribe()
	}
	var closeErr error
	if ts.Exporter != nil {
		closeErr = ts.Exporter.Close()
	}
	if ts.ReplayRecorder != nil {
		if err := ts.ReplayRecorder.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func InitTelemetry(cfg *config.Config, bus agent.EventBus) *TelemetrySubsystem {
	ts := &TelemetrySubsystem{}
	if cfg == nil {
		return ts
	}
	if cfg.Telemetry.Enabled {
		exporter, err := telemetry.NewJSONLExporter(cfg.Telemetry.TracePath)
		if err != nil {
			slog.Warn("telemetry: local trace exporter disabled", "err", err)
		} else {
			ts.Exporter = exporter
			ts.traceSubscription = exporter.Subscribe(bus)
			slog.Info("telemetry initialized", "trace_path", cfg.Telemetry.TracePath)
		}
	}
	if cfg.Telemetry.ReplayEnabled {
		recorder, err := telemetry.NewReplayRecorder(cfg.Telemetry.ReplayDir)
		if err != nil {
			slog.Warn("telemetry: replay recorder disabled", "err", err)
		} else {
			ts.ReplayRecorder = recorder
			ts.replaySubscription = recorder.Subscribe(bus)
			slog.Info("telemetry replay recorder initialized", "replay_dir", cfg.Telemetry.ReplayDir)
		}
	}
	return ts
}
