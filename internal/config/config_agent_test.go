package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestHeartConfigParses verifies the heart yaml tags round-trip. A rename or typo
// in a tag would silently disable the autonomous event path, so pin them.
func TestHeartConfigParses(t *testing.T) {
	const y = `
heart_enabled: true
heart:
  heartbeat_interval_minutes: 5
  sleep_interval_minutes: 1440
  sleep_idle_minutes: 30
  fs_watch_dirs:
    - ${HOME}/watched
  model_router: true
  chat_through_heart: true
  reflexes:
    mail_digest:
      workflow_path: ${HOME}/.daimon/reflexes/mail-digest.yaml
      timeout_seconds: 45
`
	var ac AgentConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &ac))
	assert.True(t, ac.HeartEnabled)
	assert.Equal(t, 5, ac.Heart.HeartbeatIntervalMinutes)
	assert.Equal(t, 1440, ac.Heart.SleepIntervalMinutes)
	assert.Equal(t, 30, ac.Heart.SleepIdleMinutes)
	assert.Equal(t, []string{"${HOME}/watched"}, ac.Heart.FSWatchDirs)
	assert.True(t, ac.Heart.ModelRouter)
	assert.True(t, ac.Heart.ChatThroughHeart)
	require.Contains(t, ac.Heart.Reflexes, "mail_digest")
	assert.Equal(t, "${HOME}/.daimon/reflexes/mail-digest.yaml", ac.Heart.Reflexes["mail_digest"].WorkflowPath)
	assert.Equal(t, 45, ac.Heart.Reflexes["mail_digest"].TimeoutSeconds)
}

// TestHeartConfigDefaultsOff verifies the zero value keeps the autonomous path
// disabled — omitting the heart block must never turn it on.
func TestHeartConfigDefaultsOff(t *testing.T) {
	var ac AgentConfig
	require.NoError(t, yaml.Unmarshal([]byte("max_iterations: 20\n"), &ac))
	assert.False(t, ac.HeartEnabled)
	assert.Equal(t, 0, ac.Heart.HeartbeatIntervalMinutes)
	assert.Equal(t, 0, ac.Heart.SleepIntervalMinutes)
	assert.Equal(t, 0, ac.Heart.SleepIdleMinutes)
	assert.Empty(t, ac.Heart.FSWatchDirs)
	assert.False(t, ac.Heart.ModelRouter)
	assert.False(t, ac.Heart.ChatThroughHeart)
	assert.Empty(t, ac.Heart.Reflexes)
}

func TestActionConfigParses(t *testing.T) {
	const y = `
action:
  hold_enabled: true
  hold_window_seconds: 90
  hold_drain_interval_seconds: 10
`
	var ac AgentConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &ac))
	assert.True(t, ac.Action.HoldEnabled)
	assert.Equal(t, 90, ac.Action.HoldWindowSeconds)
	assert.Equal(t, 10, ac.Action.HoldDrainIntervalSeconds)
}

func TestDefaultConfigActionHoldOff(t *testing.T) {
	cfg := defaultConfig()
	assert.False(t, cfg.Agent.Action.HoldEnabled)
	assert.Equal(t, 120, cfg.Agent.Action.HoldWindowSeconds)
	assert.Equal(t, 15, cfg.Agent.Action.HoldDrainIntervalSeconds)
}

func TestDefaultConfigStrictEpisodeGovernance(t *testing.T) {
	cfg := defaultConfig()
	assert.True(t, cfg.Agent.EpisodeEnabled)
	assert.True(t, cfg.Agent.SubagentEpisodeEnabled)
	assert.False(t, cfg.Agent.KernelFallbackEnabled)
}
