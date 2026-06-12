package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
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

func TestEmergencyTruncator_HardCut(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 30; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	// 30 messages > SoftKeepTurns*4 (20) -> hard cut: keep HardKeepTurns*2=6 + 1 notice = 7
	layer := NewEmergencyTruncator(EmergencyTruncateConfig{SoftKeepTurns: 5, HardKeepTurns: 3})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if len(history) > 12 {
		t.Errorf("expected <= 12 messages after truncation, got %d", len(history))
	}
	if !strings.Contains(history[0].Content, "trimmed") {
		t.Error("first message should be truncation notice")
	}
}

func TestToolOutputReducer_Large(t *testing.T) {
	sess := newTestSession()
	bigContent := strings.Repeat("x", 10000)
	sess.AddMessage(session.Message{ID: "tool_1", Role: "tool_result", Content: bigContent})
	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("recent_%d", i),
			Role:    role,
			Content: fmt.Sprintf("recent message %d", i),
		})
	}

	layer := NewToolOutputReducer(nil, ToolOutputReduceConfig{
		TruncateChars: 2000,
		EvictBytes:    5000,
		KeepLastTurns: 4,
	})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	for _, m := range history {
		if m.ID == "tool_1" {
			if len(m.Content) >= len(bigContent) {
				t.Error("tool result should have been truncated")
			}
			if !strings.Contains(m.Content, "truncated") {
				t.Error("truncated result should contain truncation marker")
			}
		}
	}
}

func TestToolOutputReducer_Small(t *testing.T) {
	sess := newTestSession()
	smallContent := "short result"
	sess.AddMessage(session.Message{ID: "tool_1", Role: "tool_result", Content: smallContent})
	for i := 0; i < 8; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("recent_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("recent message %d", i),
		})
	}

	layer := NewToolOutputReducer(nil, ToolOutputReduceConfig{
		TruncateChars: 2000,
		EvictBytes:    5000,
		KeepLastTurns: 4,
	})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	for _, m := range history {
		if m.ID == "tool_1" && m.Content != smallContent {
			t.Error("small tool result should not be modified")
		}
	}
}

func TestEmergencyTruncator_SoftTrim(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 12; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	// 12 messages <= SoftKeepTurns*4 (12), so soft: keep SoftKeepTurns*2=6 + 1 = 7
	layer := NewEmergencyTruncator(EmergencyTruncateConfig{SoftKeepTurns: 3, HardKeepTurns: 2})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if len(history) != 7 {
		t.Errorf("expected 7 messages, got %d", len(history))
	}
	if !strings.Contains(history[0].Content, "trimmed") {
		t.Error("first message should be trim notice")
	}
}

func TestEmergencyTruncator_SkipsShort(t *testing.T) {
	sess := newTestSession()
	for i := 0; i < 5; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("message %d", i),
		})
	}

	layer := NewEmergencyTruncator(EmergencyTruncateConfig{SoftKeepTurns: 5, HardKeepTurns: 3})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if len(history) != 5 {
		t.Errorf("expected 5 messages unchanged, got %d", len(history))
	}
}

func TestPipelineEarlyExit(t *testing.T) {
	cfg := config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolOutputReducePct: 1,  // very low threshold — always runs
			SummarizePct:        99, // very high — should not run
			EmergencyPct:        99,
		},
		TokenEstimateRatio: 0.25,
	}

	pipeline := NewCompressionPipeline(nil, "test", cfg, nil, 200000)
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "1", Role: "user", Content: "hello"})

	err := pipeline.Run(context.Background(), sess, "short prompt")
	if err != nil {
		t.Fatalf("Pipeline.Run() error: %v", err)
	}
}

func TestEnsureToolPairing_OrphanResult(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "hello"})
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
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash", ToolInput: `{"command":"ls"}`})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "done"})

	ensureToolPairing(sess)

	history := sess.History()
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
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash"})
	sess.AddMessage(session.Message{ID: "use_2", Role: "tool_use", ToolName: "file_read"})
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: "output1", ToolName: "use_1"})

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

func TestToolOutputReducer_OldToolOutput(t *testing.T) {
	sess := newTestSession()
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "do something"})
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "bash"})
	bigOutput := strings.Repeat("x", 3000)
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: bigOutput, ToolName: "use_1"})
	sess.AddMessage(session.Message{ID: "msg_2", Role: "assistant", Content: "here's the result"})
	for i := 0; i < 16; i++ {
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("recent_%d", i),
			Role:    "user",
			Content: fmt.Sprintf("recent message %d", i),
		})
	}

	layer := NewToolOutputReducer(nil, ToolOutputReduceConfig{
		TruncateChars: 2000,
		EvictBytes:    5000,
		KeepLastTurns: 4,
	})
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
		}
	}
	if !found {
		t.Error("tool result message not found in history")
	}
}

func TestCompressionPipeline_RunForced(t *testing.T) {
	cfg := config.CompressionConfig{
		Strategy: "layered",
		Layers: config.CompressionLayers{
			ToolOutputReducePct: 99,
			SummarizePct:        99,
			EmergencyPct:        99,
		},
		TokenEstimateRatio: 0.25,
	}

	pipeline := NewCompressionPipeline(&stubProvider{}, "test", cfg, nil, 10_000_000)

	sess := newTestSession()
	for i := 0; i < 60; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sess.AddMessage(session.Message{
			ID:      fmt.Sprintf("msg_%d", i),
			Role:    role,
			Content: fmt.Sprintf("message %d content", i),
		})
	}
	sess.AddMessage(session.Message{ID: "orphan_use", Role: "tool_use", ToolName: "bash", ToolInput: `{"command":"ls"}`})

	beforeRun := len(sess.History())
	_ = pipeline.Run(context.Background(), sess, "prompt")
	if len(sess.History()) < beforeRun {
		t.Fatal("Run() should not have compressed with 99% thresholds and huge context window")
	}

	err := pipeline.RunForced(context.Background(), sess, "prompt")
	if err != nil {
		t.Fatalf("RunForced() error: %v", err)
	}

	history := sess.History()
	if len(history) >= 60 {
		t.Errorf("RunForced() should have reduced history, still has %d messages", len(history))
	}

	for _, m := range history {
		if m.Role == "tool_use" && m.ID == "orphan_use" {
			found := false
			for _, m2 := range history {
				if m2.Role == "tool_result" && m2.ToolName == "orphan_use" {
					found = true
					break
				}
			}
			if !found {
				t.Error("ensureToolPairing should have inserted a stub for orphan_use")
			}
		}
	}
}

func TestToolOutputReducer_ProtectsRecent(t *testing.T) {
	sess := newTestSession()
	bigOutput := strings.Repeat("y", 3000)
	sess.AddMessage(session.Message{ID: "msg_1", Role: "user", Content: "recent"})
	sess.AddMessage(session.Message{ID: "result_1", Role: "tool_result", Content: bigOutput, ToolName: "use_1"})

	layer := NewToolOutputReducer(nil, ToolOutputReduceConfig{
		TruncateChars: 2000,
		EvictBytes:    5000,
		KeepLastTurns: 4,
	})
	err := layer.Compress(context.Background(), sess, "")
	if err != nil {
		t.Fatalf("Compress() error: %v", err)
	}

	history := sess.History()
	if history[1].Content != bigOutput {
		t.Error("recent tool result should NOT be truncated")
	}
}
