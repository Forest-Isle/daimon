package gateway

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// EvolutionSubsystem manages the self-evolution engine, cognitive metrics
// collector, health checker, and circuit breaker.
type EvolutionSubsystem struct {
	engine        *evolution.Engine
	cogCollector  *cogmetrics.Collector
	healthChecker *cogmetrics.HealthChecker
	breaker       *cogmetrics.Breaker
}

func (es *EvolutionSubsystem) Name() string { return "evolution" }

// Start is a no-op — the engine and collector are started by Gateway.Start()
// based on feature state. Cogmetrics init is idempotent.
func (es *EvolutionSubsystem) Start(_ context.Context) error { return nil }

// Stop is a no-op — evolution state persistence is handled by Gateway.Stop().
func (es *EvolutionSubsystem) Stop(_ context.Context) error { return nil }

// Engine returns the evolution engine, or nil.
func (es *EvolutionSubsystem) Engine() *evolution.Engine { return es.engine }

// Collector returns the cognitive metrics collector, or nil.
func (es *EvolutionSubsystem) Collector() *cogmetrics.Collector { return es.cogCollector }

// HealthChecker returns the cognitive health checker, or nil.
func (es *EvolutionSubsystem) HealthChecker() *cogmetrics.HealthChecker { return es.healthChecker }

// Breaker returns the circuit breaker, or nil.
func (es *EvolutionSubsystem) Breaker() *cogmetrics.Breaker { return es.breaker }
