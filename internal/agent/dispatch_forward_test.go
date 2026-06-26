package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/stretchr/testify/require"
)

// capturedActivity records what the reporter saw at fire time, resolving the
// session so the test can assert the sub-agent linkage (channel + parent).
type capturedActivity struct {
	tool    string
	channel string
	parent  string
}

type capturingActivityReporter struct {
	sessions *session.Manager
	seen     []capturedActivity
}

func (r *capturingActivityReporter) ReportToolActivity(ctx context.Context, call *tool.ToolCall, _ tool.ToolActivityEvent) {
	a := capturedActivity{tool: call.ToolName}
	if s, _ := r.sessions.GetByID(ctx, call.SessionID); s != nil {
		a.channel = s.Channel
		a.parent = s.ParentSessionID
	}
	r.seen = append(r.seen, a)
}

// readThenSummaryProvider emits one `read` tool call on the first turn, then a
// success summary on the next so the sub-agent loop terminates cleanly.
type readThenSummaryProvider struct{ turns int }

func (p *readThenSummaryProvider) Complete(_ context.Context, _ mind.CompletionRequest) (*mind.CompletionResponse, error) {
	return &mind.CompletionResponse{}, nil
}
func (p *readThenSummaryProvider) Capabilities() mind.Caps { return mind.Caps{} }
func (p *readThenSummaryProvider) Stream(_ context.Context, _ mind.CompletionRequest) (mind.StreamIterator, error) {
	p.turns++
	if p.turns == 1 {
		return &fixedToolStream{toolCalls: []mind.ToolUseBlock{{ID: "c1", Name: "read", Input: `{"path":"/tmp/x"}`}}}, nil
	}
	return &fixedStreamIterator{text: "<result>\n<status>success</status>\n<summary>done</summary>\n</result>"}, nil
}

type fixedToolStream struct {
	toolCalls []mind.ToolUseBlock
	done      bool
}

func (s *fixedToolStream) Next() (mind.StreamDelta, error) {
	if s.done {
		return mind.StreamDelta{Done: true, StopReason: mind.StopEndTurn}, nil
	}
	s.done = true
	return mind.StreamDelta{ToolCalls: s.toolCalls, Done: true, StopReason: mind.StopToolUse}, nil
}
func (s *fixedToolStream) Close() {}

// TestSubAgentToolActivityIsParentLinked proves the forwarding chain end-to-end
// on the agent side: a sub-agent's tool call fires ReportToolActivity under a
// session whose Channel is "subagent" and whose ParentSessionID points back to
// the spawning parent. The gateway reporter test then proves that such an event
// is forwarded to the parent channel at Depth>=1.
func TestSubAgentToolActivityIsParentLinked(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "forward.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	sessions := session.NewManager(db)
	tools := tool.NewRegistry()
	tools.Register(&testReadTool{})

	rep := &capturingActivityReporter{sessions: sessions}

	deps := AgentDeps{
		Core: CoreDeps{
			Provider: &readThenSummaryProvider{},
			Sessions: sessions,
			DB:       db,
			Tools:    tools,
			Cfg:      config.AgentConfig{MaxIterations: 3},
			LLMCfg:   config.LLMConfig{Model: "m", MaxTokens: 100},
		},
		Security: SecurityDeps{
			Interceptor: tool.NewInterceptorChain([]tool.ToolInterceptor{tool.NewActivityInterceptor(rep)}),
		},
	}.WithDefaults()

	mgr := NewSubAgentManager(deps)
	spec := &AgentSpec{Name: "researcher", Description: "r", Tools: []string{"read"}}
	require.NoError(t, spec.Validate())

	_, err = mgr.Spawn(context.Background(), SpawnRequest{
		Spec: spec, Task: "investigate", ParentSessionID: "parent-sess",
	})
	require.NoError(t, err)

	var linked bool
	for _, a := range rep.seen {
		if a.tool == "read" && a.channel == "subagent" && a.parent == "parent-sess" {
			linked = true
		}
	}
	require.True(t, linked, "a sub-agent tool call must fire under a subagent session linked to the parent; saw %+v", rep.seen)
}
