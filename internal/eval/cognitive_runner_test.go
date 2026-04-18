package eval

import (
	"context"
	"testing"

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
