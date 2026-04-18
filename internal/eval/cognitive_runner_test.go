package eval

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

func TestEvalChannel_AutoApproves(t *testing.T) {
	ch := &EvalChannel{}
	approved, err := ch.SendApprovalRequest(context.Background(), channel.MessageTarget{}, "bash", "rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("EvalChannel should auto-approve all tool calls")
	}
}

func TestEvalChannel_CapturesMessages(t *testing.T) {
	ch := &EvalChannel{}
	ctx := context.Background()

	_ = ch.Send(ctx, outMsg("hello"))
	_ = ch.Send(ctx, outMsg("world"))

	msgs := ch.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text != "hello" || msgs[1].Text != "world" {
		t.Errorf("messages = %v, want [hello, world]", msgs)
	}

	if ch.LastMessage() != "world" {
		t.Errorf("LastMessage() = %q, want %q", ch.LastMessage(), "world")
	}
}

func TestEvalChannel_Reset(t *testing.T) {
	ch := &EvalChannel{}
	_ = ch.Send(context.Background(), outMsg("test"))
	ch.Reset()

	if len(ch.Messages()) != 0 {
		t.Error("expected no messages after reset")
	}
	if ch.LastMessage() != "" {
		t.Error("expected empty last message after reset")
	}
}

func TestEvalChannel_StreamUpdater(t *testing.T) {
	ch := &EvalChannel{}
	updater, err := ch.SendStreaming(context.Background(), channel.MessageTarget{})
	if err != nil {
		t.Fatal(err)
	}

	_ = updater.Update("partial")
	_ = updater.Finish("complete message")

	if ch.LastMessage() != "complete message" {
		t.Errorf("expected Finish to capture message, got %q", ch.LastMessage())
	}
}

func TestEvalHook_CapturesEvents(t *testing.T) {
	hook := NewEvalHook()

	ref := evolution.ReflectionEvent{
		SessionID:  "sess1",
		Succeeded:  true,
		Confidence: 0.85,
		ToolsUsed:  []string{"bash", "file_write"},
		ReplanCount: 1,
	}
	ep := evolution.EpisodeEvent{
		SessionID:  "sess1",
		Succeeded:  true,
		DurationMs: 5000,
		ReplanCount: 1,
	}
	tool := evolution.ToolExecEvent{
		SessionID: "sess1",
		ToolName:  "bash",
		Succeeded: true,
	}

	hook.OnReflectionComplete(context.Background(), ref)
	hook.OnEpisodeComplete(context.Background(), ep)
	hook.OnToolExecuted(context.Background(), tool)

	gotRef := hook.GetReflection("sess1")
	if gotRef == nil {
		t.Fatal("expected reflection event")
	}
	if gotRef.Confidence != 0.85 {
		t.Errorf("confidence = %f, want 0.85", gotRef.Confidence)
	}

	gotEp := hook.GetEpisode("sess1")
	if gotEp == nil {
		t.Fatal("expected episode event")
	}
	if gotEp.DurationMs != 5000 {
		t.Errorf("duration = %d, want 5000", gotEp.DurationMs)
	}

	execs := hook.GetToolExecs("sess1")
	if len(execs) != 1 || execs[0].ToolName != "bash" {
		t.Errorf("tool execs = %v, want [{bash}]", execs)
	}
}

func TestEvalHook_ClearSession(t *testing.T) {
	hook := NewEvalHook()
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s1"})
	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{SessionID: "s1"})
	hook.OnToolExecuted(context.Background(), evolution.ToolExecEvent{SessionID: "s1"})

	hook.ClearSession("s1")

	if hook.GetReflection("s1") != nil {
		t.Error("expected nil reflection after clear")
	}
	if hook.GetEpisode("s1") != nil {
		t.Error("expected nil episode after clear")
	}
	if len(hook.GetToolExecs("s1")) != 0 {
		t.Error("expected no tool execs after clear")
	}
}

