package code_engine

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// SymbolKind classifies a code symbol.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindStruct    SymbolKind = "struct"
	KindInterface SymbolKind = "interface"
	KindVariable  SymbolKind = "variable"
	KindConstant  SymbolKind = "constant"
	KindImport    SymbolKind = "import"
	KindClass     SymbolKind = "class"
)

// Symbol represents a named code entity.
type Symbol struct {
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	FilePath  string     `json:"file_path"`
	LineStart int        `json:"line_start"`
	LineEnd   int        `json:"line_end"`
	Package   string     `json:"package"`
	Signature string     `json:"signature"`
	Exported  bool       `json:"exported"`
	Language  string     `json:"language"`
}

// SymbolIndex indexes code symbols for fast search.
type SymbolIndex struct {
	symbols map[string][]*Symbol // filePath → symbols
	byName  map[string][]*Symbol // name → symbols
	mu      sync.RWMutex
}

// NewSymbolIndex creates an empty symbol index.
func NewSymbolIndex() *SymbolIndex {
	return &SymbolIndex{
		symbols: make(map[string][]*Symbol),
		byName:  make(map[string][]*Symbol),
	}
}

// IndexFile extracts symbols from a single file.
func (si *SymbolIndex) IndexFile(filePath string) ([]*Symbol, error) {
	lang := detectLanguage(filePath)

	var symbols []*Symbol
	var err error

	switch lang {
	case "go":
		symbols, err = si.indexGoFile(filePath)
	case "python":
		symbols, err = si.indexPythonFile(filePath)
	case "rust":
		symbols, err = si.indexRustFile(filePath)
	case "typescript", "javascript":
		symbols, err = si.indexTSFile(filePath)
	default:
		symbols, err = si.indexGeneric(filePath, lang)
	}

	if err != nil {
		return nil, err
	}

	si.mu.Lock()
	si.symbols[filePath] = symbols
	for _, sym := range symbols {
		si.byName[sym.Name] = append(si.byName[sym.Name], sym)
	}
	si.mu.Unlock()

	return symbols, nil
}

// IndexDir recursively indexes all source files in a directory.
func (si *SymbolIndex) IndexDir(dirPath string) (int, error) {
	var count int
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		lang := detectLanguage(path)
		if lang == "unknown" {
			return nil
		}
		syms, err := si.IndexFile(path)
		if err != nil {
			return nil // skip files that fail to parse
		}
		count += len(syms)
		return nil
	})
	return count, err
}

// SearchByName finds symbols matching a name (case-insensitive partial match).
func (si *SymbolIndex) SearchByName(query string) []*Symbol {
	si.mu.RLock()
	defer si.mu.RUnlock()

	query = strings.ToLower(query)
	var results []*Symbol
	for name, syms := range si.byName {
		if strings.Contains(strings.ToLower(name), query) {
			results = append(results, syms...)
		}
	}
	return results
}

// SearchByKind finds all symbols of a given kind.
func (si *SymbolIndex) SearchByKind(kind SymbolKind) []*Symbol {
	si.mu.RLock()
	defer si.mu.RUnlock()

	var results []*Symbol
	for _, syms := range si.symbols {
		for _, s := range syms {
			if s.Kind == kind {
				results = append(results, s)
			}
		}
	}
	return results
}

// FindReferences finds all symbols that reference the given name.
func (si *SymbolIndex) FindReferences(name string) []*Symbol {
	// Simple: find symbols by name, plus check file contents for references
	return si.SearchByName(name)
}

// Stats returns index statistics.
func (si *SymbolIndex) Stats() IndexStats {
	si.mu.RLock()
	defer si.mu.RUnlock()

	var totalSymbols int
	files := len(si.symbols)
	uniqueNames := len(si.byName)
	for _, syms := range si.symbols {
		totalSymbols += len(syms)
	}

	return IndexStats{
		Files:        files,
		TotalSymbols: totalSymbols,
		UniqueNames:  uniqueNames,
	}
}

// IndexStats holds symbol index statistics.
type IndexStats struct {
	Files        int `json:"files"`
	TotalSymbols int `json:"total_symbols"`
	UniqueNames  int `json:"unique_names"`
}

// --- Go parser ---

