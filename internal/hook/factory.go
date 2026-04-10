package hook

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// BuildManagerOpts holds optional dependencies for handler construction.
type BuildManagerOpts struct {
	DB *sql.DB
}

// HandlerConfig represents a handler configuration entry.
type HandlerConfig struct {
	Type   string
	Config map[string]any
}

// BuildManager creates a HookManager from configuration, registering handlers
// based on their type strings.
func BuildManager(
	preToolUse []HandlerConfig,
	postToolUse []HandlerConfig,
	onUserMessage []HandlerConfig,
	preCompact []HandlerConfig,
	opts *BuildManagerOpts,
) *Manager {
	m := NewManager()

	for _, cfg := range preToolUse {
		h, err := buildPreToolUseHandler(cfg)
		if err != nil {
			slog.Warn("hook: failed to build PreToolUse handler", "type", cfg.Type, "err", err)
			continue
		}
		m.RegisterPreToolUse(h)
	}

	for _, cfg := range postToolUse {
		h, err := buildPostToolUseHandler(cfg, opts)
		if err != nil {
			slog.Warn("hook: failed to build PostToolUse handler", "type", cfg.Type, "err", err)
			continue
		}
		m.RegisterPostToolUse(h)
	}

	for _, cfg := range onUserMessage {
		h, err := buildOnUserMessageHandler(cfg)
		if err != nil {
			slog.Warn("hook: failed to build OnUserMessage handler", "type", cfg.Type, "err", err)
			continue
		}
		m.RegisterOnUserMessage(h)
	}

	for _, cfg := range preCompact {
		h, err := buildPreCompactHandler(cfg)
		if err != nil {
			slog.Warn("hook: failed to build PreCompact handler", "type", cfg.Type, "err", err)
			continue
		}
		m.RegisterPreCompact(h)
	}

	return m
}

func buildPreToolUseHandler(cfg HandlerConfig) (PreToolUseHandler, error) {
	switch cfg.Type {
	case "safety_analyzer":
		patterns := extractStringSlice(cfg.Config, "block_patterns")
		return NewSafetyAnalyzerHandler(patterns), nil
	default:
		return nil, fmt.Errorf("unknown PreToolUse handler type: %s", cfg.Type)
	}
}

func buildOnUserMessageHandler(cfg HandlerConfig) (OnUserMessageHandler, error) {
	switch cfg.Type {
	case "git_context":
		return NewGitContextInjector(cfg.Config), nil
	case "workdir_context":
		return NewWorkdirContextInjector(cfg.Config), nil
	default:
		return nil, fmt.Errorf("unknown OnUserMessage handler type: %s", cfg.Type)
	}
}

func buildPostToolUseHandler(cfg HandlerConfig, opts *BuildManagerOpts) (PostToolUseHandler, error) {
	switch cfg.Type {
	case "audit_logger":
		return NewAuditLogHandler(), nil
	case "permission_audit":
		if opts != nil && opts.DB != nil {
			return NewPermissionAuditHandler(opts.DB), nil
		}
		return nil, fmt.Errorf("permission_audit handler requires database connection")
	default:
		return nil, fmt.Errorf("unknown PostToolUse handler type: %s", cfg.Type)
	}
}

func buildPreCompactHandler(cfg HandlerConfig) (PreCompactHandler, error) {
	switch cfg.Type {
	case "message_preserver":
		patterns := extractStringSlice(cfg.Config, "preserve_patterns")
		return NewMessagePreserver(patterns), nil
	default:
		return nil, fmt.Errorf("unknown PreCompact handler type: %s", cfg.Type)
	}
}

// extractStringSlice extracts a []string from a config map.
func extractStringSlice(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	val, ok := config[key]
	if !ok {
		return nil
	}
	switch v := val.(type) {
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return v
	default:
		return nil
	}
}
