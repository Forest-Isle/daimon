package gateway

import (
	"fmt"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/feature"
)

func registerFeatures(cfg *config.Config) *feature.Registry {
	r := feature.NewRegistry()

	r.Register(feature.Feature{
		Name:        "memory",
		Description: "Memory system with file storage and fact extraction",
		Default:     true,
	})
	r.Register(feature.Feature{
		Name:        "skills",
		Description: "SKILL.md loading and read_skill tool",
		Default:     true,
	})
	r.Register(feature.Feature{
		Name:        "multi_agent",
		Description: "Sub-agent spawning and orchestration",
		Default:     true,
	})
	r.Register(feature.Feature{
		Name:        "server",
		Description: "Standalone HTTP admin server",
		Default:     false,
	})

	// MCP servers — each configured server gets its own feature
	for name, srv := range cfg.Tools.MCP.Servers {
		name := name
		srv := srv
		_ = srv
		r.Register(feature.Feature{
			Name:        "mcp_" + name,
			Description: fmt.Sprintf("MCP server: %s (%s)", name, srv.Command),
			Default:     true,
		})
	}

	return r
}

func configToOverrides(cfg *config.Config) map[string]bool {
	return map[string]bool{
		"memory":          cfg.Memory.Enabled,
		"skills":          cfg.Skills.Enabled,
		"multi_agent":     cfg.Agents.Enabled,
		"server":          cfg.Server.Enabled,
	}
}
