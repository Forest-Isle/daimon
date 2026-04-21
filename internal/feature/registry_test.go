package feature

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTrue_EnabledAfterInit(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "alpha", Default: true})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.True(t, reg.IsEnabled("alpha"))
}

func TestDefaultFalse_NotEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "beta", Default: false})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("beta"))
}

func TestOverride_EnablesDefaultFalse(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "gamma", Default: false})
	reg.ApplyOverrides(map[string]bool{"gamma": true})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.True(t, reg.IsEnabled("gamma"))
}

func TestOverride_DisablesDefaultTrue(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "delta", Default: true})
	reg.ApplyOverrides(map[string]bool{"delta": false})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("delta"))
}

func TestDependency_BothEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "base", Default: true})
	reg.Register(Feature{Name: "child", Default: true, Dependencies: []string{"base"}})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.True(t, reg.IsEnabled("base"))
	assert.True(t, reg.IsEnabled("child"))
}

func TestDependency_Cascade_BaseDisabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "base", Default: false})
	reg.Register(Feature{Name: "child", Default: true, Dependencies: []string{"base"}})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("base"))
	assert.False(t, reg.IsEnabled("child"))
}

func TestCircularDependency_Error(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "a", Default: true, Dependencies: []string{"b"}})
	reg.Register(Feature{Name: "b", Default: true, Dependencies: []string{"a"}})
	err := reg.ResolveAndInit(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestUnknownDependency_Error(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "lonely", Default: true, Dependencies: []string{"ghost"}})
	err := reg.ResolveAndInit(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown dependency")
}

func TestAutoDetect_NotAvailable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "docker",
		Default: true,
		AutoDetect: func(ctx context.Context) DetectResult {
			return DetectResult{Available: false, Reason: "docker not installed"}
		},
	})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("docker"))
}

func TestAutoDetect_Available_RespectsDefault(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "optional",
		Default: false,
		AutoDetect: func(ctx context.Context) DetectResult {
			return DetectResult{Available: true, Reason: "found"}
		},
	})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("optional"), "available but default=false should stay disabled")
}

func TestOnEnable_Error_DisablesFeature(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "flaky",
		Default: true,
		OnEnable: func(ctx context.Context) error {
			return fmt.Errorf("init boom")
		},
	})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("flaky"))
}

func TestRuntimeEnable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{
		Name:    "lazy",
		Default: false,
		OnEnable: func(ctx context.Context) error {
			return nil
		},
	})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.False(t, reg.IsEnabled("lazy"))

	require.NoError(t, reg.Enable(context.Background(), "lazy"))
	assert.True(t, reg.IsEnabled("lazy"))
}

func TestRuntimeDisable(t *testing.T) {
	reg := NewRegistry()
	var disabled bool
	reg.Register(Feature{
		Name:    "toggler",
		Default: true,
		OnDisable: func(ctx context.Context) error {
			disabled = true
			return nil
		},
	})
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	assert.True(t, reg.IsEnabled("toggler"))

	require.NoError(t, reg.Disable(context.Background(), "toggler"))
	assert.False(t, reg.IsEnabled("toggler"))
	assert.True(t, disabled, "OnDisable should have been called")
}

func TestDisable_BlockedByDependent(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "core", Default: true})
	reg.Register(Feature{Name: "ext", Default: true, Dependencies: []string{"core"}})
	require.NoError(t, reg.ResolveAndInit(context.Background()))

	err := reg.Disable(context.Background(), "core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ext")
	assert.True(t, reg.IsEnabled("core"), "core should still be enabled")
}

func TestEnable_UnknownFeature_Error(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.ResolveAndInit(context.Background()))
	err := reg.Enable(context.Background(), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown feature")
}

func TestList_ReturnsAllInOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "base", Default: true, Description: "base feature"})
	reg.Register(Feature{Name: "mid", Default: true, Description: "mid feature", Dependencies: []string{"base"}})
	reg.Register(Feature{Name: "top", Default: false, Description: "top feature", Dependencies: []string{"mid"}})
	require.NoError(t, reg.ResolveAndInit(context.Background()))

	list := reg.List()
	require.Len(t, list, 3)

	names := make([]string, len(list))
	for i, info := range list {
		names[i] = info.Name
	}

	baseIdx, midIdx, topIdx := -1, -1, -1
	for i, n := range names {
		switch n {
		case "base":
			baseIdx = i
		case "mid":
			midIdx = i
		case "top":
			topIdx = i
		}
	}
	assert.Less(t, baseIdx, midIdx, "base should come before mid")
	assert.Less(t, midIdx, topIdx, "mid should come before top")

	assert.True(t, list[baseIdx].Enabled)
	assert.True(t, list[midIdx].Enabled)
	assert.False(t, list[topIdx].Enabled)
}

func TestSetOnEnable(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "lazy", Default: false, HotReloadable: true})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	called := false
	require.NoError(t, r.SetOnEnable("lazy", func(ctx context.Context) error {
		called = true
		return nil
	}))

	require.NoError(t, r.Enable(context.Background(), "lazy"))
	assert.True(t, called)
	assert.True(t, r.IsEnabled("lazy"))
}

func TestSetOnDisable(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "active", Default: true, HotReloadable: true})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	called := false
	require.NoError(t, r.SetOnDisable("active", func(ctx context.Context) error {
		called = true
		return nil
	}))

	require.NoError(t, r.Disable(context.Background(), "active"))
	assert.True(t, called)
	assert.False(t, r.IsEnabled("active"))
}

func TestSetOnEnableUnknownFeature(t *testing.T) {
	r := NewRegistry()
	err := r.SetOnEnable("nope", func(ctx context.Context) error { return nil })
	assert.Error(t, err)
}

func TestHotReloadableInList(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "hot", Default: true, HotReloadable: true})
	r.Register(Feature{Name: "cold", Default: true, HotReloadable: false})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	list := r.List()
	hotReloadableMap := make(map[string]bool)
	for _, f := range list {
		hotReloadableMap[f.Name] = f.HotReloadable
	}
	assert.True(t, hotReloadableMap["hot"])
	assert.False(t, hotReloadableMap["cold"])
}

func TestEnableHookCanCallIsEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "base", Default: true})
	r.Register(Feature{Name: "hot", Default: false, HotReloadable: true})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	require.NoError(t, r.SetOnEnable("hot", func(ctx context.Context) error {
		_ = r.IsEnabled("base")
		return nil
	}))

	done := make(chan error, 1)
	go func() { done <- r.Enable(context.Background(), "hot") }()

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, r.IsEnabled("hot"))
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: Enable() did not return within 2s")
	}
}

func TestDisableHookCanCallIsEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "live", Default: true, HotReloadable: true})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	require.NoError(t, r.SetOnDisable("live", func(ctx context.Context) error {
		_ = r.IsEnabled("live")
		return nil
	}))

	done := make(chan error, 1)
	go func() { done <- r.Disable(context.Background(), "live") }()

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.False(t, r.IsEnabled("live"))
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: Disable() did not return within 2s")
	}
}

func TestEnabledNames(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Feature{Name: "on1", Default: true})
	reg.Register(Feature{Name: "off1", Default: false})
	reg.Register(Feature{Name: "on2", Default: true})
	require.NoError(t, reg.ResolveAndInit(context.Background()))

	enabled := reg.EnabledNames()
	assert.Contains(t, enabled, "on1")
	assert.Contains(t, enabled, "on2")
	assert.NotContains(t, enabled, "off1")
	assert.Len(t, enabled, 2)
}
