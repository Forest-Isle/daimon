package agent

import (
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

func TestContextBudgetAllocator_Simple(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexitySimple)

	if budget.MemoryLimit != 3 {
		t.Errorf("MemoryLimit = %d, want 3", budget.MemoryLimit)
	}
	if budget.KBLimit != 0 {
		t.Errorf("KBLimit = %d, want 0", budget.KBLimit)
	}
	if !budget.IncludeProjectContext {
		t.Error("IncludeProjectContext = false, want true")
	}
	if budget.IncludeGitState {
		t.Error("IncludeGitState = true, want false")
	}
}

func TestContextBudgetAllocator_Moderate(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexityModerate)

	if budget.MemoryLimit != 5 {
		t.Errorf("MemoryLimit = %d, want 5", budget.MemoryLimit)
	}
	if budget.KBLimit != 3 {
		t.Errorf("KBLimit = %d, want 3", budget.KBLimit)
	}
	if !budget.IncludeProjectContext {
		t.Error("IncludeProjectContext = false, want true")
	}
	if budget.IncludeGitState {
		t.Error("IncludeGitState = true, want false")
	}
}

func TestContextBudgetAllocator_Complex(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(ComplexityComplex)

	if budget.MemoryLimit != 10 {
		t.Errorf("MemoryLimit = %d, want 10", budget.MemoryLimit)
	}
	if budget.KBLimit != 5 {
		t.Errorf("KBLimit = %d, want 5", budget.KBLimit)
	}
	if !budget.IncludeProjectContext {
		t.Error("IncludeProjectContext = false, want true")
	}
	if !budget.IncludeGitState {
		t.Error("IncludeGitState = false, want true")
	}
}

func TestContextBudgetAllocator_UnknownDefaultsToModerate(t *testing.T) {
	alloc := NewContextBudgetAllocator()
	budget := alloc.Allocate(TaskComplexity("unknown"))

	moderate := alloc.Allocate(ComplexityModerate)
	if budget != moderate {
		t.Errorf("unknown complexity budget %+v != moderate budget %+v", budget, moderate)
	}
}

func TestApplyBudget_TruncatesMemories(t *testing.T) {
	alloc := NewContextBudgetAllocator()

	memories := make([]memory.SearchResult, 10)
	for i := range memories {
		memories[i] = memory.SearchResult{Score: 1.0, Entry: memory.Entry{Content: "test"}}
	}

	kb := make([]string, 5)
	for i := range kb {
		kb[i] = "kb snippet"
	}

	state := &CognitiveState{
		Goal:             Goal{Complexity: ComplexitySimple},
		RelevantMemories: memories,
		KnowledgeContext: kb,
		ProjectCtx:       &ProjectContext{Name: "proj"},
		GitState:         &GitState{Branch: "main"},
	}

	alloc.Apply(state)

	if len(state.RelevantMemories) != 3 {
		t.Errorf("RelevantMemories len = %d, want 3", len(state.RelevantMemories))
	}
	if len(state.KnowledgeContext) != 0 {
		t.Errorf("KnowledgeContext len = %d, want 0", len(state.KnowledgeContext))
	}
	if state.ProjectCtx == nil {
		t.Error("ProjectCtx should be preserved for simple complexity")
	}
	if state.GitState != nil {
		t.Error("GitState should be nil for simple complexity")
	}
}

func TestApplyBudget_ComplexPreservesAll(t *testing.T) {
	alloc := NewContextBudgetAllocator()

	memories := make([]memory.SearchResult, 8)
	for i := range memories {
		memories[i] = memory.SearchResult{Score: 1.0, Entry: memory.Entry{Content: "test"}}
	}

	state := &CognitiveState{
		Goal:             Goal{Complexity: ComplexityComplex},
		RelevantMemories: memories,
		KnowledgeContext: []string{"a", "b", "c"},
		ProjectCtx:       &ProjectContext{Name: "proj"},
		GitState:         &GitState{Branch: "main"},
	}

	alloc.Apply(state)

	if len(state.RelevantMemories) != 8 {
		t.Errorf("RelevantMemories len = %d, want 8 (under limit of 10)", len(state.RelevantMemories))
	}
	if len(state.KnowledgeContext) != 3 {
		t.Errorf("KnowledgeContext len = %d, want 3 (under limit of 5)", len(state.KnowledgeContext))
	}
	if state.ProjectCtx == nil {
		t.Error("ProjectCtx should be preserved for complex complexity")
	}
	if state.GitState == nil {
		t.Error("GitState should be preserved for complex complexity")
	}
}
