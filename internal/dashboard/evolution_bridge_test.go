package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

func TestEvolutionBridgeToolExec(t *testing.T) {
	bus := NewBus(16)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bridge := NewEvolutionBridge(bus)

	bridge.OnToolExecuted(context.Background(), evolution.ToolExecEvent{
		SessionID:  "s1",
		ToolName:   "bash",
		Succeeded:  true,
		DurationMs: 100,
		Timestamp:  time.Now(),
	})

	select {
	case ev := <-ch:
		if ev.Type != EventToolEnd {
			t.Fatalf("type = %s, want tool.end", ev.Type)
		}
		if ev.Data["tool_name"] != "bash" {
			t.Fatalf("tool = %v, want bash", ev.Data["tool_name"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEvolutionBridgeName(t *testing.T) {
	bridge := NewEvolutionBridge(NewBus(1))
	if bridge.Name() != "dashboard_bridge" {
		t.Fatalf("name = %s, want dashboard_bridge", bridge.Name())
	}
}
