package config

import "fmt"

// knownProviders lists the supported LLM provider values.
var knownProviders = map[string]bool{
	"claude":             true,
	"openai":             true,
	"openai-compatible":  true,
}

func validate(cfg *Config) error {
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
	}
	if !knownProviders[cfg.LLM.Provider] {
		return fmt.Errorf("llm.provider must be one of: claude, openai, openai-compatible (got %q)", cfg.LLM.Provider)
	}
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram.token is required")
	}
	if len(cfg.Telegram.AllowedUserIDs) == 0 {
		return fmt.Errorf("telegram.allowed_user_ids must have at least one entry")
	}
	if cfg.Agent.MaxIterations <= 0 {
		cfg.Agent.MaxIterations = 20
	}
	if cfg.Store.Path == "" {
		cfg.Store.Path = "./data/ironclaw.db"
	}

	// Validate MCP server configs
	for name, srv := range cfg.Tools.MCP.Servers {
		if srv.Command == "" {
			return fmt.Errorf("mcp.servers.%s: command is required", name)
		}
	}

	return nil
}
