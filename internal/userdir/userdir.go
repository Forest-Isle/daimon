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
// If the directory does not exist the function returns nil without modifying cfg.
func Apply(cfg *config.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("userdir: resolve home dir: %w", err)
	}
	base := filepath.Join(home, ".IronClaw")

	if _, err := os.Stat(base); os.IsNotExist(err) {
		return nil
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

// applyPersonality reads Soul.md, Memory.md, Agent.md and prepends their content
// (with ## headings) to cfg.Agent.SystemPrompt.
func applyPersonality(cfg *config.Config, base string) error {
	type section struct {
		heading  string
		filename string
	}
	sections := []section{
		{"Soul", "Soul.md"},
		{"Memory", "Memory.md"},
		{"Agent", "Agent.md"},
	}

	var parts []string
	for _, s := range sections {
		path := filepath.Join(base, s.filename)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("userdir: read %s: %w", s.filename, err)
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("## %s\n%s", s.heading, content))
	}

	if len(parts) == 0 {
		return nil
	}

	prefix := strings.Join(parts, "\n\n")
	if cfg.Agent.SystemPrompt != "" {
		cfg.Agent.SystemPrompt = prefix + "\n\n" + cfg.Agent.SystemPrompt
	} else {
		cfg.Agent.SystemPrompt = prefix
	}
	return nil
}

// applyMCP reads ~/.IronClaw/mcp/*.yaml and appends new server definitions to cfg.
// Servers whose name already exists in cfg are skipped (project config takes priority).
func applyMCP(cfg *config.Config, base string) {
	mcpDir := filepath.Join(base, "mcp")
	entries, err := os.ReadDir(mcpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("userdir: read mcp dir", "err", err)
		return
	}

	if cfg.Tools.MCP.Servers == nil {
		cfg.Tools.MCP.Servers = make(map[string]config.MCPServerConfig)
	}

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

		if _, exists := cfg.Tools.MCP.Servers[srv.Name]; exists {
			slog.Debug("userdir: mcp server already defined in project config, skipping", "name", srv.Name)
			continue
		}

		cfg.Tools.MCP.Servers[srv.Name] = config.MCPServerConfig{
			Command:          srv.Command,
			Args:             srv.Args,
			Env:              srv.Env,
			RequiresApproval: srv.RequiresApproval,
		}
		slog.Info("userdir: mcp server registered", "name", srv.Name)
	}
}
