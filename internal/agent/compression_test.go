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

func TestEnsureToolPairing_OrphanResult(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "hello"})
	// tool_result without a matching tool_use — orphan
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: "output", ToolName: "missing_use_id"})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "done"})

	ensureToolPairing(sess)

	history := sess.History()
	for _, m := range history {
		if m.Role == "tool_result" && m.ToolName == "missing_use_id" {
			t.Error("orphan tool_result should have been removed")
		}
	}
	if len(history) != 2 {
		t.Errorf("expected 2 messages after orphan removal, got %d", len(history))
	}
}

func TestEnsureToolPairing_MissingResult(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "hello"})
	// tool_use without a matching tool_result
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash", ToolInput: `{"command":"ls"}`})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "done"})

	ensureToolPairing(sess)

	history := sess.History()
	// Should now have: user, tool_use, tool_result (stub), assistant = 4
	if len(history) != 4 {
		t.Fatalf("expected 4 messages after stub insertion, got %d", len(history))
	}
	stub := history[2]
	if stub.Role != "tool_result" {
		t.Errorf("expected tool_result stub at index 2, got role=%s", stub.Role)
	}
	if stub.ToolName != "use_1" {
		t.Errorf("stub ToolName = %q, want %q", stub.ToolName, "use_1")
	}
	if !strings.Contains(stub.Content, "pruned") {
		t.Error("stub content should mention pruning")
	}
}

func TestEnsureToolPairing_NoChange(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "hello"})
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash"})
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: "output", ToolName: "use_1"})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "done"})

	ensureToolPairing(sess)

	history := sess.History()
	if len(history) != 4 {
		t.Errorf("expected 4 messages unchanged, got %d", len(history))
	}
}

func TestEnsureToolPairing_ConsecutiveToolUses(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "hello"})
	// Two consecutive tool_uses (parallel execution), only first has a result
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash"})
	sess.AddMessage(session.Message{ID: "use_2", Role: "tool_use", ToolName: "file_read"})
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: "output1", ToolName: "use_1"})
	// use_2 has no result (lost during compression)

	ensureToolPairing(sess)

	history := sess.History()
	foundStub := false
	for _, m := range history {
		if m.Role == "tool_result" && m.ToolName == "use_2" {
			foundStub = true
			if !strings.Contains(m.Content, "pruned") {
				t.Error("stub for use_2 should mention pruning")
			}
		}
	}
	if !foundStub {
		t.Error("expected stub tool_result for use_2")
	}
}

func TestToolOutputPrePruneLayer(t *testing.T) {
	sess := newTestSession()
	// Add old messages with large tool output
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "do something"})
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash"})
	bigOutput := strings.Repeat("x", 3000)
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: bigOutput, ToolName: "use_1"})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "here's the result"})
	// Add recent messages that should be protected
	for i := 0; i < 16; i++ { // 4 turns * 4 messages
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("recent_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("recent message %d", i),
		})
	}

	layer := &ToolOutputPrePruneLayer{thresholdChars: 2000, keepRecentTurns: 4, previewChars: 500}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	var found bool
	for _, m := range history {
		if m.ID == "result_1" {
			found = true
			if len(m.Content) >= len(bigOutput) {
				t.Error("old tool result should have been truncated")
			}
			if !strings.Contains(m.Content, "truncated") {
				t.Error("truncated content should contain truncation marker")
			}
			if !strings.Contains(m.Content, "3000") {
				t.Error("truncation marker should include original size")
			}
		}
	}
	if !found {
		t.Error("tool result message not found in history")
	}
}

func TestToolOutputPrePruneLayer_ProtectsRecent(t *testing.T) {
	sess := newTestSession()
	// Only recent messages — all should be protected
	bigOutput := strings.Repeat("y", 3000)
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "recent"})
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: bigOutput, ToolName: "use_1"})

	layer := &ToolOutputPrePruneLayer{thresholdChars: 2000, keepRecentTurns: 4, previewChars: 500}
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if history[1].Content != bigOutput {
		t.Error("recent tool result should NOT be truncated")
	}
}
