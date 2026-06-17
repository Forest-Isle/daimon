package episode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/mind"
)

type parallelEchoProvider struct {
	mu       sync.Mutex
	requests []mind.CompletionRequest
}

func (p *parallelEchoProvider) Complete(context.Context, mind.CompletionRequest) (*mind.CompletionResponse, error) {
	return nil, fmt.Errorf("parallelEchoProvider.Complete should not be called")
}

func (p *parallelEchoProvider) Capabilities() mind.Caps { return mind.Caps{} }

func (p *parallelEchoProvider) Stream(_ context.Context, req mind.CompletionRequest) (mind.StreamIterator, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	goal := goalFromSystem(req.System)
	payload, err := json.Marshal(Outcome{
		Status:  "done",
		Summary: "summary for " + goal,
	})
	if err != nil {
		return nil, err
	}
	return &parallelEchoStream{
		text: "reply for " + goal,
		toolCalls: []mind.ToolUseBlock{{
			ID:    "close_" + strings.ReplaceAll(goal, "-", "_"),
			Name:  episodeCloseToolName,
			Input: string(payload),
		}},
	}, nil
}

type parallelEchoStream struct {
	text      string
	toolCalls []mind.ToolUseBlock
	done      bool
}

func (s *parallelEchoStream) Next() (mind.StreamDelta, error) {
	if s.done {
		return mind.StreamDelta{Done: true}, nil
	}
	s.done = true
	return mind.StreamDelta{
		Text:       s.text,
		ToolCalls:  s.toolCalls,
		Done:       true,
		StopReason: mind.StopToolUse,
	}, nil
}

func (s *parallelEchoStream) Close() {}

func goalFromSystem(system string) string {
	const marker = "\n\n## Goal\n"
	start := strings.Index(system, marker)
	if start == -1 {
		return ""
	}
	rest := system[start+len(marker):]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func TestExecuteParallelEpisodesNoCrosstalk(t *testing.T) {
	provider := &parallelEchoProvider{}
	runner, ws := testRunner(t, provider)
	ctx := context.Background()

	const episodes = 10
	outcomes := make([]agent.CognitiveOutcome, episodes)
	errs := make([]error, episodes)

	var wg sync.WaitGroup
	wg.Add(episodes)
	for i := 0; i < episodes; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := agent.CognitiveRequest{
				SessionID: fmt.Sprintf("session-%d", i),
				EpisodeID: fmt.Sprintf("event-%d", i),
				Goal:      fmt.Sprintf("goal-%d", i),
				Trigger:   fmt.Sprintf("trigger-%d", i),
				Transcript: []mind.CompletionMessage{{
					Role:    "user",
					Content: fmt.Sprintf("trigger-%d", i),
				}},
			}
			outcomes[i], errs[i] = runner.Execute(ctx, req)
		}()
	}
	wg.Wait()

	for i := 0; i < episodes; i++ {
		goal := fmt.Sprintf("goal-%d", i)
		if errs[i] != nil {
			t.Fatalf("Execute(%s) error = %v", goal, errs[i])
		}
		if outcomes[i].Status != "done" {
			t.Fatalf("Execute(%s) status = %q, want done", goal, outcomes[i].Status)
		}
		if outcomes[i].Reply != "reply for "+goal {
			t.Fatalf("Execute(%s) reply = %q, want %q", goal, outcomes[i].Reply, "reply for "+goal)
		}
		if outcomes[i].Summary != "summary for "+goal {
			t.Fatalf("Execute(%s) summary = %q, want %q", goal, outcomes[i].Summary, "summary for "+goal)
		}
	}

	journal, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListJournal: %v", err)
	}
	seen := make(map[string]string, episodes)
	for _, e := range journal {
		if e.Kind != "outcome" {
			continue
		}
		seen[e.EpisodeID] = e.Summary
	}
	if len(seen) != episodes {
		t.Fatalf("outcome journal rows = %d, want %d: %#v", len(seen), episodes, seen)
	}
	for i := 0; i < episodes; i++ {
		episodeID := fmt.Sprintf("event-%d", i)
		want := fmt.Sprintf("summary for goal-%d", i)
		if seen[episodeID] != want {
			t.Fatalf("journal outcome for %s = %q, want %q", episodeID, seen[episodeID], want)
		}
	}
}

var _ mind.Provider = (*parallelEchoProvider)(nil)
var _ mind.StreamIterator = (*parallelEchoStream)(nil)
