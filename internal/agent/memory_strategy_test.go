package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type strategyRecordingStore struct {
	saved []memory.Entry
}

func (s *strategyRecordingStore) Save(_ context.Context, entry memory.Entry) error {
	s.saved = append(s.saved, entry)
	return nil
}

func (s *strategyRecordingStore) Search(context.Context, memory.SearchQuery) ([]memory.SearchResult, error) {
	return nil, nil
}

func (s *strategyRecordingStore) ListByScope(context.Context, memory.MemoryScope, string) ([]memory.Entry, error) {
	return nil, nil
}

func (s *strategyRecordingStore) Update(context.Context, string, string, int) error { return nil }
func (s *strategyRecordingStore) Delete(context.Context, string) error              { return nil }

func TestRecordVerifiedStrategyWritesProceduralMemory(t *testing.T) {
	store := &strategyRecordingStore{}
	cortex := memory.NewUnifiedRetriever(store, memory.NewProceduralStore(store, nil), nil)
	deps := AgentDeps{
		Core: CoreDeps{Tools: tool.NewRegistry()},
		Memory: MemoryDeps{
			Store:  store,
			Cortex: cortex,
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	sess := &session.Session{ID: "sess_strategy", Metadata: map[string]string{}}
	sess.SetMetadata("verified_tool_success", "true")
	sess.AddMessage(session.Message{ID: "use_1", Role: "tool_use", ToolName: "file_edit"})
	sess.AddMessage(session.Message{ID: "use_2", Role: "tool_use", ToolName: "test_run"})

	a.recordVerifiedStrategy(context.Background(), sess, channel.InboundMessage{
		Channel: "tui",
		UserID:  "user_1",
		Text:    "fix the failing test",
	})

	if len(store.saved) != 1 {
		t.Fatalf("saved procedural memories = %d, want 1", len(store.saved))
	}
	entry := store.saved[0]
	if entry.Metadata["type"] != "procedural" || entry.Metadata["category"] != "strategy" {
		t.Fatalf("unexpected metadata: %#v", entry.Metadata)
	}
	var record memory.StrategyRecord
	if err := json.Unmarshal([]byte(entry.Content), &record); err != nil {
		t.Fatalf("unmarshal strategy: %v", err)
	}
	if record.TaskPattern != "fix the failing test" {
		t.Fatalf("TaskPattern = %q", record.TaskPattern)
	}
	if len(record.ToolSequence) != 2 || record.ToolSequence[0] != "file_edit" || record.ToolSequence[1] != "test_run" {
		t.Fatalf("ToolSequence = %#v", record.ToolSequence)
	}
}
