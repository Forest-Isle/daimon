package gateway

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/ratelimit"
)

// ObservabilitySubsystem manages OpenTelemetry tracing/metrics shutdown and
// the rate limiter.
type ObservabilitySubsystem struct {
	obsShutdown func(context.Context)
	rateLimiter ratelimit.Limiter
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

// RateLimiter returns the rate limiter, or nil.
func (os *ObservabilitySubsystem) RateLimiter() ratelimit.Limiter { return os.rateLimiter }
