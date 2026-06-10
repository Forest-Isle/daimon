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
	cfg.Agent.MaxIterations = 10
	cfg.Agent.Execution.MaxParallelTools = 3
	cfg.Agent.Execution.ApprovalTimeoutSeconds = 120
	cfg.Store.Path = filepath.Join(tmp, "test.db")

	// Ensure user home directory exists
	homeDir := filepath.Join(tmp, "home")
	_ = os.MkdirAll(homeDir, 0o755)

	return &cfg
}

func TestCognitiveAgentAlwaysInitialized(t *testing.T) {
	cfg := testConfig(t)

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	assert.NotNil(t, gw.agent, "agent must be initialized")
}
