package code_engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSymbolIndex(t *testing.T) {
	si := NewSymbolIndex()
	if si == nil {
		t.Fatal("expected non-nil SymbolIndex")
	}
	if si.symbols == nil {
		t.Error("expected symbols map to be initialized")
	}
	if si.byName == nil {
		t.Error("expected byName map to be initialized")
	}
}

func TestSymbolIndex_IndexGoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.go")
	code := `package testpkg

import "fmt"

// User represents a user in the system.
type User struct {
	Name string
	Age  int
}

// Greet returns a greeting for the user.
func (u *User) Greet() string {
	return "Hello, " + u.Name
}

// NewUser creates a new user.
func NewUser(name string, age int) *User {
	return &User{Name: name, Age: age}
}

// Stringer interface
type Stringer interface {
	String() string
}
`
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	si := NewSymbolIndex()
	symbols, err := si.IndexFile(path)
	if err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	if len(symbols) == 0 {
		t.Fatal("expected symbols to be extracted")
	}

	// Verify specific symbols
	found := make(map[string]bool)
	for _, sym := range symbols {
		found[sym.Name] = true
		switch sym.Name {
		case "User":
			if sym.Kind != KindStruct {
				t.Errorf("User: expected KindStruct, got %s", sym.Kind)
			}
			if !sym.Exported {
				t.Error("User should be exported")
			}
		case "*User.Greet":
			if sym.Kind != KindMethod {
				t.Errorf("Greet: expected KindMethod, got %s", sym.Kind)
			}
		case "NewUser":
			if sym.Kind != KindFunction {
				t.Errorf("NewUser: expected KindFunction, got %s", sym.Kind)
			}
		case "Stringer":
			if sym.Kind != KindInterface {
				t.Errorf("Stringer: expected KindInterface, got %s", sym.Kind)
			}
		}
	}

	expected := []string{"User", "*User.Greet", "NewUser", "Stringer"}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("expected symbol %q not found", name)
		}
	}

	// Verify All symbols have correct package
	for _, sym := range symbols {
		if sym.Package != "testpkg" {
			t.Errorf("symbol %q: expected package 'testpkg', got %q", sym.Name, sym.Package)
		}
	}
}

func TestSymbolIndex_IndexGoFile_FuncSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sig.go")
	code := `package sig

func Add(a, b int) int {
	return a + b
}

func (s *Service) Process(ctx context.Context, req Request) (Response, error) {
	return Response{}, nil
}
`
	os.WriteFile(path, []byte(code), 0644)

	si := NewSymbolIndex()
	symbols, err := si.IndexFile(path)
	if err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	for _, sym := range symbols {
		if sym.Name == "Add" {
			if sym.Signature == "" {
				t.Error("Add should have signature")
			}
			if !strings.Contains(sym.Signature, "int") {
				t.Errorf("Add signature should contain 'int': %s", sym.Signature)
			}
		}
	}
}

func TestSymbolIndex_IndexGoFile_NotGo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	os.WriteFile(path, []byte("just text"), 0644)

	si := NewSymbolIndex()
	symbols, err := si.IndexFile(path)
	if err != nil {
		t.Fatalf("IndexFile: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols for unknown file type, got %d", len(symbols))
	}
}

func TestSymbolIndex_SearchByName(t *testing.T) {
	si := NewSymbolIndex()

	// Manually add symbols
	sym1 := &Symbol{Name: "UserService", Kind: KindInterface, FilePath: "svc.go"}
	sym2 := &Symbol{Name: "userHandler", Kind: KindFunction, FilePath: "handler.go"}
	sym3 := &Symbol{Name: "User", Kind: KindStruct, FilePath: "model.go"}

	si.mu.Lock()
	si.symbols["svc.go"] = append(si.symbols["svc.go"], sym1)
	si.byName["UserService"] = append(si.byName["UserService"], sym1)
	si.symbols["handler.go"] = append(si.symbols["handler.go"], sym2)
	si.byName["userHandler"] = append(si.byName["userHandler"], sym2)
	si.symbols["model.go"] = append(si.symbols["model.go"], sym3)
	si.byName["User"] = append(si.byName["User"], sym3)
	si.mu.Unlock()

	tests := []struct {
		query string
		want  int
	}{
		{"User", 3},    // UserService + User + userHandler (case-insensitive)
		{"user", 3},    // case-insensitive: all 3
		{"Handler", 1}, // userHandler
		{"NonExistent", 0},
		{"", 3}, // Empty matches everything
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results := si.SearchByName(tt.query)
			if len(results) != tt.want {
				t.Errorf("SearchByName(%q) = %d results, want %d", tt.query, len(results), tt.want)
			}
		})
	}
}

