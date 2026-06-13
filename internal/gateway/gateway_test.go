package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
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
	t.Setenv("HOME", homeDir)

	return &cfg
}

func TestCognitiveAgentAlwaysInitialized(t *testing.T) {
	cfg := testConfig(t)

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	assert.NotNil(t, gw.agent, "agent must be initialized")
}

func TestToolSearchRegisteredAsCoreTool(t *testing.T) {
	cfg := testConfig(t)

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	require.NotNil(t, gw.toolSub.DeferredCatalog)
	_, err = gw.toolSub.Registry.Get("tool_search")
	require.NoError(t, err)

	defs := gw.agent.GetTools().All()
	found := false
	for _, def := range defs {
		if def.Name() == "tool_search" {
			found = true
			break
		}
	}
	assert.True(t, found, "tool_search must be exposed eagerly")
}

func TestWorldToolsRegisteredAsCoreTools(t *testing.T) {
	cfg := testConfig(t)

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	for _, name := range []string{"world_read", "commitment", "world_edit"} {
		_, err := gw.toolSub.Registry.Get(name)
		require.NoError(t, err, "%s must be registered", name)
	}
}

// TestHeartEnabledRequiresEpisode pins the config invariant: the heart routes
// events into episodes, so enabling it without the episode kernel is a broken
// loop and must be rejected at construction rather than failing per-event.
func TestHeartEnabledRequiresEpisode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agent.HeartEnabled = true
	cfg.Agent.EpisodeEnabled = false

	gw, err := New(cfg)
	if gw != nil && gw.db != nil {
		defer func() { _ = gw.db.Close() }()
	}
	require.Error(t, err, "heart_enabled without episode_enabled must fail construction")
	assert.Contains(t, err.Error(), "episode_enabled")
}
