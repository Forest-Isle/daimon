package episode

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/world"
)

// TestSubAgentSpawnRoutesThroughRealEpisodeKernel is the slice-1.5 integration
// test for §4.3 (subagent→episode). The slice-1 unit tests exercised the wiring
// with a stub CognitiveKernel; this test wires a REAL episode.Runner as the
// sub-agent's kernel and drives a full SubAgentManager.Spawn, proving the
// end-to-end seam:
//
//  1. Spawn routes the sub-agent through Runner.Execute (the episode kernel runs);
//  2. the episode 交账's a durable Outcome into the world journal (invariant #3);
//  3. the cost is attributed to ActivityClass "subagent" (§4.11 ledger);
//  4. the episode's reply surfaces back through the capture path as the result.
//
// It lives in package episode (which already imports agent) so it can reuse the
// real episodeTestProvider/captureRecorder fakes while constructing the agent's
// exported SubAgentManager — agent does not import episode, so there is no cycle.
func TestSubAgentSpawnRoutesThroughRealEpisodeKernel(t *testing.T) {
	// A real episode Runner backed by a fake provider that drives episode_close.
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "Subagent reasoning.",
		toolCalls: []mind.ToolUseBlock{closeCall(`{"status":"done","summary":"Subagent task complete."}`)},
		usage:     mind.Usage{InputTokens: 120, OutputTokens: 30},
	}}}
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	runner := NewRunner(provider, ws, &world.Identity{Dir: t.TempDir()}, nil)
	rec := &captureRecorder{}
	runner.SetCostRecorder(rec)

	// A real SubAgentManager with the episode kernel enabled (the slice-1 flag on).
	sdb, err := store.Open(filepath.Join(t.TempDir(), "subagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	mgr := agent.NewSubAgentManager(agent.AgentDeps{
		Core: agent.CoreDeps{
			Sessions: session.NewManager(sdb),
			DB:       sdb,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())
	mgr.SetEpisodeKernel(runner, true)

	spec := &agent.AgentSpec{Name: "integration-agent", Description: "test"}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.Spawn(context.Background(), agent.SpawnRequest{Spec: spec, Task: "do the work"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// (4) The episode reply surfaced through the capture path as the result.
	if !strings.Contains(result.Output, "Subagent reasoning.") {
		t.Errorf("result.Output = %q, want episode reply surfaced via capture", result.Output)
	}

	// (2) The episode 交账'd a durable Outcome into the world journal.
	journal, err := ws.ListJournal(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes int
	var outcomeSummary string
	for _, e := range journal {
		if e.Kind == "outcome" {
			outcomes++
			outcomeSummary = e.Summary
		}
	}
	if outcomes != 1 {
		t.Fatalf("world journal outcome rows = %d, want exactly 1 (交账 did not land)", outcomes)
	}
	if outcomeSummary != "Subagent task complete." {
		t.Errorf("journal outcome summary = %q, want the episode_close summary", outcomeSummary)
	}

	// (3) The cost was attributed to the subagent activity class.
	if len(rec.costs) != 1 {
		t.Fatalf("cost rows = %d, want 1", len(rec.costs))
	}
	if got := rec.costs[0].ActivityClass; got != "subagent" {
		t.Errorf("cost activity class = %q, want subagent", got)
	}
	if got := rec.costs[0].Usage; got.InputTokens != 120 || got.OutputTokens != 30 {
		t.Errorf("cost usage = %+v, want input=120 output=30", got)
	}
}
