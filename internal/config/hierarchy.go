package config

import (
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigLevel represents a configuration source priority level.
type ConfigLevel int

const (
	LevelSystem  ConfigLevel = iota // /etc/ironclaw/ — admin global rules
	LevelUser                       // ~/.ironclaw/config.yaml — user defaults
	LevelProject                    // .ironclaw/ironclaw.yaml — project-specific
	LevelLocal                      // .ironclaw/local.yaml — local override (gitignored)
)

func (l ConfigLevel) String() string {
	switch l {
	case LevelSystem:
		return "system"
	case LevelUser:
		return "user"
	case LevelProject:
		return "project"
	case LevelLocal:
		return "local"
	default:
		return "unknown"
	}
}

// HierarchySource represents a single config source with its level.
type HierarchySource struct {
	Level ConfigLevel
	Path  string
	Found bool
}

// LoadHierarchy loads configuration from all 4 levels and merges them.
// Priority: Local > Project > User > System
// Returns the merged config and the list of sources that were found.
func LoadHierarchy(workDir string) (*Config, []HierarchySource, error) {
	sources := discoverSources(workDir)

	// Start with defaults
	cfg := defaultConfig()

	// Apply each level in order (lowest priority first)
	for i := range sources {
		if !sources[i].Found {
			continue
		}
		slog.Info("config: loading level", "level", sources[i].Level.String(), "path", sources[i].Path)

		overlay, err := loadSingleYAML(sources[i].Path)
		if err != nil {
			slog.Warn("config: failed to load level", "level", sources[i].Level.String(), "err", err)
			continue
		}
		mergeConfig(&cfg, overlay)
	}

	return &cfg, sources, nil
}

// discoverSources finds config files at each level.
func discoverSources(workDir string) []HierarchySource {
	home, _ := os.UserHomeDir()

	sources := []HierarchySource{
		{Level: LevelSystem, Path: "/etc/ironclaw/config.yaml"},
		{Level: LevelUser, Path: filepath.Join(home, ".ironclaw", "config.yaml")},
		{Level: LevelProject, Path: filepath.Join(workDir, ".ironclaw", "ironclaw.yaml")},
		{Level: LevelLocal, Path: filepath.Join(workDir, ".ironclaw", "local.yaml")},
	}

	for i := range sources {
		if _, err := os.Stat(sources[i].Path); err == nil {
			sources[i].Found = true
		}
	}

	return sources
}

// loadSingleYAML reads and parses a single YAML config file.
func loadSingleYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := ExpandEnv(data)
	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

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

	// Agent.Team
	if overlay.Agent.Team.Enabled {
		base.Agent.Team.Enabled = true
	}
	if overlay.Agent.Team.MaxWorkers > 0 {
		base.Agent.Team.MaxWorkers = overlay.Agent.Team.MaxWorkers
	}
	if overlay.Agent.Team.Model != "" {
		base.Agent.Team.Model = overlay.Agent.Team.Model
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

	// Knowledge
	if overlay.Knowledge.Enabled {
		base.Knowledge.Enabled = true
	}
	if overlay.Knowledge.ChunkSize > 0 {
		base.Knowledge.ChunkSize = overlay.Knowledge.ChunkSize
	}
	if overlay.Knowledge.ChunkOverlap > 0 {
		base.Knowledge.ChunkOverlap = overlay.Knowledge.ChunkOverlap
	}
	if overlay.Knowledge.BM25Weight > 0 {
		base.Knowledge.BM25Weight = overlay.Knowledge.BM25Weight
	}
	if overlay.Knowledge.VectorWeight > 0 {
		base.Knowledge.VectorWeight = overlay.Knowledge.VectorWeight
	}
	if overlay.Knowledge.GraphEnabled {
		base.Knowledge.GraphEnabled = true
	}
	if len(overlay.Knowledge.IngestDirs) > 0 {
		base.Knowledge.IngestDirs = overlay.Knowledge.IngestDirs
	}

	// Graph
	if overlay.Graph.Enabled {
		base.Graph.Enabled = true
	}

	// Scheduler
	if overlay.Scheduler.Enabled {
		base.Scheduler.Enabled = true
	}
	if overlay.Scheduler.PollInterval > 0 {
		base.Scheduler.PollInterval = overlay.Scheduler.PollInterval
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

	// Tools.Browser
	if overlay.Tools.Browser.Timeout > 0 {
		base.Tools.Browser.Timeout = overlay.Tools.Browser.Timeout
	}
	if overlay.Tools.Browser.RequiresApproval {
		base.Tools.Browser.RequiresApproval = true
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

	// Dashboard
	if overlay.Dashboard.Enabled {
		base.Dashboard.Enabled = true
	}
	if overlay.Dashboard.Addr != "" {
		base.Dashboard.Addr = overlay.Dashboard.Addr
	}
	if overlay.Dashboard.Token != "" {
		base.Dashboard.Token = overlay.Dashboard.Token
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

	// Sandbox
	if overlay.Sandbox.Enabled {
		base.Sandbox.Enabled = true
	}
	if len(overlay.Sandbox.AllowedDirectories) > 0 {
		base.Sandbox.AllowedDirectories = append(base.Sandbox.AllowedDirectories, overlay.Sandbox.AllowedDirectories...)
	}
	if len(overlay.Sandbox.ReadonlyDirectories) > 0 {
		base.Sandbox.ReadonlyDirectories = append(base.Sandbox.ReadonlyDirectories, overlay.Sandbox.ReadonlyDirectories...)
	}
	if overlay.Sandbox.Bash.Backend != "" {
		base.Sandbox.Bash.Backend = overlay.Sandbox.Bash.Backend
	}
	if overlay.Sandbox.Network.Mode != "" {
		base.Sandbox.Network.Mode = overlay.Sandbox.Network.Mode
	}
	if len(overlay.Sandbox.Network.Blacklist) > 0 {
		base.Sandbox.Network.Blacklist = append(base.Sandbox.Network.Blacklist, overlay.Sandbox.Network.Blacklist...)
	}
	if len(overlay.Sandbox.Network.Whitelist) > 0 {
		base.Sandbox.Network.Whitelist = append(base.Sandbox.Network.Whitelist, overlay.Sandbox.Network.Whitelist...)
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

// overlayHierarchy applies project-level and local-level configs on top of an
// already-loaded base config. Called by Load() to transparently support the
// hierarchical config without changing call sites.
func overlayHierarchy(base *Config, workDir string) {
	projectPath := filepath.Join(workDir, ".ironclaw", "ironclaw.yaml")
	localPath := filepath.Join(workDir, ".ironclaw", "local.yaml")

	for _, p := range []string{projectPath, localPath} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		overlay, err := loadSingleYAML(p)
		if err != nil {
			slog.Warn("config: failed to load overlay", "path", p, "err", err)
			continue
		}
		slog.Info("config: applying overlay", "path", p)
		mergeConfig(base, overlay)
	}
}
