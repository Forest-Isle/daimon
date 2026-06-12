package userdir

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"gopkg.in/yaml.v3"
)

// MCPServerFile represents a single MCP server definition loaded from ~/.daimon/mcp/*.yaml.
type MCPServerFile struct {
	Name             string            `yaml:"name"`
	Command          string            `yaml:"command"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	RequiresApproval bool              `yaml:"requires_approval"`
}

// Apply merges ~/.daimon/ personality files and MCP server definitions into cfg.
// If the directory does not exist it is initialized with default templates.
func Apply(cfg *config.Config) error {
	if err := EnsureMigrated(); err != nil {
		return fmt.Errorf("userdir: migrate legacy dir: %w", err)
	}
	base := appdir.BaseDir()

	if _, err := os.Stat(base); os.IsNotExist(err) {
		slog.Info("userdir: ~/.daimon not found, initializing default structure", "path", base)
		if err := initDir(base); err != nil {
			return fmt.Errorf("userdir: init: %w", err)
		}
	}

	if err := applyPersonality(cfg, base); err != nil {
		return err
	}
	applyMCP(cfg, base)
	ensureSkillsDir(base)
	ensureAgentsDir(base)
	return nil
}

// EnsureMigrated migrates the legacy IronClaw user directory to Daimon if needed.
func EnsureMigrated() error {
	return migrateLegacyDir(appdir.BaseDir(), appdir.LegacyBaseDir())
}

func migrateLegacyDir(base, legacy string) error {
	baseInfo, baseErr := os.Lstat(base)
	if baseErr != nil && !errors.Is(baseErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", base, baseErr)
	}

	legacyInfo, legacyErr := os.Lstat(legacy)
	if legacyErr != nil {
		if errors.Is(legacyErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", legacy, legacyErr)
	}

	if baseErr == nil {
		if baseInfo.IsDir() && legacyInfo.Mode()&os.ModeSymlink == 0 && legacyInfo.IsDir() {
			slog.Warn("userdir: legacy and Daimon directories both exist; skipping migration", "legacy", legacy, "current", base)
		}
		return nil
	}

	if legacyInfo.Mode()&os.ModeSymlink != 0 {
		slog.Warn("userdir: legacy path is a symlink; skipping migration", "legacy", legacy)
		return nil
	}
	if !legacyInfo.IsDir() {
		slog.Warn("userdir: legacy path is not a directory; skipping migration", "legacy", legacy)
		return nil
	}

	if err := os.Rename(legacy, base); err != nil {
		return fmt.Errorf("rename %s to %s: %w", legacy, base, err)
	}
	slog.Info("userdir: migrated legacy directory", "from", legacy, "to", base)

	if err := migrateLegacyDB(base); err != nil {
		return err
	}
	if err := os.Symlink(base, legacy); err != nil {
		return fmt.Errorf("create legacy compatibility symlink %s -> %s: %w", legacy, base, err)
	}
	slog.Info("userdir: created legacy compatibility symlink", "link", legacy, "target", base)
	return nil
}

func migrateLegacyDB(base string) error {
	legacyDB := filepath.Join(base, "data", appdir.LegacyDBName)
	currentDB := filepath.Join(base, "data", appdir.DBName)

	if _, err := os.Stat(currentDB); err == nil {
		if _, legacyErr := os.Stat(legacyDB); legacyErr == nil {
			slog.Warn("userdir: both legacy and Daimon database files exist; skipping database migration", "legacy", legacyDB, "current", currentDB)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", currentDB, err)
	}

	legacyInfo, err := os.Stat(legacyDB)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", legacyDB, err)
	}
	if legacyInfo.IsDir() {
		slog.Warn("userdir: legacy database path is a directory; skipping database migration", "path", legacyDB)
		return nil
	}

	if err := os.Rename(legacyDB, currentDB); err != nil {
		return fmt.Errorf("rename %s to %s: %w", legacyDB, currentDB, err)
	}
	slog.Info("userdir: migrated legacy database", "from", legacyDB, "to", currentDB)
	return nil
}

// ensureSkillsDir creates ~/.daimon/skills/ if it does not already exist.
func ensureSkillsDir(base string) {
	skillsDir := filepath.Join(base, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		slog.Warn("userdir: could not create skills dir", "path", skillsDir, "err", err)
	}
}

// ensureAgentsDir creates ~/.daimon/agents/ if it does not already exist.
func ensureAgentsDir(base string) {
	agentsDir := filepath.Join(base, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		slog.Warn("userdir: could not create agents dir", "path", agentsDir, "err", err)
	}
}

// AgentsDir returns the path to ~/.daimon/agents/.
func AgentsDir() string {
	return filepath.Join(appdir.BaseDir(), "agents")
}

// applyPersonality reads Soul.md, Memory.md, Agent.md and injects them into
// separate config fields with distinct semantic roles:
//   - Soul.md   → cfg.Agent.Personality     (persona/style, affects reply tone)
//   - Memory.md → cfg.Agent.PersistentRules (long-term rules, all phases must follow)
//   - Agent.md  → cfg.Agent.SystemPrompt    (prepended to YAML system_prompt)
func applyPersonality(cfg *config.Config, base string) error {
	// Soul.md → Personality
	if data, err := os.ReadFile(filepath.Join(base, "Soul.md")); err == nil {
		if content := strings.TrimSpace(string(data)); content != "" {
			cfg.Agent.Personality = content
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("userdir: read Soul.md: %w", err)
	}

	// Memory.md → PersistentRules
	if data, err := os.ReadFile(filepath.Join(base, "Memory.md")); err == nil {
		if content := strings.TrimSpace(string(data)); content != "" {
			cfg.Agent.PersistentRules = content
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("userdir: read Memory.md: %w", err)
	}

	// Agent.md → prepend to SystemPrompt
	if data, err := os.ReadFile(filepath.Join(base, "Agent.md")); err == nil {
		if content := strings.TrimSpace(string(data)); content != "" {
			if cfg.Agent.SystemPrompt != "" {
				cfg.Agent.SystemPrompt = content + "\n\n" + cfg.Agent.SystemPrompt
			} else {
				cfg.Agent.SystemPrompt = content
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("userdir: read Agent.md: %w", err)
	}

	return nil
}

// applyMCP reads ~/.daimon/mcp/*.yaml and appends new server definitions to cfg.
// Servers whose name already exists in cfg are skipped (project config takes priority).
// If the mcp directory does not exist, it is created with an example config file.
func applyMCP(cfg *config.Config, base string) {
	mcpDir := filepath.Join(base, "mcp")
	if err := ensureMCPDir(mcpDir); err != nil {
		return
	}

	servers := scanMCPFiles(mcpDir)
	if len(servers) == 0 {
		return
	}

	if cfg.Tools.MCP.Servers == nil {
		cfg.Tools.MCP.Servers = make(map[string]config.MCPServerConfig)
	}

	for name, srv := range servers {
		if _, exists := cfg.Tools.MCP.Servers[name]; exists {
			slog.Debug("userdir: mcp server already defined in project config, skipping", "name", name)
			continue
		}
		cfg.Tools.MCP.Servers[name] = srv
		slog.Info("userdir: mcp server registered", "name", name)
	}
}

// ScanMCPDir reads all MCP server definitions from ~/.daimon/mcp/*.yaml.
// It returns a map of server name → config, suitable for passing to mcp.Manager.SyncServers.
func ScanMCPDir() map[string]config.MCPServerConfig {
	mcpDir := filepath.Join(appdir.BaseDir(), "mcp")
	return scanMCPFiles(mcpDir)
}

// ensureMCPDir creates the mcp directory and example file if it doesn't exist.
func ensureMCPDir(mcpDir string) error {
	if _, err := os.Stat(mcpDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(mcpDir, 0755); err != nil {
		slog.Warn("userdir: create mcp dir", "err", err)
		return err
	}
	examplePath := filepath.Join(mcpDir, "example.yaml.disabled")
	if err := os.WriteFile(examplePath, []byte(defaultMCPExample), 0644); err != nil {
		slog.Warn("userdir: write mcp example", "err", err)
	}
	slog.Info("userdir: initialized mcp directory", "path", mcpDir)
	return nil
}

// scanMCPFiles reads all .yaml/.yml files in mcpDir and returns parsed server configs.
func scanMCPFiles(mcpDir string) map[string]config.MCPServerConfig {
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("userdir: read mcp dir", "err", err)
		}
		return nil
	}

	servers := make(map[string]config.MCPServerConfig)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(mcpDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("userdir: read mcp file", "file", name, "err", err)
			continue
		}

		data = config.ExpandEnv(data)

		var srv MCPServerFile
		if err := yaml.Unmarshal(data, &srv); err != nil {
			slog.Warn("userdir: parse mcp file", "file", name, "err", err)
			continue
		}

		if srv.Name == "" || srv.Command == "" {
			slog.Warn("userdir: mcp file missing required fields (name, command)", "file", name)
			continue
		}

		servers[srv.Name] = config.MCPServerConfig{
			Command:          srv.Command,
			Args:             srv.Args,
			Env:              srv.Env,
			RequiresApproval: srv.RequiresApproval,
		}
	}
	return servers
}

// initDir creates the ~/.daimon/ directory structure with default template files.
func initDir(base string) error {
	dirs := []string{
		base,
		filepath.Join(base, "mcp"),
		filepath.Join(base, "skills"),
		filepath.Join(base, "agents"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}

	defaults := []struct {
		name    string
		content string
	}{
		{"Soul.md", defaultSoul},
		{"Memory.md", defaultMemory},
		{"Agent.md", defaultAgent},
	}
	for _, f := range defaults {
		path := filepath.Join(base, f.name)
		if err := os.WriteFile(path, []byte(f.content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	slog.Info("userdir: initialized ~/.daimon with default templates", "path", base)
	return nil
}

const defaultSoul = `# Soul — Personality & Style

Define the core personality and identity of your Daimon agent.
This content is injected as the agent's persona, influencing reply tone and style.
In cognitive mode, it shapes the REFLECT phase's final_answer voice.

## Example

You are a helpful coding assistant who speaks concisely.
`

const defaultMemory = `# Memory — Persistent Rules

- Configuration directory: ~/.daimon/
- Add custom skills by placing SKILL.md files in ~/.daimon/skills/
- Add MCP tool servers by placing *.yaml configs in ~/.daimon/mcp/
- **NEVER run global package installations** (npm install -g, pip install, etc.) on the user's machine. To add MCP tools, only create YAML config files in ~/.daimon/mcp/ using on-demand runners (npx -y, uvx, docker run).
- To install skills, use ` + "`daimon skill install <slug>`" + ` from ClawHub, or manually create SKILL.md files in ~/.daimon/skills/. Skills are pure markdown, never use package managers.
`

const defaultAgent = `# Agent — Core System Prompt

You are Daimon, a local-first AI assistant with tool-use capabilities.

## Capabilities

- Execute shell commands, read/write files, make HTTP requests, and browse web pages.
- Retrieve relevant memories to inform your responses.
- Follow multi-step plans when tasks are complex; prefer direct answers when they are not.

## MCP Tool Management

You can manage your own MCP tool servers. **NEVER install packages globally** (no npm install -g, pip install, etc.) on the user's machine. Instead, create YAML config files in ~/.daimon/mcp/.

When the user asks to "install" or "add" an MCP tool:
1. Create a .yaml file in ~/.daimon/mcp/ with the server definition
2. Use on-demand runners like npx -y, uvx, or docker run as the command
3. The hot-reload watcher picks up new configs automatically (within 30 seconds)

YAML format:
` + "```" + `yaml
name: <unique-server-id>
command: npx
args:
  - -y
  - "<package-name>"
env:
  API_KEY: "${ENV_VAR}"
requires_approval: true
` + "```" + `

To remove an MCP tool, delete or rename its .yaml file (e.g., append .disabled).

## Skill Management

Skills are SKILL.md files (YAML frontmatter + markdown body) stored in ~/.daimon/skills/.
A built-in ` + "`clawhub`" + ` skill is always available — it teaches you how to search and install skills from the ClawHub public registry using ` + "`clawhub`" + ` CLI.

When the user asks to "install" or "add" a skill:
1. Use the clawhub skill instructions to search and install via bash
2. Or use the CLI shorthand: ` + "`daimon skill search <query>`" + ` / ` + "`daimon skill install <slug>`" + `
3. Skills are loaded automatically at startup

To create a custom skill manually, write a SKILL.md file in ~/.daimon/skills/<name>/SKILL.md with YAML frontmatter (name, description, version, tags) and markdown body.

Other CLI commands:
- ` + "`daimon skill list`" + ` — list installed skills
- ` + "`daimon skill update`" + ` — update all installed skills
- ` + "`daimon skill remove <name>`" + ` — remove a skill

**NEVER install skills by running npm install, pip install, or any package manager.** Skills are pure markdown files.

## Guidelines

- Be concise. Answer the question, then stop.
- Use tools only when necessary — don't over-automate simple queries.
- When uncertain, state your assumptions before acting.
- Respect user-defined rules in the Rules section below.
`

const defaultMCPExample = `# MCP Server Configuration
# Rename to *.yaml to enable. Each file defines one MCP server.
#
# Required fields:
#   name:    unique server identifier
#   command: executable to launch the server
#
# Optional fields:
#   args:              list of command-line arguments
#   env:               environment variables (supports ${VAR} expansion)
#   requires_approval: true to require user confirmation before tool calls (default: false)
#
# --- Example: filesystem server ---
# name: filesystem
# command: npx
# args:
#   - -y
#   - "@modelcontextprotocol/server-filesystem"
#   - "/path/to/allowed/dir"
#
# --- Example: custom server with env ---
# name: my-api
# command: python
# args: ["-m", "my_mcp_server"]
# env:
#   API_KEY: "${MY_API_KEY}"
# requires_approval: true
`
