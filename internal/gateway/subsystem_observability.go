package gateway

import (
	"context"
)

// ObservabilitySubsystem manages OpenTelemetry tracing/metrics shutdown.
type ObservabilitySubsystem struct {
	obsShutdown func(context.Context)
}

func (os *ObservabilitySubsystem) Name() string { return "observability" }

// Start is a no-op — observability is initialized during New().
func (os *ObservabilitySubsystem) Start(_ context.Context) error { return nil }

// Stop calls the observability shutdown function.
func (os *ObservabilitySubsystem) Stop(ctx context.Context) error {
	if os.obsShutdown != nil {
		os.obsShutdown(ctx)
	}
	return nil
}
