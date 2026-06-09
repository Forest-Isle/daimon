package config

import (
	"fmt"
	"log/slog"
	"reflect"

	"gopkg.in/yaml.v3"
)

// knownProviders lists the supported LLM provider values.
var knownProviders = map[string]bool{
	"claude":            true,
	"openai":            true,
	"openai-compatible": true,
}

// knownTopLevelKeys is the set of valid top-level YAML keys in the Config struct.
// Built from Config struct yaml tags at init time.
var knownTopLevelKeys = buildKnownKeys()

func buildKnownKeys() map[string]bool {
	keys := make(map[string]bool)
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" and ",flow" etc.
		name := tag
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				name = tag[:j]
				break
			}
		}
		keys[name] = true
	}
	return keys
}

// CheckUnknownKeys parses raw YAML data and warns about top-level keys that
// have no corresponding Config struct field. This catches typos and keys from
// removed features (e.g., RL config) that silently do nothing.
func CheckUnknownKeys(rawYAML []byte) {
	var raw map[string]any
	if err := yaml.Unmarshal(rawYAML, &raw); err != nil {
		return // can't parse, let the main unmarshal report the error
	}
	for key := range raw {
		if !knownTopLevelKeys[key] {
			slog.Warn("config: unknown top-level key — has no effect",
				"key", key,
				"hint", "check for typos or keys from removed features (e.g., rl:)")
		}
	}
}

func validate(cfg *Config) error {
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
	}
	if !knownProviders[cfg.LLM.Provider] {
		return fmt.Errorf("llm.provider must be one of: claude, openai, openai-compatible (got %q)", cfg.LLM.Provider)
	}
	// Telegram is optional — only validate when token or user IDs are provided.
	if cfg.Telegram.Token != "" || len(cfg.Telegram.AllowedUserIDs) > 0 {
		if cfg.Telegram.Token == "" {
			return fmt.Errorf("telegram.token is required when telegram is configured")
		}
		if len(cfg.Telegram.AllowedUserIDs) == 0 {
			return fmt.Errorf("telegram.allowed_user_ids must have at least one entry when telegram is configured")
		}
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
