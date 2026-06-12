package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/workflow"
)

func TestLoopPublishesModelCallEvents(t *testing.T) {
	bus := NewEventBus()
	events := subscribeTestEvents(bus)
	sess := &session.Session{
		ID: "model-events", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
	}
	registry := tool.NewRegistry()
	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = registry
	deps.Core.Cfg.MaxIterations = 1
	deps.Core.Provider = &testProvider{text: "done"}
	deps.Core.LLMCfg.Model = "test-model"
	deps.Core.LLMCfg.Provider = "test-provider"
	a := NewAgent(&deps, &LinearLoop{}, bus)

	err := (&LinearLoop{}).Execute(context.Background(), a, &testChannel{}, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "hello",
	}, sess, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	found := waitEventTypes(t, events, "model.call.started", "model.call.ended")
	if got := found["model.call.started"]; got == nil {
		t.Fatal("missing model.call.started")
	}
	if got := found["model.call.ended"]; got == nil {
		t.Fatal("missing model.call.ended")
	}
}

func TestWorkflowToolPublishesWorkflowStepEvents(t *testing.T) {
	bus := NewEventBus()
	events := subscribeTestEvents(bus)
	observer := workflowObserver{bus: bus}
	observer.ObserveWorkflowStep(context.Background(), workflow.StepEvent{
		WorkflowName:   "wf",
		WorkflowHash:   "hash",
		StageID:        "stage",
		StepID:         "step",
		StepType:       workflow.StepTypeAgent,
		Phase:          "completed",
		Status:         workflow.StatusSuccess,
		DurationMillis: 3,
	})
	event := waitEventType(t, events, "workflow.step")
	if event == nil {
		t.Fatal("missing workflow.step event")
	}
	step, ok := event.(WorkflowStepEvent)
	if !ok {
		t.Fatalf("event type = %T", event)
	}
	if step.WorkflowName != "wf" || step.StepID != "step" || step.Status != "success" {
		t.Fatalf("workflow step event = %#v", step)
	}
}

func subscribeTestEvents(bus EventBus) <-chan Event {
	ch := make(chan Event, 16)
	bus.Subscribe(func(event Event) {
		ch <- event
	})
	return ch
}

func waitEventType(t *testing.T, events <-chan Event, eventType string) Event {
	t.Helper()
	return waitEventTypes(t, events, eventType)[eventType]
}

func waitEventTypes(t *testing.T, events <-chan Event, eventTypes ...string) map[string]Event {
	t.Helper()
	wanted := make(map[string]struct{}, len(eventTypes))
	for _, eventType := range eventTypes {
		wanted[eventType] = struct{}{}
	}
	found := make(map[string]Event, len(eventTypes))
	timeout := time.After(2 * time.Second)
	for {
		if len(found) == len(wanted) {
			return found
		}
		select {
		case event := <-events:
			if _, ok := wanted[event.EventType()]; ok {
				found[event.EventType()] = event
			}
		case <-timeout:
			return found
		}
	}
}
