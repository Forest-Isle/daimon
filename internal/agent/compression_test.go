package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

func TestEstimateUtilization(t *testing.T) {
	tests := []struct {
		name          string
		totalChars    int
		ratio         float64
		contextWindow int
		expected      float64
	}{
		{"low usage", 32000, 0.25, 200000, 0.04},
		{"medium usage", 400000, 0.25, 200000, 0.5},
		{"high usage", 720000, 0.25, 200000, 0.9},
		{"full usage", 800000, 0.25, 200000, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateUtilization(tt.totalChars, tt.ratio, tt.contextWindow)
			if got != tt.expected {
				t.Errorf("EstimateUtilization() = %f, want %f", got, tt.expected)
			}
		})
	}
}

func newTestSession() *session.Session {
	return &session.Session{
		ID:       "test",
		Channel:  "test",
		Metadata: map[string]string{},
	}
}

func TestEmergencyTruncation(t *testing.T) {
	sess := newTestSession()
	// Add 30 messages
	for i := 0; i < 30; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	layer := &EmergencyTruncationLayer{keepLastTurns: 5}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	// Should keep 10 messages + 1 notice = 11
	history := sess.History()
	if len(history) > 12 {
		t.Errorf("expected <= 12 messages after truncation, got %d", len(history))
	}

	// First message should be the truncation notice
	if !strings.Contains(history[0].Content, "truncated") {
		t.Error("first message should be truncation notice")
	}
}

func TestToolEvictionLayer(t *testing.T) {
	sess := newTestSession()
	// Add a large tool result
	bigContent := strings.Repeat("x", 10000)
	sess.AddMessage(session.Message{
		ID:      "tool_1",
		Role:    "tool_result",
		Content: bigContent,
	})

	layer := &ToolEvictionLayer{thresholdBytes: 5000}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if len(history[0].Content) >= len(bigContent) {
		t.Error("tool result should have been truncated")
	}
	if !strings.Contains(history[0].Content, "TRUNCATED") {
		t.Error("truncated result should contain TRUNCATED marker")
	}
}

func TestToolEvictionLayerSkipsSmall(t *testing.T) {
	sess := newTestSession()
	smallContent := "short result"
	sess.AddMessage(session.Message{
		ID:      "tool_1",
		Role:    "tool_result",
		Content: smallContent,
	})

	layer := &ToolEvictionLayer{thresholdBytes: 5000}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if history[0].Content != smallContent {
		t.Error("small tool result should not be modified")
	}
}

func TestOldContextRemoval(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 12; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	layer := &OldContextRemovalLayer{}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	// 12 messages, remove 4 (12/3), add 1 notice = 9
	if len(history) != 9 {
		t.Errorf("expected 9 messages, got %d", len(history))
	}
	if !strings.Contains(history[0].Content, "trimmed") {
		t.Error("first message should be trim notice")
	}
}

func TestOldContextRemovalSkipsShort(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 4; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	layer := &OldContextRemovalLayer{}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	// Should not modify — 4 <= 6
	history := sess.History()
	if len(history) != 4 {
		t.Errorf("expected 4 messages unchanged, got %d", len(history))
	}
}

// TestPipelineEarlyExit verifies that the pipeline stops when utilization drops below threshold.
func TestPipelineEarlyExit(t *testing.T) {
	cfg := config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolEvictionPct: 1,  // very low threshold — always runs
			SummarizePct:    99, // very high — should not run
			SlimPromptPct:   99,
			EmergencyPct:    99,
		},
		TokenEstimateRatio: 0.25,
	}

	pipeline := NewCompressionPipeline(nil, "test", cfg, nil, 200000)

	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: "hello"})

	// This should not panic or error even though provider is nil
	// because layer 2 (which needs LLM) should be skipped due to high threshold
	err := pipeline.Run(context.Background(), sess, "short prompt")
	if err != nil {
		t.Fatalf("Pipeline.Run() error: %v", err)
	}
}

func TestEmergencyTruncationSkipsShort(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 5; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	layer := &EmergencyTruncationLayer{keepLastTurns: 5}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	// 5 messages <= 10 (5*2), should not truncate
	history := sess.History()
	if len(history) != 5 {
		t.Errorf("expected 5 messages unchanged, got %d", len(history))
	}
}
