package userdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
)

// agentConfigFields are the fields we care about in config.AgentConfig
// We reference them dynamically to avoid tight struct coupling.

func TestAgentsDir(t *testing.T) {
	expected := filepath.Join(appdir.BaseDir(), "agents")
	got := AgentsDir()
	if got != expected {
		t.Errorf("AgentsDir() = %q, want %q", got, expected)
	}
}

func TestApply_InitializesDir(t *testing.T) {
	// Use a temp home dir override
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify directory created
	base := appdir.BaseDir()
	if _, err := os.Stat(base); os.IsNotExist(err) {
		t.Error("expected .daimon directory to be created")
	}
	if _, err := os.Stat(filepath.Join(base, "Soul.md")); os.IsNotExist(err) {
		t.Error("expected Soul.md to be created")
	}
	if _, err := os.Stat(filepath.Join(base, "Memory.md")); os.IsNotExist(err) {
		t.Error("expected Memory.md to be created")
	}
	if _, err := os.Stat(filepath.Join(base, "Agent.md")); os.IsNotExist(err) {
		t.Error("expected Agent.md to be created")
	}
}

func TestApply_MigratesLegacyDirAndDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacy := appdir.LegacyBaseDir()
	if err := os.MkdirAll(filepath.Join(legacy, "data"), 0755); err != nil {
		t.Fatalf("mkdir legacy data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "Soul.md"), []byte("legacy soul"), 0644); err != nil {
		t.Fatalf("write legacy Soul.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "data", appdir.LegacyDBName), []byte("db"), 0644); err != nil {
		t.Fatalf("write legacy db: %v", err)
	}

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	base := appdir.BaseDir()
	if _, err := os.Stat(filepath.Join(base, "Soul.md")); err != nil {
		t.Fatalf("expected migrated Soul.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "data", appdir.DBName)); err != nil {
		t.Fatalf("expected migrated Daimon database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "data", appdir.LegacyDBName)); !os.IsNotExist(err) {
		t.Fatalf("expected legacy database to be renamed, got err=%v", err)
	}

	info, err := os.Lstat(legacy)
	if err != nil {
		t.Fatalf("expected legacy compatibility symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected legacy path to be a symlink, got mode %v", info.Mode())
	}
	target, err := os.Readlink(legacy)
	if err != nil {
		t.Fatalf("read legacy symlink: %v", err)
	}
	if target != base {
		t.Fatalf("legacy symlink target = %q, want %q", target, base)
	}
}

func TestApply_DoesNotMigrateWhenBothDirsExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacy := appdir.LegacyBaseDir()
	base := appdir.BaseDir()
	if err := os.MkdirAll(filepath.Join(legacy, "data"), 0755); err != nil {
		t.Fatalf("mkdir legacy data: %v", err)
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "data", appdir.LegacyDBName), []byte("db"), 0644); err != nil {
		t.Fatalf("write legacy db: %v", err)
	}

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	info, err := os.Lstat(legacy)
	if err != nil {
		t.Fatalf("expected legacy dir to remain: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected legacy dir not to be replaced with a symlink")
	}
	if _, err := os.Stat(filepath.Join(legacy, "data", appdir.LegacyDBName)); err != nil {
		t.Fatalf("expected legacy db to remain untouched: %v", err)
	}
}

func TestApply_ReadsPersonality(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := appdir.BaseDir()
	os.MkdirAll(base, 0755)
	os.WriteFile(filepath.Join(base, "Soul.md"), []byte("You are a helpful assistant"), 0644)
	os.WriteFile(filepath.Join(base, "Memory.md"), []byte("Important: never modify system files"), 0644)
	os.WriteFile(filepath.Join(base, "Agent.md"), []byte("Custom system prompt"), 0644)
	os.MkdirAll(filepath.Join(base, "mcp"), 0755)

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if cfg.Agent.Personality != "You are a helpful assistant" {
		t.Errorf("unexpected Personality: %q", cfg.Agent.Personality)
	}
	if cfg.Agent.PersistentRules != "Important: never modify system files" {
		t.Errorf("unexpected PersistentRules: %q", cfg.Agent.PersistentRules)
	}
	if cfg.Agent.SystemPrompt != "Custom system prompt" {
		t.Errorf("unexpected SystemPrompt: %q", cfg.Agent.SystemPrompt)
	}
}

func TestApply_PrependsAgentMDFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := appdir.BaseDir()
	os.MkdirAll(base, 0755)
	os.WriteFile(filepath.Join(base, "Agent.md"), []byte("Custom prompt"), 0644)
	os.MkdirAll(filepath.Join(base, "mcp"), 0755)

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if cfg.Agent.SystemPrompt != "Custom prompt" {
		t.Errorf("expected SystemPrompt 'Custom prompt', got %q", cfg.Agent.SystemPrompt)
	}
}

