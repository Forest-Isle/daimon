package config

import "fmt"

func validate(cfg *Config) error {
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
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
	return nil
}
