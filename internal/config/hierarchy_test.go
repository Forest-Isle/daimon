package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverSources(t *testing.T) {
	workDir := t.TempDir()

	// Create project-level config
	ironcDir := filepath.Join(workDir, ".ironclaw")
	require.NoError(t, os.MkdirAll(ironcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ironcDir, "ironclaw.yaml"), []byte("llm:\n  model: test\n"), 0o644))

	sources := discoverSources(workDir)
	assert.Len(t, sources, 4)
	assert.Equal(t, LevelSystem, sources[0].Level)
	assert.Equal(t, LevelUser, sources[1].Level)
	assert.Equal(t, LevelProject, sources[2].Level)
	assert.Equal(t, LevelLocal, sources[3].Level)

	// Only project-level should be found in temp dir
	assert.True(t, sources[2].Found, "project-level config should be found")
	assert.False(t, sources[3].Found, "local-level config should not be found")
}

func TestLoadHierarchy_MergesPriority(t *testing.T) {
	workDir := t.TempDir()
	ironcDir := filepath.Join(workDir, ".ironclaw")
	require.NoError(t, os.MkdirAll(ironcDir, 0o755))

	// Project config — sets model and api_key
	projectCfg := `
llm:
  api_key: "project-key"
  model: "project-model"
  max_tokens: 4096
agent:
  max_iterations: 10
`
	require.NoError(t, os.WriteFile(filepath.Join(ironcDir, "ironclaw.yaml"), []byte(projectCfg), 0o644))

	// Local config — overrides model only
	localCfg := `
llm:
  model: "local-model"
log:
  level: "debug"
`
	require.NoError(t, os.WriteFile(filepath.Join(ironcDir, "local.yaml"), []byte(localCfg), 0o644))

	cfg, sources, err := LoadHierarchy(workDir)
	require.NoError(t, err)

	// Local overrides project model
	assert.Equal(t, "local-model", cfg.LLM.Model)
	// Project api_key preserved (local didn't set it)
	assert.Equal(t, "project-key", cfg.LLM.APIKey)
	// Project max_tokens preserved
	assert.Equal(t, 4096, cfg.LLM.MaxTokens)
	// Project agent iterations preserved
	assert.Equal(t, 10, cfg.Agent.MaxIterations)
	// Local log level applied
	assert.Equal(t, "debug", cfg.Log.Level)

	// Check sources
	foundCount := 0
	for _, s := range sources {
		if s.Found {
			foundCount++
		}
	}
	assert.Equal(t, 2, foundCount)
}

func TestLoadHierarchy_NoConfigs(t *testing.T) {
	workDir := t.TempDir()

	cfg, sources, err := LoadHierarchy(workDir)
	require.NoError(t, err)
	assert.NotNil(t, cfg)

	// Should return defaults
	assert.Equal(t, "claude", cfg.LLM.Provider)
	assert.Equal(t, 20, cfg.Agent.MaxIterations)

	// No sources found
	for _, s := range sources {
		assert.False(t, s.Found)
	}
}

func TestMergeConfig_NonZeroOverride(t *testing.T) {
	base := defaultConfig()
	overlay := &Config{
		LLM: LLMConfig{
			Provider:  "openai",
			Model:     "gpt-4",
			MaxTokens: 16384,
		},
		Agent: AgentConfig{
			Mode: "cognitive",
		},
		Log: LogConfig{
			Level: "debug",
		},
	}

	mergeConfig(&base, overlay)

	assert.Equal(t, "openai", base.LLM.Provider)
	assert.Equal(t, "gpt-4", base.LLM.Model)
	assert.Equal(t, 16384, base.LLM.MaxTokens)
	assert.Equal(t, "cognitive", base.Agent.Mode)
	assert.Equal(t, "debug", base.Log.Level)
	// Unchanged defaults
	assert.Equal(t, 20, base.Agent.MaxIterations)
	assert.Equal(t, "text", base.Log.Format)
}

func TestMergeConfig_MCPServersAppend(t *testing.T) {
	base := defaultConfig()
	base.Tools.MCP.Servers = map[string]MCPServerConfig{
		"existing": {Command: "existing-cmd"},
	}

	overlay := &Config{}
	overlay.Tools.MCP.Servers = map[string]MCPServerConfig{
		"new": {Command: "new-cmd"},
	}

	mergeConfig(&base, overlay)

	assert.Len(t, base.Tools.MCP.Servers, 2)
	assert.Equal(t, "existing-cmd", base.Tools.MCP.Servers["existing"].Command)
	assert.Equal(t, "new-cmd", base.Tools.MCP.Servers["new"].Command)
}

func TestMergeConfig_NilOverlay(t *testing.T) {
	base := defaultConfig()
	original := base.LLM.Model
	mergeConfig(&base, nil)
	assert.Equal(t, original, base.LLM.Model)
}

func TestMergeConfig_SlicesAppend(t *testing.T) {
	base := defaultConfig()
	base.Tools.Bash.BlockedCommands = []string{"rm"}
	base.Sandbox.AllowedDirectories = []string{"/home"}

	overlay := &Config{}
	overlay.Tools.Bash.BlockedCommands = []string{"dd"}
	overlay.Sandbox.AllowedDirectories = []string{"/tmp"}

	mergeConfig(&base, overlay)

	assert.Equal(t, []string{"rm", "dd"}, base.Tools.Bash.BlockedCommands)
	assert.Equal(t, []string{"/home", "/tmp"}, base.Sandbox.AllowedDirectories)
}

func TestMergeConfig_PermissionsDenyFirst(t *testing.T) {
	base := defaultConfig()
	base.Permissions.Rules = []PermissionRule{
		{Tool: "bash", Action: "allow"},
	}

	overlay := &Config{}
	overlay.Permissions.Rules = []PermissionRule{
		{Tool: "bash", Action: "deny"},
	}

	mergeConfig(&base, overlay)

	require.Len(t, base.Permissions.Rules, 1)
	assert.Equal(t, "deny", base.Permissions.Rules[0].Action)
}

func TestConfigLevel_String(t *testing.T) {
	assert.Equal(t, "system", LevelSystem.String())
	assert.Equal(t, "user", LevelUser.String())
	assert.Equal(t, "project", LevelProject.String())
	assert.Equal(t, "local", LevelLocal.String())
	assert.Equal(t, "unknown", ConfigLevel(99).String())
}

func TestLoadSingleYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")

	content := `
llm:
  provider: "test-provider"
  model: "test-model"
agent:
  max_iterations: 50
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := loadSingleYAML(path)
	require.NoError(t, err)
	assert.Equal(t, "test-provider", cfg.LLM.Provider)
	assert.Equal(t, "test-model", cfg.LLM.Model)
	assert.Equal(t, 50, cfg.Agent.MaxIterations)
}

func TestLoadSingleYAML_EnvExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")

	t.Setenv("TEST_API_KEY", "secret-key-123")
	content := `
llm:
  api_key: "${TEST_API_KEY}"
  model: "test"
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := loadSingleYAML(path)
	require.NoError(t, err)
	assert.Equal(t, "secret-key-123", cfg.LLM.APIKey)
}

func TestLoadSingleYAML_NotFound(t *testing.T) {
	_, err := loadSingleYAML("/nonexistent/path.yaml")
	assert.Error(t, err)
}
