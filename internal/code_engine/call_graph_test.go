package code_engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCallGraph(t *testing.T) {
	cg := NewCallGraph()
	if cg == nil {
		t.Fatal("expected non-nil CallGraph")
	}
	if cg.nodes == nil {
		t.Error("expected nodes map to be initialized")
	}
}

func TestCallGraph_NodeCount_Empty(t *testing.T) {
	cg := NewCallGraph()
	if cg.NodeCount() != 0 {
		t.Errorf("expected 0 nodes, got %d", cg.NodeCount())
	}
}

func TestCallGraph_AnalyzeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testdata.go")
	code := `package test

func caller() {
    callee1()
    callee2()
}

func callee1() {}

func callee2() {}
`
	os.WriteFile(path, []byte(code), 0644)

	cg := NewCallGraph()
	if err := cg.AnalyzeFile(path); err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}

	// Nodes: caller->callerID, callee1->calleeID, callee2->calleeID
	// Plus callee1->FuncDecl, callee2->FuncDecl = 5 because callee as call target
	// uses "-1" line number while its FuncDecl uses actual line number.
	if cg.NodeCount() != 5 {
		t.Errorf("expected 5 nodes (3 FuncDecls + 2 call-target refs), got %d", cg.NodeCount())
	}

	if cg.EdgeCount() != 2 {
		t.Errorf("expected 2 edges, got %d", cg.EdgeCount())
	}
}

func TestCallGraph_AnalyzeFile_WithReceiver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receiver.go")
	code := `package test

type Service struct{}

func (s *Service) Process() {
    s.validate()
    s.save()
}

func (s *Service) validate() {}
func (s *Service) save() {}
`
	os.WriteFile(path, []byte(code), 0644)

	cg := NewCallGraph()
	if err := cg.AnalyzeFile(path); err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}

	// 3 FuncDecls + 2 call targets (validate, save) = 5
	if cg.NodeCount() != 5 {
		t.Errorf("expected 5 nodes, got %d", cg.NodeCount())
	}

	// Process() should call validate() and save() = 2 edges
	if cg.EdgeCount() != 2 {
		t.Errorf("expected 2 edges, got %d", cg.EdgeCount())
	}
}

func TestCallGraph_AnalyzeFile_ExternalCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external.go")
	code := `package test

import "fmt"

func myFunc() {
    fmt.Println("hello")
    internalHelper()
}

func internalHelper() {}
`
	os.WriteFile(path, []byte(code), 0644)

	cg := NewCallGraph()
	if err := cg.AnalyzeFile(path); err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}

	// myFunc(FuncDecl), internalHelper(FuncDecl), fmt.Println(external), internalHelper(call-target) = 4
	if cg.NodeCount() != 4 {
		t.Errorf("expected 4 nodes, got %d", cg.NodeCount())
	}
}

func TestCallGraph_ImpactAnalysis(t *testing.T) {
	cg := NewCallGraph()

	// Manually set up a call graph
	cg.ensureNode("main.go:topLevel:1", &Symbol{Name: "topLevel", FilePath: "main.go", LineStart: 1})
	cg.ensureNode("main.go:middleA:10", &Symbol{Name: "middleA", FilePath: "main.go", LineStart: 10})
	cg.ensureNode("main.go:middleB:20", &Symbol{Name: "middleB", FilePath: "main.go", LineStart: 20})
	cg.ensureNode("main.go:leaf:30", &Symbol{Name: "leaf", FilePath: "main.go", LineStart: 30})

	// Wire up: leaf -> middleA, leaf -> middleB, middleA -> topLevel
	cg.mu.Lock()
	cg.nodes["main.go:topLevel:1"].Callees = []string{"main.go:middleA:10"}
	cg.nodes["main.go:middleA:10"].Callers = []string{"main.go:topLevel:1"}
	cg.nodes["main.go:middleA:10"].Callees = []string{"main.go:leaf:30"}
	cg.nodes["main.go:leaf:30"].Callers = []string{"main.go:middleA:10"}
	cg.nodes["main.go:middleB:20"].Callees = []string{"main.go:leaf:30"}
	cg.nodes["main.go:leaf:30"].Callers = append(cg.nodes["main.go:leaf:30"].Callers, "main.go:middleB:20")
	cg.edges = append(cg.edges, &CallEdge{CallerID: "main.go:topLevel:1", CalleeID: "main.go:middleA:10"})
	cg.edges = append(cg.edges, &CallEdge{CallerID: "main.go:middleA:10", CalleeID: "main.go:leaf:30"})
	cg.edges = append(cg.edges, &CallEdge{CallerID: "main.go:middleB:20", CalleeID: "main.go:leaf:30"})
	cg.mu.Unlock()

	report := cg.ImpactAnalysis("leaf", 3)
	if report.TotalAffected < 3 {
		t.Errorf("expected at least 3 affected functions, got %d: %v", report.TotalAffected, report.AffectedFunctions)
	}
	if report.DirectCallers != 2 {
		t.Errorf("expected 2 direct callers (middleA, middleB), got %d", report.DirectCallers)
	}
}