func (si *SymbolIndex) indexGoFile(path string) ([]*Symbol, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse go: %w", err)
	}

	pkgName := f.Name.Name
	var symbols []*Symbol

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := &Symbol{
				Name:      d.Name.Name,
				Kind:      KindFunction,
				FilePath:  path,
				LineStart: fset.Position(d.Pos()).Line,
				LineEnd:   fset.Position(d.End()).Line,
				Package:   pkgName,
				Exported:  ast.IsExported(d.Name.Name),
				Language:  "go",
				Signature: funcSignature(d),
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				sym.Kind = KindMethod
				if recv := receiverType(d.Recv.List[0].Type); recv != "" {
					sym.Name = recv + "." + d.Name.Name
				}
			}
			symbols = append(symbols, sym)

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind := KindStruct
					if _, ok := s.Type.(*ast.InterfaceType); ok {
						kind = KindInterface
					}
					symbols = append(symbols, &Symbol{
						Name:      s.Name.Name,
						Kind:      kind,
						FilePath:  path,
						LineStart: fset.Position(s.Pos()).Line,
						LineEnd:   fset.Position(s.End()).Line,
						Package:   pkgName,
						Exported:  ast.IsExported(s.Name.Name),
						Language:  "go",
					})
				}
			}
		}
	}

	return symbols, nil
}

func funcSignature(fn *ast.FuncDecl) string {
	var parts []string
	parts = append(parts, "func", fn.Name.Name)
	parts = append(parts, "(")
	if fn.Type.Params != nil {
		for i, p := range fn.Type.Params.List {
			if i > 0 {
				parts = append(parts, ", ")
			}
			for _, n := range p.Names {
				parts = append(parts, n.Name, " ")
			}
			parts = append(parts, typeString(p.Type))
		}
	}
	parts = append(parts, ")")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		parts = append(parts, " ")
		if len(fn.Type.Results.List) > 1 {
			parts = append(parts, "(")
		}
		for i, r := range fn.Type.Results.List {
			if i > 0 {
				parts = append(parts, ", ")
			}
			parts = append(parts, typeString(r.Type))
		}
		if len(fn.Type.Results.List) > 1 {
			parts = append(parts, ")")
		}
	}
	return strings.Join(parts, "")
}

func receiverType(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return "*" + typeString(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

func typeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + typeString(e.X)
	case *ast.SelectorExpr:
		return typeString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeString(e.Elt)
	case *ast.MapType:
		return "map[" + typeString(e.Key) + "]" + typeString(e.Value)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// --- Regex-based parsers for other languages ---

var pyFuncPattern = regexp.MustCompile(`(?m)^\s*def\s+(\w+)\s*\((.*?)\)`)
var pyClassPattern = regexp.MustCompile(`(?m)^\s*class\s+(\w+)`)
var rustFnPattern = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+(\w+)\s*[<(]([^)]*)[>)]`)
var rustStructPattern = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?struct\s+(\w+)`)
var tsFnPattern = regexp.MustCompile(`(?m)(?:function\s+(\w+)|(\w+)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>|(\w+)\s*\([^)]*\)\s*\{)`)

func (si *SymbolIndex) indexPythonFile(path string) ([]*Symbol, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(content)
	var symbols []*Symbol

	for _, m := range pyFuncPattern.FindAllStringSubmatch(text, -1) {
		symbols = append(symbols, &Symbol{
			Name:      m[1],
			Kind:      KindFunction,
			FilePath:  path,
			Signature: "def " + m[1] + "(" + m[2] + ")",
			Language:  "python",
		})
	}
	for _, m := range pyClassPattern.FindAllStringSubmatch(text, -1) {
		symbols = append(symbols, &Symbol{
			Name:     m[1],
			Kind:     KindClass,
			FilePath: path,
			Language: "python",
		})
	}
	return symbols, nil
}

func (si *SymbolIndex) indexRustFile(path string) ([]*Symbol, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(content)
	var symbols []*Symbol

	for _, m := range rustFnPattern.FindAllStringSubmatch(text, -1) {
		symbols = append(symbols, &Symbol{
			Name:      m[1],
			Kind:      KindFunction,
			FilePath:  path,
			Signature: "fn " + m[1] + "(" + m[2] + ")",
			Language:  "rust",
		})
	}
	for _, m := range rustStructPattern.FindAllStringSubmatch(text, -1) {
		symbols = append(symbols, &Symbol{
			Name:     m[1],
			Kind:     KindStruct,
			FilePath: path,
			Language: "rust",
		})
	}
	return symbols, nil
}

func (si *SymbolIndex) indexTSFile(path string) ([]*Symbol, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(content)
	var symbols []*Symbol

	for _, m := range tsFnPattern.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" {
			name = m[3]
		}
		if name != "" {
			symbols = append(symbols, &Symbol{
				Name:     name,
				Kind:     KindFunction,
				FilePath: path,
				Language: "typescript",
			})
		}
	}
	return symbols, nil
}

func (si *SymbolIndex) indexGeneric(path, lang string) ([]*Symbol, error) {
	return nil, nil
}

// detectLanguage guesses the language from file extension.
func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".rs":
		return "rust"
	case ".ts", ".tsx", ".js", ".jsx":
		return "typescript"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	default:
		return "unknown"
	}
}
