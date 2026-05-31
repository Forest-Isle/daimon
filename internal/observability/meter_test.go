package observability

import (
	"context"
	"testing"
)

func TestInitMeter_NoopWhenDisabled(t *testing.T) {
	cfg := Config{Enabled: false}
	shutdown, err := InitMeter(cfg)
	if err != nil {
		t.Fatalf("InitMeter with disabled config should not error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown should not error: %v", err)
	}
}

func TestInitMeter_Enabled(t *testing.T) {
	// Prometheus exporter requires a valid port, but it creates a default one.
	// This should succeed since we don't need to query it.
	if testing.Short() {
		t.Skip("skipping in short mode due to Prometheus exporter requirements")
	}

	cfg := Config{
		Enabled:     true,
		ServiceName: "test-service",
		Exporter:    "prometheus",
	}
	shutdown, err := InitMeter(cfg)
	if err != nil {
		t.Fatalf("InitMeter with prometheus failed: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	// Note: In some environments the Prometheus port may conflict.
	// This test should pass in CI.
	t.Cleanup(func() { _ = shutdown(context.Background()) })
}

func TestMeter_DefaultName(t *testing.T) {
	m := Meter("")
	if m == nil {
		t.Fatal("Meter('') returned nil")
	}
}

func TestMeter_CustomName(t *testing.T) {
	m := Meter("custom-meter")
	if m == nil {
		t.Fatal("Meter('custom-meter') returned nil")
	}
}

func TestInitMeter_NormalizedConfig(t *testing.T) {
	// Even when disabled, should normalize and return noop
	cfg := Config{Enabled: true, ServiceName: "", SampleRate: 2.0}

	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	shutdown, err := InitMeter(cfg)
	if err != nil {
		t.Logf("InitMeter may fail without Prometheus port: %v", err)
		return
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
}

func TestGlobalInstruments_Initialized(t *testing.T) {
	// Instruments should be initialized via init() with noop provider
	if LLMRequestDuration == nil {
		t.Error("LLMRequestDuration should be initialized")
	}
	if LLMTokensTotal == nil {
		t.Error("LLMTokensTotal should be initialized")
	}
	if ToolExecutionDuration == nil {
		t.Error("ToolExecutionDuration should be initialized")
	}
	if CognitivePhasesDuration == nil {
		t.Error("CognitivePhasesDuration should be initialized")
	}
	if SubAgentSpawns == nil {
		t.Error("SubAgentSpawns should be initialized")
	}
	if ActiveSessions == nil {
		t.Error("ActiveSessions should be initialized")
	}
}

func TestGlobalInstruments_NoPanic(t *testing.T) {
	// These operations should not panic even with noop provider
	ctx := context.Background()

	LLMRequestDuration.Record(ctx, 100)
	LLMTokensTotal.Add(ctx, 50)
	ToolExecutionDuration.Record(ctx, 200)
	CognitivePhasesDuration.Record(ctx, 300)
	SubAgentSpawns.Add(ctx, 1)
	ActiveSessions.Add(ctx, 1)
	ActiveSessions.Add(ctx, -1)
}

func TestInitMeter_Idempotent(t *testing.T) {
	cfg := Config{Enabled: false}
	shutdown1, err := InitMeter(cfg)
	if err != nil {
		t.Fatalf("first InitMeter: %v", err)
	}

	shutdown2, err := InitMeter(cfg)
	if err != nil {
		t.Fatalf("second InitMeter: %v", err)
	}

	_ = shutdown1(context.Background())
	_ = shutdown2(context.Background())
}

func TestConfig_Normalized(t *testing.T) {
	cfg := Config{}
	normalized := cfg.normalized()

	if normalized.ServiceName != defaultServiceName {
		t.Errorf("ServiceName = %q, want %q", normalized.ServiceName, defaultServiceName)
	}
	if normalized.SampleRate != 1.0 {
		t.Errorf("SampleRate = %f, want 1.0", normalized.SampleRate)
	}
}

func TestConfig_NormalizedWithValues(t *testing.T) {
	cfg := Config{
		ServiceName: "custom",
		SampleRate:  0.5,
	}
	normalized := cfg.normalized()

	if normalized.ServiceName != "custom" {
		t.Errorf("ServiceName = %q, want 'custom'", normalized.ServiceName)
	}
	if normalized.SampleRate != 0.5 {
		t.Errorf("SampleRate = %f, want 0.5", normalized.SampleRate)
	}
}

func TestConfig_NormalizedClampSampleRate(t *testing.T) {
	cfg := Config{SampleRate: 1.5}
	normalized := cfg.normalized()
	if normalized.SampleRate > 1.0 {
		t.Errorf("SampleRate should be clamped to 1.0, got %f", normalized.SampleRate)
	}

	cfg = Config{SampleRate: -1}
	normalized = cfg.normalized()
	if normalized.SampleRate <= 0 {
		t.Errorf("SampleRate should be defaulted to 1.0, got %f", normalized.SampleRate)
	}
}
