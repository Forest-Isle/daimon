package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ────────────────────────────── CoreDeps ──────────────────────────────

// CoreDeps holds the required dependencies to run an agent. Every field is mandatory.
type CoreDeps struct {
	Provider Provider
	Tools    *tool.Registry
	Sessions *session.Manager
	DB       *store.DB
	Cfg      config.AgentConfig
	LLMCfg   config.LLMConfig
	AgentID  string
	ToolsCfg config.ToolsConfig // concurrent tool execution config
}

// ────────────────────────────── MemoryDeps ──────────────────────────────

// MemoryDeps holds optional memory subsystem dependencies. Call WithDefaults()
// to fill nil interface fields with no-op implementations.
type MemoryDeps struct {
	Store         memory.Store             // default: memory.NoopStore()
	LifecycleMgr  *memory.LifecycleManager // nil = NOOP lifecycle decisions
	ContextMgr    ContextManager           // default: noopContextManager{}
	FactExtractor *memory.LLMFactExtractor // nil = no extraction
	BaseDir       string                   // base directory for file-based memory storage
}

// WithDefaults returns a copy of MemoryDeps with nil interface fields filled.
func (d MemoryDeps) WithDefaults() MemoryDeps {
	if d.Store == nil {
		d.Store = memory.NoopStore()
	}
	if d.ContextMgr == nil {
		d.ContextMgr = noopContextManager{}
	}
	return d
}

// ────────────────────────────── SecurityDeps ──────────────────────────────

// SecurityDeps holds optional security subsystem dependencies.
type SecurityDeps struct {
	Interceptor  *tool.InterceptorChain // nil = passthrough (tools execute directly)
	HookMgr      *hook.Manager          // nil = no hooks
	PermEngine   *tool.PermissionEngine // nil = allow-all
}

// WithDefaults returns a copy of SecurityDeps with nil fields filled.
// Automatically wires HookInterceptor and PermissionInterceptor into the chain
// when HookMgr or PermEngine are non-nil and Interceptor has not been explicitly set.
func (d SecurityDeps) WithDefaults() SecurityDeps {
	if d.Interceptor == nil {
		var interceptors []tool.ToolInterceptor
		if d.PermEngine != nil {
			interceptors = append(interceptors, tool.NewPermissionInterceptor(d.PermEngine))
		}
		if d.HookMgr != nil {
			interceptors = append(interceptors, tool.NewHookInterceptor(d.HookMgr))
		}
		d.Interceptor = tool.NewInterceptorChain(interceptors)
	}
	return d
}

// ────────────────────────────── ObservabilityDeps ──────────────────────────────

// ObservabilityDeps holds optional observability subsystem dependencies.
type ObservabilityDeps struct {
	Emitter        ObservabilityEmitter // default: discardEmitter{}
	MetricsEmitter MetricsEmitter   // default: discardMetrics{}
}

// WithDefaults returns a copy of ObservabilityDeps with nil interface fields filled.
func (d ObservabilityDeps) WithDefaults() ObservabilityDeps {
	if d.Emitter == nil {
		d.Emitter = discardEmitter{}
	}
	if d.MetricsEmitter == nil {
		d.MetricsEmitter = discardMetrics{}
	}
	return d
}

// ────────────────────────────── MultiAgentDeps ──────────────────────────────

// MultiAgentDeps holds optional multi-agent subsystem dependencies.
// Nil fields mean the feature is disabled.
type MultiAgentDeps struct {
	SkillMgr     *skill.Manager
	AgentMgr     *AgentManager
	SubAgentMgr  *SubAgentManager // nil = no sub-agents
	AgentMCP     *AgentMCPManager
	ResultStore  *tool.ResultStore
	TaskLedger   taskledger.TaskLedger // default: taskledger.NoopTaskLedger()
	BgManager    *BackgroundManager    // nil = disabled
	PromptCache  *PromptCache          // nil = disabled
}

// WithDefaults returns a copy of MultiAgentDeps with nil interface fields filled.
func (d MultiAgentDeps) WithDefaults() MultiAgentDeps {
	if d.TaskLedger == nil {
		d.TaskLedger = taskledger.NoopTaskLedger()
	}
	return d
}

// ────────────────────────────── AgentDeps ──────────────────────────────

// AgentDeps is the complete dependency bundle for all agent types (Runtime,
// CognitiveAgent, SubAgentManager). Construct once in Gateway.New() and share.
type AgentDeps struct {
	Core          CoreDeps
	Memory        MemoryDeps
	Security      SecurityDeps
	Observability ObservabilityDeps
	MultiAgent    MultiAgentDeps
}

// WithDefaults calls WithDefaults() on each sub-struct, filling nil interfaces with no-ops.
func (d AgentDeps) WithDefaults() AgentDeps {
	d.Memory = d.Memory.WithDefaults()
	d.Security = d.Security.WithDefaults()
	d.Observability = d.Observability.WithDefaults()
	d.MultiAgent = d.MultiAgent.WithDefaults()
	return d
}
