package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

type stubProvider struct{}

func (s *stubProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	return &CompletionResponse{Text: "summary of conversation"}, nil
}

func (s *stubProvider) Stream(_ context.Context, _ CompletionRequest) (StreamIterator, error) {
	return nil, fmt.Errorf("stream not implemented")
}

func TestPipelineContextManager_Compress_BelowThreshold(t *testing.T) {
	cfg := &config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolEvictionPct: 30,
			SummarizePct:    50,
			SlimPromptPct:   70,
			EmergencyPct:    90,
		},
		TokenEstimateRatio: 0.25,
	}

	cm := NewPipelineContextManager(nil, "test-model", cfg, 200000, nil)

	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: "hello"})

	compressed, err := cm.Compress(context.Background(), sess, "short prompt")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if compressed {
		t.Error("Compress() should return false when utilization is below threshold")
	}
}

func TestPipelineContextManager_Compress_AboveThreshold(t *testing.T) {
	cfg := &config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolEvictionPct: 1, // very low threshold — always triggers
			SummarizePct:    99,
			SlimPromptPct:   99,
			EmergencyPct:    99,
		},
		TokenEstimateRatio: 0.25,
	}

	cm := NewPipelineContextManager(nil, "test-model", cfg, 200000, nil)

	sess := newTestSession()
	bigContent := strings.Repeat("x", 100000)
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: bigContent})

	compressed, err := cm.Compress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if !compressed {
		t.Error("Compress() should return true when utilization is above threshold")
	}
}

func TestPipelineContextManager_Compress_NilPipeline_Legacy(t *testing.T) {
	provider := &stubProvider{}
	cm := NewPipelineContextManager(provider, "test-model", nil, 200000, nil)

	sess := newTestSession()
	for i := 0; i < 50; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d with some content", i),
		})
	}

	compressed, err := cm.Compress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if !compressed {
		t.Error("Compress() should return true when history exceeds compaction threshold")
	}

	// CompactHistory should have reduced the history
	history := sess.History()
	if len(history) >= 50 {
		t.Errorf("expected history to be compacted, still has %d messages", len(history))
	}
}

func TestPipelineContextManager_Compress_NilPipeline_BelowThreshold(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	sess := newTestSession()
	for i := 0; i < 10; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	compressed, err := cm.Compress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if compressed {
		t.Error("Compress() should return false when history is below compaction threshold")
	}
}

func TestPipelineContextManager_Utilization_Small(t *testing.T) {
	cfg := &config.CompressionConfig{
		TokenEstimateRatio: 0.25,
	}
	cm := NewPipelineContextManager(nil, "test-model", cfg, 200000, nil)

	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: "hello"})

	util := cm.Utilization(sess, "short prompt")
	if util <= 0 {
		t.Error("Utilization() should be > 0 for non-empty session")
	}
	if util >= 0.01 {
		t.Errorf("Utilization() = %f, expected very low for small messages", util)
	}
}

func TestPipelineContextManager_Utilization_Large(t *testing.T) {
	cfg := &config.CompressionConfig{
		TokenEstimateRatio: 0.25,
	}
	cm := NewPipelineContextManager(nil, "test-model", cfg, 200000, nil)

	sess := newTestSession()
	bigContent := strings.Repeat("x", 400000)
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: bigContent})

	util := cm.Utilization(sess, "prompt")
	if util < 0.4 {
		t.Errorf("Utilization() = %f, expected >= 0.4 for large message (400k chars, ratio 0.25, window 200k)", util)
	}
}

func TestPipelineContextManager_Utilization_IncludesToolInput(t *testing.T) {
	cfg := &config.CompressionConfig{
		TokenEstimateRatio: 0.25,
	}
	cm := NewPipelineContextManager(nil, "test-model", cfg, 200000, nil)

	sess := newTestSession()
	sess.AddMessage(session.Message{
		ID:        "1",
		Role:      "tool_use",
		Content:   "x",
		ToolInput: strings.Repeat("y", 100000),
	})

	util := cm.Utilization(sess, "prompt")
	// 100000 chars ToolInput + small Content + prompt + overhead
	// ~100000 * 0.25 / 200000 = 0.125
	if util < 0.1 {
		t.Errorf("Utilization() = %f, expected >= 0.1 — ToolInput should be counted", util)
	}
}

