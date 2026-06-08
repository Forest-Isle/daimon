package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	tmp := t.TempDir()
	cfg := config.Config{}
	cfg.LLM.Provider = "claude"
	cfg.LLM.APIKey = "test-key"
	cfg.LLM.Model = "claude-sonnet-4-20250514"
	cfg.LLM.MaxTokens = 4096
	cfg.Agent.Mode = "simple"
	cfg.Agent.MaxIterations = 10
	cfg.Agent.Cognitive.ConfidenceThreshold = 0.6
	cfg.Agent.Cognitive.MaxParallelTools = 3
	cfg.Agent.Cognitive.MaxReplanAttempts = 2
	cfg.Agent.Cognitive.PlanMaxTokens = 2048
	cfg.Agent.Cognitive.ReflectMaxTokens = 1024
	cfg.Agent.Cognitive.ApprovalTimeoutSeconds = 120
	cfg.Store.Path = filepath.Join(tmp, "test.db")

	// Ensure user home directory exists
	homeDir := filepath.Join(tmp, "home")
	_ = os.MkdirAll(homeDir, 0o755)

	return &cfg
}

func TestCognitiveAgentAlwaysInitialized(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agent.Mode = "simple"

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	assert.NotNil(t, gw.agent, "agent must be initialized")
}

func TestGatewaySetMode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agent.Mode = "simple"

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	assert.Equal(t, "simple", gw.CurrentMode())

	err = gw.SetMode("cognitive")
	require.NoError(t, err)
	assert.Equal(t, "cognitive", gw.CurrentMode())

	err = gw.SetMode("simple")
	require.NoError(t, err)
	assert.Equal(t, "simple", gw.CurrentMode())

	err = gw.SetMode("invalid")
	assert.Error(t, err)
	assert.Equal(t, "simple", gw.CurrentMode())
}

func TestHandleInboundRoutesByCurrentMode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agent.Mode = "simple"

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	assert.Equal(t, "simple", gw.CurrentMode())

	require.NoError(t, gw.SetMode("cognitive"))
	assert.Equal(t, "cognitive", gw.CurrentMode())
}