func TestSymbolIndex_SearchByKind(t *testing.T) {
	si := NewSymbolIndex()

	syms := []*Symbol{
		{Name: "Foo", Kind: KindFunction, FilePath: "a.go"},
		{Name: "Bar", Kind: KindFunction, FilePath: "a.go"},
		{Name: "Baz", Kind: KindStruct, FilePath: "b.go"},
		{Name: "MyInterface", Kind: KindInterface, FilePath: "c.go"},
	}
	for _, s := range syms {
		si.mu.Lock()
		si.symbols[s.FilePath] = append(si.symbols[s.FilePath], s)
		si.byName[s.Name] = append(si.byName[s.Name], s)
		si.mu.Unlock()
	}

	if len(si.SearchByKind(KindFunction)) != 2 {
		t.Errorf("expected 2 functions, got %d", len(si.SearchByKind(KindFunction)))
	}
	if len(si.SearchByKind(KindStruct)) != 1 {
		t.Errorf("expected 1 struct, got %d", len(si.SearchByKind(KindStruct)))
	}
	if len(si.SearchByKind(KindInterface)) != 1 {
		t.Errorf("expected 1 interface, got %d", len(si.SearchByKind(KindInterface)))
	}
	if len(si.SearchByKind(KindVariable)) != 0 {
		t.Errorf("expected 0 variables, got %d", len(si.SearchByKind(KindVariable)))
	}
}

func TestSymbolIndex_FindReferences(t *testing.T) {
	si := NewSymbolIndex()
	si.mu.Lock()
	si.byName["Logger"] = []*Symbol{
		{Name: "Logger", Kind: KindInterface, FilePath: "log.go"},
		{Name: "Logger", Kind: KindVariable, FilePath: "main.go"},
	}
	si.mu.Unlock()

	refs := si.FindReferences("Logger")
	if len(refs) != 2 {
		t.Errorf("expected 2 references, got %d", len(refs))
	}
}

func TestSymbolIndex_Stats(t *testing.T) {
	si := NewSymbolIndex()
	stats := si.Stats()
	if stats.Files != 0 || stats.TotalSymbols != 0 {
		t.Errorf("expected empty stats, got %+v", stats)
	}

	si.mu.Lock()
	si.symbols["a.go"] = []*Symbol{{Name: "A", Kind: KindFunction}}
	si.byName["A"] = []*Symbol{{Name: "A", Kind: KindFunction}}
	si.mu.Unlock()

	stats = si.Stats()
	if stats.Files != 1 {
		t.Errorf("expected 1 file, got %d", stats.Files)
	}
	if stats.TotalSymbols != 1 {
		t.Errorf("expected 1 symbol, got %d", stats.TotalSymbols)
	}
}

func TestSymbolIndex_IndexDir(t *testing.T) {
	si := NewSymbolIndex()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`
package main
func main() {}
func helper() int { return 0 }
`), 0644)

	os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`
package main
type Config struct{}
func process(c Config) {}
`), 0644)

	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not code"), 0644)

	count, err := si.IndexDir(dir)
	if err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	// main.go: main + helper, lib.go: Config + process = 4 symbols
	if count != 4 {
		t.Errorf("expected 4 symbols, got %d", count)
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path string
		lang string
	}{
		{"file.go", "go"},
		{"file.py", "python"},
		{"file.pyi", "python"},
		{"file.rs", "rust"},
		{"file.ts", "typescript"},
		{"file.tsx", "typescript"},
		{"file.js", "typescript"},
		{"file.jsx", "typescript"},
		{"file.java", "java"},
		{"file.c", "c"},
		{"file.h", "c"},
		{"file.cpp", "cpp"},
		{"file.hpp", "cpp"},
		{"file.txt", "unknown"},
		{"file.md", "unknown"},
		{"Makefile", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectLanguage(tt.path)
			if got != tt.lang {
				t.Errorf("detectLanguage(%q) = %q, want %q", tt.path, got, tt.lang)
			}
		})
	}
}

func TestIndexPythonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.py")
	code := `
def hello(name):
    print(f"Hello {name}")

class MyClass:
    def method(self):
        pass
`
	os.WriteFile(path, []byte(code), 0644)

	si := NewSymbolIndex()
	symbols, err := si.indexPythonFile(path)
	if err != nil {
		t.Fatalf("indexPythonFile: %v", err)
	}
	if len(symbols) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d", len(symbols))
	}

	foundFn := false
	foundClass := false
	for _, s := range symbols {
		if s.Name == "hello" && s.Kind == KindFunction && s.Language == "python" {
			foundFn = true
		}
		if s.Name == "MyClass" && s.Kind == KindClass && s.Language == "python" {
			foundClass = true
		}
	}
	if !foundFn {
		t.Error("expected 'hello' function")
	}
	if !foundClass {
		t.Error("expected 'MyClass' class")
	}
}

func TestIndexRustFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.rs")
	code := `
pub fn calculate(x: i32) -> i32 {
    x * 2
}

struct Config {
    name: String,
}
`
	os.WriteFile(path, []byte(code), 0644)

	si := NewSymbolIndex()
	symbols, err := si.indexRustFile(path)
	if err != nil {
		t.Fatalf("indexRustFile: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestIndexTSFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	code := `
function greet(name: string): string {
    return "Hello " + name;
}

const handler = (req: Request) => {
    return new Response("ok");
}
`
	os.WriteFile(path, []byte(code), 0644)

	si := NewSymbolIndex()
	symbols, err := si.indexTSFile(path)
	if err != nil {
		t.Fatalf("indexTSFile: %v", err)
	}
	if len(symbols) < 2 {
		t.Errorf("expected at least 2 symbols, got %d", len(symbols))
	}
}

func TestIndexGeneric(t *testing.T) {
	si := NewSymbolIndex()
	symbols, err := si.indexGeneric("/path/to/file.java", "java")
	if err != nil {
		t.Fatalf("indexGeneric: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols from generic indexer, got %d", len(symbols))
	}
}
