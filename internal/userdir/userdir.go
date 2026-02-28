package userdir

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/punkopunko/ironclaw/internal/config"
	"gopkg.in/yaml.v3"
)

// MCPServerFile represents a single MCP server definition loaded from ~/.IronClaw/mcp/*.yaml.
type MCPServerFile struct {
	Name             string            `yaml:"name"`
	Command          string            `yaml:"command"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	RequiresApproval bool              `yaml:"requires_approval"`
}

// Apply merges ~/.IronClaw/ personality files and MCP server definitions into cfg.
// If the directory does not exist it is initialized with default templates.
func Apply(cfg *config.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("userdir: resolve home dir: %w", err)
	}
	base := filepath.Join(home, ".IronClaw")

	if _, err := os.Stat(base); os.IsNotExist(err) {
		slog.Info("userdir: ~/.IronClaw not found, initializing default structure", "path", base)
		if err := initDir(base); err != nil {
			return fmt.Errorf("userdir: init: %w", err)
		}
	}

	if err := applyPersonality(cfg, base); err != nil {
		return err
	}
	applyMCP(cfg, base)
	ensureSkillsDir(base)
	return nil
}

// ensureSkillsDir creates ~/.IronClaw/skills/ if it does not already exist.
func ensureSkillsDir(base string) {
	skillsDir := filepath.Join(base, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		slog.Warn("userdir: could not create skills dir", "path", skillsDir, "err", err)
	}
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

// applyMCP reads ~/.IronClaw/mcp/*.yaml and appends new server definitions to cfg.
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

// ScanMCPDir reads all MCP server definitions from ~/.IronClaw/mcp/*.yaml.
// It returns a map of server name → config, suitable for passing to mcp.Manager.SyncServers.
func ScanMCPDir() map[string]config.MCPServerConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("userdir: resolve home dir for mcp scan", "err", err)
		return nil
	}
	mcpDir := filepath.Join(home, ".IronClaw", "mcp")
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

// initDir creates the ~/.IronClaw/ directory structure with default template files.
func initDir(base string) error {
	dirs := []string{
		base,
		filepath.Join(base, "mcp"),
		filepath.Join(base, "skills"),
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

	slog.Info("userdir: initialized ~/.IronClaw with default templates", "path", base)
	return nil
}

const defaultSoul = `# Soul — Personality & Style

Define the core personality and identity of your IronClaw agent.
This content is injected as the agent's persona, influencing reply tone and style.
In cognitive mode, it shapes the REFLECT phase's final_answer voice.

## Example

You are a helpful coding assistant who speaks concisely.
`

const defaultMemory = `# Memory — Persistent Rules

Long-term rules and constraints your agent must always follow.
These are injected into ALL cognitive phases (PLAN, REFLECT) and the simple-mode system prompt.

## Example

- Preferred language: Go
- Project conventions: use slog for logging
`

const defaultAgent = `# Agent — Core System Prompt

Agent behavior and workflow customization.
This content is prepended to the YAML system_prompt as the base instruction set.

## Example

- Always think step-by-step before answering.
- Prefer simple solutions over clever ones.
`

const defaultMCPExample = `# MCP Server Configuration Example
# Rename this file to *.yaml to enable it.
#
# name: my-server
# command: npx
# args:
#   - -y
#   - "@example/mcp-server"
# env:
#   API_KEY: "${MY_API_KEY}"
# requires_approval: false
`
