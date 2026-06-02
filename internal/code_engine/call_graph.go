package code_engine

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
)

// CallGraph represents function call relationships.
type CallGraph struct {
	nodes map[string]*CallNode // functionID → node
	edges []*CallEdge
	mu    sync.RWMutex
}

// CallNode represents a function in the call graph.
type CallNode struct {
	Symbol  *Symbol  `json:"symbol"`
	Callers []string `json:"callers"` // function IDs that call this node
	Callees []string `json:"callees"` // function IDs this node calls
}

func (n *CallNode) ID() string {
	if n.Symbol == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%d", n.Symbol.FilePath, n.Symbol.Name, n.Symbol.LineStart)
}

// CallEdge represents a single call relationship.
type CallEdge struct {
	CallerID string `json:"caller_id"`
	CalleeID string `json:"callee_id"`
	Caller   string `json:"caller"`
	Callee   string `json:"callee"`
	Location string `json:"location"` // "file.go:42"
}

// NewCallGraph creates an empty call graph.
func NewCallGraph() *CallGraph {
	return &CallGraph{
		nodes: make(map[string]*CallNode),
	}
}

// AnalyzeFile builds call graph edges from a Go source file.
func (cg *CallGraph) AnalyzeFile(filePath string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return fmt.Errorf("parse go: %w", err)
	}

	pkgName := f.Name.Name

	// Build function name set for this file
	funcNames := make(map[string]bool)
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			funcNames[fd.Name.Name] = true
		}
	}

	// Extract calls from each function
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		callerID := fmt.Sprintf("%s:%s:%d", filePath, fd.Name.Name, fset.Position(fd.Pos()).Line)
		cg.ensureNode(callerID, &Symbol{
			Name: fd.Name.Name, FilePath: filePath,
			LineStart: fset.Position(fd.Pos()).Line, Package: pkgName,
		})

		// Find all function calls in the body
		calls := extractCalls(fd, fset)
		for _, call := range calls {
			calleeID := ""
			// Try to resolve within same package
			if funcNames[call] {
				calleeID = fmt.Sprintf("%s:%s:-1", filePath, call)
			} else {
				calleeID = fmt.Sprintf("external:%s", call)
			}

			cg.ensureNode(calleeID, &Symbol{Name: call, Package: "external"})

			edge := &CallEdge{
				CallerID: callerID,
				CalleeID: calleeID,
				Caller:   fd.Name.Name,
				Callee:   call,
				Location: fmt.Sprintf("%s:-1", filePath),
			}

			cg.mu.Lock()
			cg.edges = append(cg.edges, edge)
			if node := cg.nodes[callerID]; node != nil {
				node.Callees = append(node.Callees, calleeID)
			}
			if node := cg.nodes[calleeID]; node != nil {
				node.Callers = append(node.Callers, callerID)
			}
			cg.mu.Unlock()
		}
	}

	return nil
}

// ImpactAnalysis finds all functions affected by a change to the given function.
func (cg *CallGraph) ImpactAnalysis(functionName string, maxDepth int) *ImpactReport {
	cg.mu.RLock()
	defer cg.mu.RUnlock()

	report := &ImpactReport{Target: functionName, MaxDepth: maxDepth}

	// BFS from the target function following callers
	visited := make(map[string]bool)
	queue := []string{}

	// Find all node IDs matching the function name
	var firstNodeID string
	for id, node := range cg.nodes {
		if node.Symbol != nil && node.Symbol.Name == functionName {
			queue = append(queue, id)
			visited[id] = true
			if firstNodeID == "" {
				firstNodeID = id
			}
		}
	}

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var nextQueue []string
		for _, currentID := range queue {
			node := cg.nodes[currentID]
			if node == nil {
				continue
			}
			report.AffectedFunctions = append(report.AffectedFunctions, node.Symbol.Name)

			for _, callerID := range node.Callers {
				if !visited[callerID] {
					visited[callerID] = true
					nextQueue = append(nextQueue, callerID)
				}
			}
		}
		queue = nextQueue
	}

	// Count direct callers from the first matching node
	if firstNodeID != "" {
		if firstNode := cg.nodes[firstNodeID]; firstNode != nil {
			report.DirectCallers = len(firstNode.Callers)
		}
	}
	report.TotalAffected = len(report.AffectedFunctions)

	return report
}

// ImpactReport summarizes the blast radius of a code change.
type ImpactReport struct {
	Target            string   `json:"target"`
	MaxDepth          int      `json:"max_depth"`
	DirectCallers     int      `json:"direct_callers"`
	TotalAffected     int      `json:"total_affected"`
	AffectedFunctions []string `json:"affected_functions"`
}

func (cg *CallGraph) ensureNode(id string, sym *Symbol) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	if _, exists := cg.nodes[id]; !exists {
		cg.nodes[id] = &CallNode{Symbol: sym}
	}
}

// EdgeCount returns the total number of call edges.
func (cg *CallGraph) EdgeCount() int {
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	return len(cg.edges)
}

// NodeCount returns the total number of nodes.
func (cg *CallGraph) NodeCount() int {
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	return len(cg.nodes)
}

// extractCalls walks the AST and collects function call names.
func extractCalls(fd *ast.FuncDecl, fset *token.FileSet) []string {
	var calls []string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, fn.Name)
		case *ast.SelectorExpr:
			if x, ok := fn.X.(*ast.Ident); ok {
				calls = append(calls, x.Name+"."+fn.Sel.Name)
			}
		}
		return true
	})
	return calls
}

// AnalyzeDir recursively analyzes all Go files in a directory.
func (cg *CallGraph) AnalyzeDir(dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			base := entry.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				continue
			}
			cg.AnalyzeDir(filepathJoin(dirPath, base))
			continue
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			cg.AnalyzeFile(filepathJoin(dirPath, entry.Name()))
		}
	}
	return nil
}

func filepathJoin(a, b string) string {
	return a + "/" + b
}
