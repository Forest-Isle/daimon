package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergePermissionRules_DenyWins(t *testing.T) {
	base := []PermissionRule{
		{Tool: "bash", Action: "allow"},
		{Tool: "file", Action: "allow"},
	}
	overlay := []PermissionRule{
		{Tool: "bash", Action: "deny"},
	}

	merged := MergePermissionRules(base, overlay)

	byTool := make(map[string]string)
	for _, r := range merged {
		byTool[r.Tool] = r.Action
	}

	assert.Equal(t, "deny", byTool["bash"], "deny should override allow")
	assert.Equal(t, "allow", byTool["file"], "unaffected rule should remain")
}

func TestMergePermissionRules_DenyInBaseWins(t *testing.T) {
	base := []PermissionRule{
		{Tool: "bash", Action: "deny"},
	}
	overlay := []PermissionRule{
		{Tool: "bash", Action: "allow"},
	}

	merged := MergePermissionRules(base, overlay)

	require.Len(t, merged, 1)
	assert.Equal(t, "deny", merged[0].Action, "deny in base should still win over allow in overlay")
}

func TestMergePermissionRules_HighestPriorityWins(t *testing.T) {
	base := []PermissionRule{
		{Tool: "http", Action: "notify"},
	}
	overlay := []PermissionRule{
		{Tool: "http", Action: "approve"},
	}

	merged := MergePermissionRules(base, overlay)

	require.Len(t, merged, 1)
	assert.Equal(t, "approve", merged[0].Action, "overlay should override base for non-deny rules")
}

func TestMergePermissionRules_NewRulesAppended(t *testing.T) {
	base := []PermissionRule{
		{Tool: "bash", Action: "allow"},
	}
	overlay := []PermissionRule{
		{Tool: "mcp", Action: "approve"},
	}

	merged := MergePermissionRules(base, overlay)

	assert.Len(t, merged, 2)
}

func TestMergePermissionRules_PatternDistinguishes(t *testing.T) {
	base := []PermissionRule{
		{Tool: "bash", Pattern: "rm *", Action: "deny"},
		{Tool: "bash", Pattern: "ls *", Action: "allow"},
	}
	overlay := []PermissionRule{
		{Tool: "bash", Pattern: "ls *", Action: "notify"},
	}

	merged := MergePermissionRules(base, overlay)

	byPattern := make(map[string]string)
	for _, r := range merged {
		byPattern[r.Pattern] = r.Action
	}
	assert.Equal(t, "deny", byPattern["rm *"])
	assert.Equal(t, "notify", byPattern["ls *"])
}

func TestMergePermissionRules_Empty(t *testing.T) {
	merged := MergePermissionRules(nil, nil)
	assert.Empty(t, merged)
}

func TestLoadProjectInstructions(t *testing.T) {
	workDir := t.TempDir()

	// No instructions file
	assert.Equal(t, "", LoadProjectInstructions(workDir))

	// Create .ironclaw/IRONCLAW.md
	ironcDir := filepath.Join(workDir, ".ironclaw")
	require.NoError(t, os.MkdirAll(ironcDir, 0o755))
	content := "# Project Instructions\nAlways use Go.\n"
	require.NoError(t, os.WriteFile(filepath.Join(ironcDir, "IRONCLAW.md"), []byte(content), 0o644))

	result := LoadProjectInstructions(workDir)
	assert.Equal(t, content, result)
}

func TestLoadProjectInstructions_RootFallback(t *testing.T) {
	workDir := t.TempDir()

	// Create IRONCLAW.md at root (fallback)
	content := "# Root Instructions\n"
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "IRONCLAW.md"), []byte(content), 0o644))

	result := LoadProjectInstructions(workDir)
	assert.Equal(t, content, result)
}

func TestLoadProjectInstructions_DotDirTakesPrecedence(t *testing.T) {
	workDir := t.TempDir()

	// Both files exist
	ironcDir := filepath.Join(workDir, ".ironclaw")
	require.NoError(t, os.MkdirAll(ironcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ironcDir, "IRONCLAW.md"), []byte("dotdir"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "IRONCLAW.md"), []byte("root"), 0o644))

	result := LoadProjectInstructions(workDir)
	assert.Equal(t, "dotdir", result, ".ironclaw/ dir should take precedence over root")
}
