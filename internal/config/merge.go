package config

// mergeConfig merges overlay values into base. Non-zero overlay values
// override base values. Slice fields are appended, not replaced.
func mergeConfig(base *Config, overlay *Config) {
	if overlay == nil {
		return
	}

	// LLM
	if overlay.LLM.Provider != "" {
		base.LLM.Provider = overlay.LLM.Provider
	}
	if overlay.LLM.APIKey != "" {
		base.LLM.APIKey = overlay.LLM.APIKey
	}
	if overlay.LLM.BaseURL != "" {
		base.LLM.BaseURL = overlay.LLM.BaseURL
	}
	if overlay.LLM.Model != "" {
		base.LLM.Model = overlay.LLM.Model
	}
	if overlay.LLM.MaxTokens > 0 {
		base.LLM.MaxTokens = overlay.LLM.MaxTokens
	}
	if overlay.LLM.Retry.MaxRetries > 0 {
		base.LLM.Retry.MaxRetries = overlay.LLM.Retry.MaxRetries
	}
	if overlay.LLM.Retry.BaseDelay > 0 {
		base.LLM.Retry.BaseDelay = overlay.LLM.Retry.BaseDelay
	}
	if overlay.LLM.Retry.MaxDelay > 0 {
		base.LLM.Retry.MaxDelay = overlay.LLM.Retry.MaxDelay
	}

	// Telegram
	if overlay.Telegram.Token != "" {
		base.Telegram.Token = overlay.Telegram.Token
	}
	if len(overlay.Telegram.AllowedUserIDs) > 0 {
		base.Telegram.AllowedUserIDs = overlay.Telegram.AllowedUserIDs
	}

	// TUI
	if overlay.TUI.AutoApprove {
		base.TUI.AutoApprove = true
	}
	if overlay.TUI.Theme != "" {
		base.TUI.Theme = overlay.TUI.Theme
	}

	// Agent
	if overlay.Agent.MaxIterations > 0 {
		base.Agent.MaxIterations = overlay.Agent.MaxIterations
	}
	if overlay.Agent.SystemPrompt != "" {
		base.Agent.SystemPrompt = overlay.Agent.SystemPrompt
	}
	if overlay.Agent.Personality != "" {
		base.Agent.Personality = overlay.Agent.Personality
	}
	if overlay.Agent.PersistentRules != "" {
		base.Agent.PersistentRules = overlay.Agent.PersistentRules
	}
	if overlay.Agent.Mode != "" {
		base.Agent.Mode = overlay.Agent.Mode
	}

	// Store
	if overlay.Store.Path != "" {
		base.Store.Path = overlay.Store.Path
	}

	// Memory
	if overlay.Memory.Enabled {
		base.Memory.Enabled = true
	}
	if overlay.Memory.StorageType != "" {
		base.Memory.StorageType = overlay.Memory.StorageType
	}
	if overlay.Memory.StorageDir != "" {
		base.Memory.StorageDir = overlay.Memory.StorageDir
	}
	if overlay.Memory.EmbeddingModel != "" {
		base.Memory.EmbeddingModel = overlay.Memory.EmbeddingModel
	}
	if overlay.Memory.EmbeddingBaseURL != "" {
		base.Memory.EmbeddingBaseURL = overlay.Memory.EmbeddingBaseURL
	}
	if overlay.Memory.OpenAIAPIKey != "" {
		base.Memory.OpenAIAPIKey = overlay.Memory.OpenAIAPIKey
	}
	if overlay.Memory.FactExtraction {
		base.Memory.FactExtraction = true
	}
	if overlay.Memory.SimilarityThreshold > 0 {
		base.Memory.SimilarityThreshold = overlay.Memory.SimilarityThreshold
	}
	if overlay.Memory.ConsolidationInterval > 0 {
		base.Memory.ConsolidationInterval = overlay.Memory.ConsolidationInterval
	}
	if overlay.Memory.BM25Weight > 0 {
		base.Memory.BM25Weight = overlay.Memory.BM25Weight
	}
	if overlay.Memory.VectorWeight > 0 {
		base.Memory.VectorWeight = overlay.Memory.VectorWeight
	}
	if overlay.Memory.VectorDimension > 0 {
		base.Memory.VectorDimension = overlay.Memory.VectorDimension
	}

	// Tools.Bash
	if overlay.Tools.Bash.Timeout > 0 {
		base.Tools.Bash.Timeout = overlay.Tools.Bash.Timeout
	}
	if overlay.Tools.Bash.RequiresApproval {
		base.Tools.Bash.RequiresApproval = true
	}
	if len(overlay.Tools.Bash.BlockedCommands) > 0 {
		base.Tools.Bash.BlockedCommands = append(base.Tools.Bash.BlockedCommands, overlay.Tools.Bash.BlockedCommands...)
	}

	// Tools.File
	if overlay.Tools.File.RequiresApproval {
		base.Tools.File.RequiresApproval = true
	}

	// Tools.HTTP
	if overlay.Tools.HTTP.Timeout > 0 {
		base.Tools.HTTP.Timeout = overlay.Tools.HTTP.Timeout
	}
	if overlay.Tools.HTTP.RequiresApproval {
		base.Tools.HTTP.RequiresApproval = true
	}

	// Tools.MCP — merge server maps
	if len(overlay.Tools.MCP.Servers) > 0 {
		if base.Tools.MCP.Servers == nil {
			base.Tools.MCP.Servers = make(map[string]MCPServerConfig)
		}
		for k, v := range overlay.Tools.MCP.Servers {
			base.Tools.MCP.Servers[k] = v
		}
	}

	// Tools.ConcurrentExecution
	if overlay.Tools.ConcurrentExecution.MaxConcurrency > 0 {
		base.Tools.ConcurrentExecution.MaxConcurrency = overlay.Tools.ConcurrentExecution.MaxConcurrency
	}

	// Server
	if overlay.Server.Addr != "" {
		base.Server.Addr = overlay.Server.Addr
	}
	if overlay.Server.Enabled {
		base.Server.Enabled = true
	}

	// Log
	if overlay.Log.Level != "" {
		base.Log.Level = overlay.Log.Level
	}
	if overlay.Log.Format != "" {
		base.Log.Format = overlay.Log.Format
	}

	// Skills
	if len(overlay.Skills.ExtraDirs) > 0 {
		base.Skills.ExtraDirs = append(base.Skills.ExtraDirs, overlay.Skills.ExtraDirs...)
	}

	// Agents
	if len(overlay.Agents.ExtraDirs) > 0 {
		base.Agents.ExtraDirs = append(base.Agents.ExtraDirs, overlay.Agents.ExtraDirs...)
	}
	if len(overlay.Agents.Definitions) > 0 {
		base.Agents.Definitions = append(base.Agents.Definitions, overlay.Agents.Definitions...)
	}

	// Permissions — deny rules are merged via MergePermissionRules in rules.go
	if overlay.Permissions.Default != "" {
		base.Permissions.Default = overlay.Permissions.Default
	}
	if len(overlay.Permissions.Rules) > 0 {
		base.Permissions.Rules = MergePermissionRules(base.Permissions.Rules, overlay.Permissions.Rules)
	}

	// Hooks — append handlers from overlay
	if len(overlay.Hooks.PreToolUse) > 0 {
		base.Hooks.PreToolUse = append(base.Hooks.PreToolUse, overlay.Hooks.PreToolUse...)
	}
	if len(overlay.Hooks.PostToolUse) > 0 {
		base.Hooks.PostToolUse = append(base.Hooks.PostToolUse, overlay.Hooks.PostToolUse...)
	}
	if len(overlay.Hooks.OnUserMessage) > 0 {
		base.Hooks.OnUserMessage = append(base.Hooks.OnUserMessage, overlay.Hooks.OnUserMessage...)
	}
	if len(overlay.Hooks.PreCompact) > 0 {
		base.Hooks.PreCompact = append(base.Hooks.PreCompact, overlay.Hooks.PreCompact...)
	}
}
