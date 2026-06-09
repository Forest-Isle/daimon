package feature

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTrue_Enabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "alpha", Default: true})
	reg.Resolve(context.Background(), nil)
	assert.True(t, reg.IsEnabled("alpha"))
}

func TestDefaultFalse_NotEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "beta", Default: false})
	reg.Resolve(context.Background(), nil)
	assert.False(t, reg.IsEnabled("beta"))
}

func TestOverrideWins(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "gamma", Default: false})
	reg.Resolve(context.Background(), map[string]bool{"gamma": true})
	assert.True(t, reg.IsEnabled("gamma"))
}

func TestOverrideDisablesDefaultTrue(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "delta", Default: true})
	reg.Resolve(context.Background(), map[string]bool{"delta": false})
	assert.False(t, reg.IsEnabled("delta"))
}

func TestAutoDetectUnavailable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "docker",
		Default: true,
		AutoDetect: func(ctx context.Context) bool {
			return false
		},
	})
	reg.Resolve(context.Background(), nil)
	assert.False(t, reg.IsEnabled("docker"))
}

func TestAutoDetectAvailable_RespectsDefault(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "optional",
		Default: false,
		AutoDetect: func(ctx context.Context) bool {
			return true
		},
	})
	reg.Resolve(context.Background(), nil)
	assert.False(t, reg.IsEnabled("optional"), "available but default=false → disabled")
}

func TestDependency_Cascade(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "multi_agent", Default: false})
	reg.Register(Feature{Name: "team", Default: true})
	reg.Resolve(context.Background(), nil)
	assert.False(t, reg.IsEnabled("multi_agent"))
	assert.False(t, reg.IsEnabled("team"), "team disabled because multi_agent is off")
}

func TestDependency_BothEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "multi_agent", Default: true})
	reg.Register(Feature{Name: "team", Default: true})
	reg.Resolve(context.Background(), nil)
	assert.True(t, reg.IsEnabled("multi_agent"))
	assert.True(t, reg.IsEnabled("team"))
}

func TestList_Order(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "base", Default: true, Description: "base"})
	reg.Register(Feature{Name: "multi_agent", Default: true, Description: "ma"})
	reg.Register(Feature{Name: "team", Default: true, Description: "team"})
	reg.Resolve(context.Background(), nil)

	list := reg.List()
	require.Len(t, list, 3)
	assert.Equal(t, "base", list[0].Name)
	assert.Equal(t, "multi_agent", list[1].Name)
	assert.Equal(t, "team", list[2].Name)
}

func TestEnabledNames(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "on1", Default: true})
	reg.Register(Feature{Name: "off1", Default: false})
	reg.Register(Feature{Name: "on2", Default: true})
	reg.Resolve(context.Background(), nil)

	enabled := reg.EnabledNames()
	assert.Contains(t, enabled, "on1")
	assert.Contains(t, enabled, "on2")
	assert.NotContains(t, enabled, "off1")
	assert.Len(t, enabled, 2)
}

func TestUnknownFeature(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "real", Default: true})
	reg.Resolve(context.Background(), nil)
	assert.False(t, reg.IsEnabled("nope"))
}
