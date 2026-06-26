package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/stretchr/testify/require"
)

func TestBuildDispatchSpec_Defaults(t *testing.T) {
	s := buildDispatchSpec(dispatchToolInput{Task: "t", Prompt: "be a researcher"})
	require.Equal(t, "be a researcher", s.SystemPrompt)
	require.Equal(t, defaultDispatchTools, s.Tools)
	require.NotEmpty(t, s.Description, "description should fall back to a constant")
	require.Equal(t, DefaultMaxIterations, s.MaxIterations)
}

func TestBuildDispatchSpec_ExplicitTools(t *testing.T) {
	s := buildDispatchSpec(dispatchToolInput{Task: "t", Tools: []string{"bash"}, Description: "d"})
	require.Equal(t, []string{"bash"}, s.Tools)
	require.Equal(t, "d", s.Description)
}

func TestDispatchTool_Execute_EmptyTask(t *testing.T) {
	dt := NewDispatchTool(nil, nil)
	out, err := dt.Execute(context.Background(), []byte(`{"prompt":"x"}`))
	require.NoError(t, err)
	require.Equal(t, "task is required", out.Error)
}

func TestDispatchTool_Execute_InlineSpawns(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "dispatch.db"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "<result>\n<status>success</status>\n<summary>Found it.</summary>\n</result>"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 2},
			LLMCfg:   config.LLMConfig{Model: "m", MaxTokens: 100},
		},
	}.WithDefaults())

	dt := NewDispatchTool(mgr, nil)
	out, err := dt.Execute(context.Background(), []byte(`{"task":"find the close logic","prompt":"you are a researcher"}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)
	require.NotEmpty(t, out.Output)
}
