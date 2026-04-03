package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
	"gopkg.in/yaml.v3"
)

// AgentManager loads, validates, and registers sub-agent specs as tools.
type AgentManager struct {
	mu        sync.RWMutex
	specs     []*AgentSpec
	provider  Provider
	sessions  *session.Manager
	db        *store.DB
	memStore  memory.Store
	tools     *tool.Registry
	cfg       config.AgentConfig
	llmCfg    config.LLMConfig
	bgManager *BackgroundManager
	agentMCP  *AgentMCPManager
}

// NewAgentManager creates a new AgentManager.
func NewAgentManager(
	provider Provider,
	sessions *session.Manager,
	db *store.DB,
	memStore memory.Store,
	tools *tool.Registry,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *AgentManager {
	return &AgentManager{
		provider: provider,
		sessions: sessions,
		db:       db,
		memStore: memStore,
		tools:    tools,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}
}

// SetBackgroundManager sets the background manager for all agent tools.
func (m *AgentManager) SetBackgroundManager(bm *BackgroundManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bgManager = bm
}

// SetAgentMCPManager sets the per-agent MCP manager for all agent tools.
func (m *AgentManager) SetAgentMCPManager(mgr *AgentMCPManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentMCP = mgr
}

// Add adds an inline AgentSpec definition.
func (m *AgentManager) Add(spec *AgentSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deduplicate by name
	for i, s := range m.specs {
		if s.Name == spec.Name {
			m.specs[i] = spec
			slog.Info("agent_manager: updated existing agent spec", "name", spec.Name)
			return nil
		}
	}

	m.specs = append(m.specs, spec)
	slog.Info("agent_manager: added agent spec", "name", spec.Name)
	return nil
}

// LoadDir loads all agent spec YAML files from the given directory.
func (m *AgentManager) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // directory doesn't exist yet, that's fine
		}
		return fmt.Errorf("agent_manager: read dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(dir, name)
		spec, err := loadAgentSpec(path)
		if err != nil {
			slog.Warn("agent_manager: skip invalid spec",
				"file", name, "err", err)
			continue
		}

		if err := m.Add(spec); err != nil {
			slog.Warn("agent_manager: skip invalid spec",
				"file", name, "err", err)
			continue
		}
	}

	return nil
}

// RegisterAll creates AgentTool instances for each spec and registers them in the tool registry.
func (m *AgentManager) RegisterAll(registry *tool.Registry) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, spec := range m.specs {
		at := NewAgentTool(spec, m.provider, m.sessions, m.db, m.memStore, m.tools, m.cfg, m.llmCfg)
		registry.Register(at)
		if m.bgManager != nil {
			at.SetBackgroundManager(m.bgManager)
		}
		if m.agentMCP != nil {
			at.SetAgentMCPManager(m.agentMCP)
		}
		slog.Info("agent_manager: registered agent tool",
			"name", at.Name(),
			"tools", spec.Tools,
			"max_iterations", spec.MaxIterations,
		)
	}
}

// All returns all loaded agent specs.
func (m *AgentManager) All() []*AgentSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*AgentSpec, len(m.specs))
	copy(out, m.specs)
	return out
}

// BuildPromptSection generates a text section describing available sub-agents
// for injection into the orchestrator's system prompt.
func (m *AgentManager) BuildPromptSection() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.specs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Agents\n\n")
	sb.WriteString("You can delegate tasks to specialized agents using the corresponding agent_* tools.\n")
	sb.WriteString("Each agent runs independently with its own tool set and iteration budget.\n")
	sb.WriteString("Pass context from previous tasks via the \"context\" field to enable pipeline collaboration.\n\n")
	sb.WriteString("Execution modes: spawn (independent), fork (inherits conversation context), background (async).\n\n")

	for _, spec := range m.specs {
		sb.WriteString(fmt.Sprintf("- **agent_%s**: %s", spec.Name, spec.Description))
		if spec.ExecutionMode != "" && spec.ExecutionMode != ExecModeSpawn {
			sb.WriteString(fmt.Sprintf(" [mode: %s]", spec.ExecutionMode))
		}
		if len(spec.Tags) > 0 {
			sb.WriteString(fmt.Sprintf(" [tags: %s]", strings.Join(spec.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// loadAgentSpec reads and parses a single YAML agent spec file.
func loadAgentSpec(path string) (*AgentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Expand environment variables
	data = config.ExpandEnv(data)

	var spec AgentSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &spec, nil
}