func TestPipelineContextManager_SplitSystemPrompt_WithMarker(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	full := "You are a helpful assistant.\n<!-- DYNAMIC_CONTEXT -->\nMemory: user likes Go"
	static, dynamic := cm.SplitSystemPrompt(full)

	expectedStatic := "You are a helpful assistant.\n"
	expectedDynamic := "\nMemory: user likes Go"

	if static != expectedStatic {
		t.Errorf("static = %q, want %q", static, expectedStatic)
	}
	if dynamic != expectedDynamic {
		t.Errorf("dynamic = %q, want %q", dynamic, expectedDynamic)
	}
}

func TestPipelineContextManager_SplitSystemPrompt_WithoutMarker(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	full := "You are a helpful assistant with no dynamic context."
	static, dynamic := cm.SplitSystemPrompt(full)

	if static != full {
		t.Errorf("static = %q, want full prompt %q", static, full)
	}
	if dynamic != "" {
		t.Errorf("dynamic = %q, want empty string", dynamic)
	}
}

func TestPipelineContextManager_SplitSystemPrompt_MarkerAtStart(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	full := "<!-- DYNAMIC_CONTEXT -->\nAll dynamic content"
	static, dynamic := cm.SplitSystemPrompt(full)

	if static != "" {
		t.Errorf("static = %q, want empty string", static)
	}
	if dynamic != "\nAll dynamic content" {
		t.Errorf("dynamic = %q, want %q", dynamic, "\nAll dynamic content")
	}
}

func TestPipelineContextManager_SplitSystemPrompt_MarkerAtEnd(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	full := "Static content only\n<!-- DYNAMIC_CONTEXT -->"
	static, dynamic := cm.SplitSystemPrompt(full)

	if static != "Static content only\n" {
		t.Errorf("static = %q, want %q", static, "Static content only\n")
	}
	if dynamic != "" {
		t.Errorf("dynamic = %q, want empty string", dynamic)
	}
}

func TestPipelineContextManager_ReactiveCompress_NilPipeline(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: "hello"})

	// Without a pipeline, falls back to CompactHistory.
	// With only 1 message (below compactionThreshold=40), it should be a no-op.
	err := cm.ReactiveCompress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("ReactiveCompress() error: %v", err)
	}
}

func TestPipelineContextManager_ReactiveCompress_WithPipeline(t *testing.T) {
	cfg := &config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolEvictionPct: 99,
			SummarizePct:    99,
			SlimPromptPct:   99,
			EmergencyPct:    99,
		},
		TokenEstimateRatio: 0.25,
	}

	provider := &stubProvider{}
	cm := NewPipelineContextManager(provider, "test-model", cfg, 10_000_000, nil)

	sess := newTestSession()
	for i := 0; i < 60; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    role,
			Content: fmt.Sprintf("message %d", i),
		})
	}

	// Normal Compress should NOT trigger — utilization far below 99% thresholds.
	compressed, err := cm.Compress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}
	if compressed {
		t.Error("Compress() should not trigger with huge context window and 99% thresholds")
	}

	// ReactiveCompress should force-compress via pipeline.RunForced.
	err = cm.ReactiveCompress(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("ReactiveCompress() error: %v", err)
	}

	history := sess.History()
	if len(history) >= 60 {
		t.Errorf("ReactiveCompress should have reduced history via RunForced, still has %d messages", len(history))
	}
}

func TestPipelineContextManager_Utilization_DefaultRatio(t *testing.T) {
	cm := NewPipelineContextManager(nil, "test-model", nil, 200000, nil)

	sess := newTestSession()
	bigContent := strings.Repeat("x", 200000)
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: bigContent})

	util := cm.Utilization(sess, "")
	// 200000 chars * 0.25 / 200000 = 0.25 (plus 20 overhead per message)
	if util < 0.2 || util > 0.3 {
		t.Errorf("Utilization() = %f, expected ~0.25 with default ratio", util)
	}
}