func TestCallGraph_ImpactAnalysis_MaxDepth(t *testing.T) {
	cg := NewCallGraph()

	cg.ensureNode("a:1", &Symbol{Name: "a"})
	cg.ensureNode("b:10", &Symbol{Name: "b"})
	cg.ensureNode("c:20", &Symbol{Name: "c"})
	cg.ensureNode("d:30", &Symbol{Name: "d"})

	cg.mu.Lock()
	cg.nodes["a:1"].Callees = []string{"b:10"}
	cg.nodes["b:10"].Callers = []string{"a:1"}
	cg.nodes["b:10"].Callees = []string{"c:20"}
	cg.nodes["c:20"].Callers = []string{"b:10"}
	cg.nodes["c:20"].Callees = []string{"d:30"}
	cg.nodes["d:30"].Callers = []string{"c:20"}
	cg.mu.Unlock()

	report := cg.ImpactAnalysis("d", 1)
	if report.TotalAffected != 1 {
		t.Errorf("expected 1 affected at depth 1, got %d (affected: %v)", report.TotalAffected, report.AffectedFunctions)
	}

	report = cg.ImpactAnalysis("d", 3)
	if report.TotalAffected != 3 {
		t.Errorf("expected 3 affected at depth 3, got %d", report.TotalAffected)
	}
}

func TestCallGraph_ImpactAnalysis_NotFound(t *testing.T) {
	cg := NewCallGraph()
	report := cg.ImpactAnalysis("nonexistent", 3)
	if report.TotalAffected != 0 {
		t.Errorf("expected 0 affected for nonexistent function, got %d", report.TotalAffected)
	}
}

func TestCallGraph_EdgeCount(t *testing.T) {
	cg := NewCallGraph()

	if cg.EdgeCount() != 0 {
		t.Errorf("expected 0 edges initially, got %d", cg.EdgeCount())
	}

	cg.mu.Lock()
	cg.edges = append(cg.edges, &CallEdge{CallerID: "a", CalleeID: "b"})
	cg.mu.Unlock()

	if cg.EdgeCount() != 1 {
		t.Errorf("expected 1 edge, got %d", cg.EdgeCount())
	}
}

func TestCallGraph_EnsureNode(t *testing.T) {
	cg := NewCallGraph()
	cg.ensureNode("id1", &Symbol{Name: "func1"})
	cg.ensureNode("id2", &Symbol{Name: "func2"})
	// ensureNode should be idempotent
	cg.ensureNode("id1", &Symbol{Name: "func1"})

	if cg.NodeCount() != 2 {
		t.Errorf("expected 2 nodes, got %d", cg.NodeCount())
	}
}

func TestCallGraph_AnalyzeDir(t *testing.T) {
	cg := NewCallGraph()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package test
func foo() { bar() }
func bar() {}
`), 0644)

	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte(`package test
func baz() {}
`), 0644)

	// Create sub directory
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte(`package test
func baz() {}
`), 0644)

	if err := cg.AnalyzeDir(dir); err != nil {
		t.Fatalf("AnalyzeDir: %v", err)
	}

	if cg.NodeCount() == 0 {
		t.Error("expected nodes after AnalyzeDir")
	}
}

func TestCallNode_ID(t *testing.T) {
	n := &CallNode{Symbol: &Symbol{Name: "test", FilePath: "file.go", LineStart: 10}}
	id := n.ID()
	if id != "file.go:test:10" {
		t.Errorf("expected 'file.go:test:10', got %q", id)
	}

	n2 := &CallNode{}
	if n2.ID() != "" {
		t.Errorf("expected empty ID for nil symbol, got %q", n2.ID())
	}
}
