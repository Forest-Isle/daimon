package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/cortex"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
)

// CognitiveAgentOptions bundles all optional dependencies for the cognitive agent.
// Fields left nil are silently skipped (feature not enabled).
type CognitiveAgentOptions struct {
	EntityExtractor     *graph.LLMEntityExtractor
	CodebaseIndex       *CodebaseIndex
	KnowledgeSearcher   knowledge.Searcher
	KnowledgeGraph      graph.Graph
	TreePlanner         *StrategicTreePlanner
	MCTSPlanner         *MCTSPlanner
	EvolutionEngine     *evolution.Engine
	MemoryNotifyFunc    MemoryNotifyFunc
	CheckpointStore     CheckpointStore
	ObservationCallback func(result *ObservationResult)
	ApprovalFunc        ApprovalFunc
	PlanMode            *PlanMode
	DebateConfig        config.DebateSettings
	CortexRetriever     *cortex.UnifiedRetriever
}