func TestEvalHook_IsolatesSessions(t *testing.T) {
	hook := NewEvalHook()
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s1", Confidence: 0.9})
	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{SessionID: "s2", Confidence: 0.4})

	if hook.GetReflection("s1").Confidence != 0.9 {
		t.Error("s1 confidence should be 0.9")
	}
	if hook.GetReflection("s2").Confidence != 0.4 {
		t.Error("s2 confidence should be 0.4")
	}
	if hook.GetReflection("s3") != nil {
		t.Error("non-existent session should return nil")
	}
}

func outMsg(text string) channel.OutboundMessage {
	return channel.OutboundMessage{Text: text}
}

func TestPopulateFromObservation(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
	}

	obs := &agent.ObservationResult{
		Assertions: []agent.AssertionResult{
			{Check: "exit_code == 0", Passed: true},
			{Check: "no stderr", Passed: true},
			{Check: "file exists", Passed: false, Actual: "file not found"},
		},
		Observations: []agent.Observation{
			{ToolName: "bash"},
			{ToolName: "file_write"},
			{ToolName: "bash"},
		},
	}

	r.mu.Lock()
	r.lastObservation = obs
	r.mu.Unlock()

	result := &EvalResult{}
	r.populateFromObservation(result)

	if result.AssertionTotal != 3 {
		t.Errorf("AssertionTotal = %d, want 3", result.AssertionTotal)
	}
	if result.AssertionPassed != 2 {
		t.Errorf("AssertionPassed = %d, want 2", result.AssertionPassed)
	}
	wantRate := 2.0 / 3.0
	if diff := result.AssertionPassRate - wantRate; diff > 0.001 || diff < -0.001 {
		t.Errorf("AssertionPassRate = %f, want ~%f", result.AssertionPassRate, wantRate)
	}
	if len(result.ToolsUsed) != 2 {
		t.Errorf("ToolsUsed = %v, want 2 unique tools", result.ToolsUsed)
	}
}

func TestPopulateFromObservation_NilObservation(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
	}

	result := &EvalResult{}
	r.populateFromObservation(result)

	if result.AssertionTotal != 0 {
		t.Errorf("AssertionTotal should be 0 when no observation, got %d", result.AssertionTotal)
	}
	if result.ToolsUsed != nil {
		t.Errorf("ToolsUsed should be nil when no observation, got %v", result.ToolsUsed)
	}
}

func TestPopulateFromEvolution(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	hook.OnReflectionComplete(context.Background(), evolution.ReflectionEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		Confidence:  0.92,
		ReplanCount: 2,
		ToolsUsed:   []string{"bash"},
	})
	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		ReplanCount: 2,
	})

	result := &EvalResult{}
	r.populateFromEvolution(result, "test-sess")

	if !result.Success {
		t.Error("Success should be true from reflection event")
	}
	if result.Confidence != 0.92 {
		t.Errorf("Confidence = %f, want 0.92", result.Confidence)
	}
	if result.ReplanCount != 2 {
		t.Errorf("ReplanCount = %d, want 2", result.ReplanCount)
	}
}

func TestPopulateFromEvolution_NoHook(t *testing.T) {
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    nil,
	}

	result := &EvalResult{Success: false, Confidence: 0}
	r.populateFromEvolution(result, "any-session")

	if result.Success {
		t.Error("Success should remain false when hook is nil")
	}
	if result.Confidence != 0 {
		t.Error("Confidence should remain 0 when hook is nil")
	}
}

func TestPopulateFromEvolution_EpisodeFallback(t *testing.T) {
	hook := NewEvalHook()
	r := &CognitiveAgentRunner{
		channel: &EvalChannel{},
		hook:    hook,
	}

	hook.OnEpisodeComplete(context.Background(), evolution.EpisodeEvent{
		SessionID:   "test-sess",
		Succeeded:   true,
		ReplanCount: 1,
	})

	result := &EvalResult{}
	r.populateFromEvolution(result, "test-sess")

	if !result.Success {
		t.Error("Success should be true from episode fallback")
	}
	if result.ReplanCount != 1 {
		t.Errorf("ReplanCount = %d, want 1", result.ReplanCount)
	}
}