func TestApply_PrependsAgentMDToExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := appdir.BaseDir()
	os.MkdirAll(base, 0755)
	os.WriteFile(filepath.Join(base, "Agent.md"), []byte("File prompt"), 0644)
	os.MkdirAll(filepath.Join(base, "mcp"), 0755)

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// With no YAML system prompt, Agent.md is the full prompt
	if cfg.Agent.SystemPrompt != "File prompt" {
		t.Errorf("expected 'File prompt', got %q", cfg.Agent.SystemPrompt)
	}
}

func TestApply_MissingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := appdir.BaseDir()
	os.MkdirAll(base, 0755)
	os.MkdirAll(filepath.Join(base, "mcp"), 0755)
	// No personality files

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Fields should remain empty
	if cfg.Agent.Personality != "" {
		t.Errorf("expected empty Personality, got %q", cfg.Agent.Personality)
	}
}

func TestApply_MCPFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := appdir.BaseDir()
	os.MkdirAll(base, 0755)
	os.MkdirAll(filepath.Join(base, "mcp"), 0755)
	os.WriteFile(filepath.Join(base, "mcp", "server.yaml"), []byte(`
name: test-server
command: echo
args: ["hello"]
env:
  KEY: value
requires_approval: true
`), 0644)

	cfg := &config.Config{}
	if err := Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if cfg.Tools.MCP.Servers == nil {
		t.Fatal("expected MCP servers to be loaded")
	}
	srv, ok := cfg.Tools.MCP.Servers["test-server"]
	if !ok {
		t.Fatal("expected test-server to be registered")
	}
	if srv.Command != "echo" {
		t.Errorf("expected Command 'echo', got %q", srv.Command)
	}
	if !srv.RequiresApproval {
		t.Error("expected RequiresApproval true")
	}
}

func TestScanMCPDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mcpDir := filepath.Join(appdir.BaseDir(), "mcp")
	os.MkdirAll(mcpDir, 0755)
	os.WriteFile(filepath.Join(mcpDir, "github.yaml"), []byte(`
name: github
command: npx
args: ["-y", "@github/mcp"]
`), 0644)

	servers := ScanMCPDir()
	if servers == nil {
		t.Fatal("expected non-nil server map")
	}
	srv, ok := servers["github"]
	if !ok {
		t.Fatal("expected github server")
	}
	if srv.Command != "npx" {
		t.Errorf("expected Command 'npx', got %q", srv.Command)
	}
}

func TestScanMCPDir_InvalidFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mcpDir := filepath.Join(appdir.BaseDir(), "mcp")
	os.MkdirAll(mcpDir, 0755)
	os.WriteFile(filepath.Join(mcpDir, "bad.yaml"), []byte("invalid: yaml: [[["), 0644)

	servers := ScanMCPDir()
	if servers == nil {
		t.Fatal("expected non-nil server map")
	}
	if len(servers) != 0 {
		t.Errorf("expected 0 servers for invalid yaml, got %d", len(servers))
	}
}

func TestScanMCPDir_NoDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	servers := ScanMCPDir()
	if servers != nil {
		t.Errorf("expected nil when mcp dir doesn't exist, got %+v", servers)
	}
}

func TestEnsureSkillsDir(t *testing.T) {
	dir := t.TempDir()
	ensureSkillsDir(dir)
	skillsDir := filepath.Join(dir, "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		t.Error("expected skills dir to be created")
	}

	// Second call should be idempotent
	ensureSkillsDir(dir)
}

func TestEnsureAgentsDir(t *testing.T) {
	dir := t.TempDir()
	ensureAgentsDir(dir)
	agentsDir := filepath.Join(dir, "agents")
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		t.Error("expected agents dir to be created")
	}
}

func TestMCPServerFile_Defaults(t *testing.T) {
	s := MCPServerFile{
		Name:    "test",
		Command: "echo",
	}
	if s.Name != "test" {
		t.Errorf("unexpected Name: %q", s.Name)
	}
	if s.RequiresApproval {
		t.Error("expected RequiresApproval false by default")
	}
}
