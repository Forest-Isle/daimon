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
  model_router: true
  chat_through_heart: true
`
	var ac AgentConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &ac))
	assert.True(t, ac.HeartEnabled)
	assert.Equal(t, 5, ac.Heart.HeartbeatIntervalMinutes)
	assert.True(t, ac.Heart.ModelRouter)
	assert.True(t, ac.Heart.ChatThroughHeart)
}

// TestHeartConfigDefaultsOff verifies the zero value keeps the autonomous path
// disabled — omitting the heart block must never turn it on.
func TestHeartConfigDefaultsOff(t *testing.T) {
	var ac AgentConfig
	require.NoError(t, yaml.Unmarshal([]byte("max_iterations: 20\n"), &ac))
	assert.False(t, ac.HeartEnabled)
	assert.Equal(t, 0, ac.Heart.HeartbeatIntervalMinutes)
	assert.False(t, ac.Heart.ModelRouter)
	assert.False(t, ac.Heart.ChatThroughHeart)
}
