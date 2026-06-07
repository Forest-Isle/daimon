package feature

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feature_state.json")

	overrides := map[string]bool{
		"memory":    true,
		"server":    false,
		"scheduler": true,
	}

	require.NoError(t, SaveOverrides(path, overrides))

	loaded, err := LoadOverrides(path)
	require.NoError(t, err)
	assert.Equal(t, overrides, loaded)
}

func TestLoadOverrides_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	loaded, err := LoadOverrides(path)
	require.NoError(t, err)
	assert.Empty(t, loaded)
}

func TestLoadOverrides_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feature_state.json")
	require.NoError(t, os.WriteFile(path, []byte("not json {{{"), 0o640))

	_, err := LoadOverrides(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse state file")
}

func TestSaveOverrides_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "feature_state.json")

	require.NoError(t, SaveOverrides(path, map[string]bool{"memory": true}))
	assert.FileExists(t, path)
}

func TestSaveOverrides_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feature_state.json")

	// First write succeeds
	require.NoError(t, SaveOverrides(path, map[string]bool{"memory": true}))

	// Overwrite with different content
	require.NoError(t, SaveOverrides(path, map[string]bool{"memory": false, "server": true}))

	// Temp file should not exist after success
	assert.NoFileExists(t, path+".tmp")

	loaded, err := LoadOverrides(path)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"memory": false, "server": true}, loaded)
}

func TestRuntimeOverrides_ReturnsAllStates(t *testing.T) {
	r := NewRegistry()
	r.Register(Feature{Name: "on", Default: true})
	r.Register(Feature{Name: "off", Default: false})
	require.NoError(t, r.ResolveAndInit(context.Background()))

	overrides := r.RuntimeOverrides()
	assert.True(t, overrides["on"])
	assert.False(t, overrides["off"])
}

func TestDefaultStatePath(t *testing.T) {
	path := DefaultStatePath("/home/user/.IronClaw")
	assert.Equal(t, "/home/user/.IronClaw/feature_state.json", path)
}
