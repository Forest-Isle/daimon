package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
	"gopkg.in/yaml.v3"
)

// AgentManager loads, validates, and registers sub-agent specs as tools.
type AgentManager struct {
	mu          sync.RWMutex
	specs       []*AgentSpec
	provider    mind.Provider
	sessions    *session.Manager
	db          *store.DB
	memStore    memory.Store
	tools       *tool.Registry
	cfg         config.AgentConfig
	llmCfg      config.LLMConfig
	bgManager   *BackgroundManager
	agentMCP    *AgentMCPManager
	subAgentMgr *SubAgentManager
}

// NewAgentManager creates a new AgentManager.
func NewAgentManager(
	provider mind.Provider,
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

// SetSubAgentManager sets the sub-agent manager for delegation support.
func (m *AgentManager) SetSubAgentManager(mgr *SubAgentManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subAgentMgr = mgr
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
		path := filepath.Join(dir, name)

		var spec *AgentSpec
		var loadErr error

		switch {
		case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
			spec, loadErr = loadAgentSpec(path)
		case strings.HasSuffix(name, ".md"):
			spec, loadErr = loadMarkdownAgentSpec(path)
		default:
			continue
		}

		if loadErr != nil {
			slog.Warn("agent_manager: skip invalid spec", "file", name, "err", loadErr)
			continue
		}
		if err := m.Add(spec); err != nil {
			slog.Warn("agent_manager: skip invalid spec", "file", name, "err", err)
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
		mgr := m.subAgentMgr
		if mgr == nil {
			slog.Warn("agent_manager: no SubAgentManager set, skipping registration", "name", spec.Name)
			continue
		}

		at := NewAgentTool(spec, mgr)
		registry.Register(at)
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

// Get returns a loaded agent spec by name.
func (m *AgentManager) Get(name string) (*AgentSpec, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, spec := range m.specs {
		if spec.Name == name {
			cp := *spec
			return &cp, true
		}
	}
	return nil, false
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
	sb.WriteString("Execution modes: spawn (independent) and background (async).\n\n")

	for _, spec := range m.specs {
		_, _ = fmt.Fprintf(&sb, "- **agent_%s**: %s", spec.Name, spec.Description)
		if spec.ExecutionMode != "" && spec.ExecutionMode != ExecModeSpawn {
			_, _ = fmt.Fprintf(&sb, " [mode: %s]", spec.ExecutionMode)
		}
		if len(spec.Tags) > 0 {
			_, _ = fmt.Fprintf(&sb, " [tags: %s]", strings.Join(spec.Tags, ", "))
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

// loadMarkdownAgentSpec reads a Markdown file with YAML frontmatter.
// The frontmatter fields map to AgentSpec; the Markdown body becomes SystemPrompt.
func loadMarkdownAgentSpec(path string) (*AgentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	data = config.ExpandEnv(data)
	content := string(data)

	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter %s: %w", path, err)
	}

	var spec AgentSpec
	if err := yaml.Unmarshal([]byte(frontmatter), &spec); err != nil {
		return nil, fmt.Errorf("parse yaml %s: %w", path, err)
	}
	spec.SystemPrompt = strings.TrimSpace(body)
	return &spec, nil
}

// splitFrontmatter splits "---\nyaml\n---\nmarkdown" into (yaml, body, error).
func splitFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", "", fmt.Errorf("no frontmatter found")
	}
	rest := content[3:]
	if i := strings.Index(rest, "\n"); i >= 0 {
		rest = rest[i+1:]
	}
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return "", "", fmt.Errorf("unclosed frontmatter")
	}
	return strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+3:]), nil
}
