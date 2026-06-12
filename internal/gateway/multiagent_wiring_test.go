package gateway

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGatewayRegistersConfiguredAgentTools(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agents.Enabled = true
	cfg.Agents.Definitions = []config.AgentDefinition{
		{
			Name:          "reviewer",
			Description:   "Reviews changes for correctness.",
			SystemPrompt:  "Review the requested change.",
			MaxIterations: 1,
			Tools:         []string{"plan"},
		},
	}

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	_, err = gw.toolSub.Registry.Get("agent_reviewer")
	require.NoError(t, err, "configured agent spec should register an agent_* tool")

	_, err = gw.toolSub.Registry.Get("workflow")
	require.NoError(t, err, "multi-agent runtime should register the workflow orchestration tool")
}
