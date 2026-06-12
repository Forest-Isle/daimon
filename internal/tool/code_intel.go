package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const codeIntelTimeout = 5 * time.Second

type GrepCodeTool struct{ workingDir string }

func NewGrepCodeTool(workingDir string) *GrepCodeTool {
	return &GrepCodeTool{workingDir: workingDir}
}

func (t *GrepCodeTool) Name() string { return "grep_code" }
func (t *GrepCodeTool) Description() string {
	return "Search code with grep and return file:line:content matches."
}
func (t *GrepCodeTool) RequiresApproval() bool { return false }
func (t *GrepCodeTool) IsReadOnly() bool       { return true }
func (t *GrepCodeTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
		ParallelSafety:  ParallelSafe,
	}
}

func (t *GrepCodeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional subdirectory to search",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Optional file glob pattern such as *.go or *.{ts,tsx}",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matches to return",
			},
		},
		"required": []string{"pattern"},
	}
}

type grepCodeInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Include    string `json:"include"`
	MaxResults int    `json:"max_results"`
}

func (t *GrepCodeTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in grepCodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Pattern == "" {
		return Result{Error: "pattern is required"}, nil
	}

	searchPath, err := t.resolvePath(in.Path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	args := []string{"-rnI", "--binary-files=without-match", "-E", in.Pattern}
	if in.Include != "" {
		args = append(args, "--include", in.Include)
	}
	args = append(args, searchPath)

	stdout, stderr, err := runCodeIntelCommand(ctx, t.workingDir, "grep", args...)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return Result{
				Output: "",
				Type:   ResultText,
				Metadata: map[string]any{
					"match_count":    0,
					"returned_count": 0,
				},
			}, nil
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Result{Error: msg}, nil
	}

	lines := splitNonEmptyLines(stdout)
	totalMatches := len(lines)
	returned := lines
	if len(returned) > maxResults {
		returned = returned[:maxResults]
	}

	output := strings.Join(returned, "\n")
	if output != "" {
		output += "\n"
	}

	result := Result{
		Output: output,
		Type:   ResultText,
		Metadata: map[string]any{
			"match_count":    totalMatches,
			"returned_count": len(returned),
			"path":           searchPath,
		},
	}
	if len(output) > maxOutputSize {
		result.Output = output[:maxOutputSize] + "\n[truncated]"
		result.IsPartial = true
	}
	return result, nil
}

type FindSymbolTool struct{ workingDir string }

func NewFindSymbolTool(workingDir string) *FindSymbolTool {
	return &FindSymbolTool{workingDir: workingDir}
}

func (t *FindSymbolTool) Name() string { return "find_symbol" }
func (t *FindSymbolTool) Description() string {
	return "Find likely symbol definitions across a codebase."
}
func (t *FindSymbolTool) RequiresApproval() bool { return false }
func (t *FindSymbolTool) IsReadOnly() bool       { return true }
func (t *FindSymbolTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
		ParallelSafety:  ParallelSafe,
	}
}

func (t *FindSymbolTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Symbol name to find. Partial matches are supported.",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Optional file glob pattern such as *.go",
			},
			"kind": map[string]any{
				"type":        "string",
				"description": "Optional symbol kind: function, type, var, any",
			},
		},
		"required": []string{"name"},
	}
}

type findSymbolInput struct {
	Name    string `json:"name"`
	Include string `json:"include"`
	Kind    string `json:"kind"`
}

func (t *FindSymbolTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in findSymbolInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Name == "" {
		return Result{Error: "name is required"}, nil
	}

	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = "any"
	}

	pattern, err := buildFindSymbolPattern(in.Name, kind)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	args := []string{"-rnI", "--binary-files=without-match", "-E", pattern}
	if in.Include != "" {
		args = append(args, "--include", in.Include)
	}
	args = append(args, t.workingDir)

	stdout, stderr, err := runCodeIntelCommand(ctx, t.workingDir, "grep", args...)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return Result{
				Output: "",
				Type:   ResultText,
				Metadata: map[string]any{
					"match_count": 0,
					"kind":        kind,
				},
			}, nil
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Result{Error: msg}, nil
	}

	rawLines := splitNonEmptyLines(stdout)
	formatted := make([]string, 0, len(rawLines))
	matches := make([]map[string]any, 0, len(rawLines))
	for _, line := range rawLines {
		matchKind := detectSymbolKind(line)
		formatted = append(formatted, fmt.Sprintf("%s %s", matchKind, line))
		matches = append(matches, map[string]any{
			"kind":  matchKind,
			"match": line,
		})
	}

	output := strings.Join(formatted, "\n")
	if output != "" {
		output += "\n"
	}

	result := Result{
		Output: output,
		Type:   ResultText,
		Metadata: map[string]any{
			"match_count": len(matches),
			"kind":        kind,
			"matches":     matches,
		},
	}
	if len(output) > maxOutputSize {
		result.Output = output[:maxOutputSize] + "\n[truncated]"
		result.IsPartial = true
	}
	return result, nil
}

type ListImportsTool struct{ workingDir string }

func NewListImportsTool(workingDir string) *ListImportsTool {
	return &ListImportsTool{workingDir: workingDir}
}

func (t *ListImportsTool) Name() string           { return "list_imports" }
func (t *ListImportsTool) Description() string    { return "List import statements from a source file." }
func (t *ListImportsTool) RequiresApproval() bool { return false }
func (t *ListImportsTool) IsReadOnly() bool       { return true }
func (t *ListImportsTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
		ParallelSafety:  ParallelSafe,
	}
}

func (t *ListImportsTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the source file",
			},
		},
		"required": []string{"file_path"},
	}
}

type listImportsInput struct {
	FilePath string `json:"file_path"`
}

func (t *ListImportsTool) Execute(_ context.Context, input []byte) (Result, error) {
	var in listImportsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.FilePath == "" {
		return Result{Error: "file_path is required"}, nil
	}

	resolvedPath, err := t.resolvePath(in.FilePath)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	imports := extractImports(resolvedPath, string(data))
	lines := make([]string, 0, len(imports))
	metadataImports := make([]map[string]any, 0, len(imports))
	for _, imp := range imports {
		lines = append(lines, fmt.Sprintf("%d:%s:%s", imp.Line, imp.Kind, imp.Module))
		metadataImports = append(metadataImports, map[string]any{
			"line":   imp.Line,
			"kind":   imp.Kind,
			"module": imp.Module,
			"raw":    imp.Raw,
		})
	}

	output := strings.Join(lines, "\n")
	if output != "" {
		output += "\n"
	}

	return Result{
		Output: output,
		Type:   ResultText,
		Metadata: map[string]any{
			"file_path": resolvedPath,
			"imports":   metadataImports,
		},
	}, nil
}

type importMatch struct {
	Line   int
	Kind   string
	Module string
	Raw    string
}

func runCodeIntelCommand(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, codeIntelTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("command timed out after %s", codeIntelTimeout)
	}
	return stdout.String(), stderr.String(), err
}

func (t *GrepCodeTool) resolvePath(path string) (string, error) {
	return resolveCodeIntelPath(t.workingDir, path)
}

func (t *ListImportsTool) resolvePath(path string) (string, error) {
	return resolveCodeIntelPath(t.workingDir, path)
}

func resolveCodeIntelPath(workingDir, path string) (string, error) {
	if path == "" {
		return resolvePathInRoot(workingDir, ".")
	}
	return resolvePathInRoot(workingDir, path)
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func buildFindSymbolPattern(name, kind string) (string, error) {
	quoted := regexp.QuoteMeta(name)
	ident := "[A-Za-z0-9_]*" + quoted + "[A-Za-z0-9_]*"

	switch kind {
	case "function":
		return strings.Join([]string{
			`^[[:space:]]*func[[:space:]]*(\([^)]*\)[[:space:]]*)?` + ident + `[[:space:]]*\(`,
			`^[[:space:]]*def[[:space:]]+` + ident + `[[:space:]]*\(`,
			`^[[:space:]]*function[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*(const|let|var)[[:space:]]+` + ident + `[[:space:]]*=[[:space:]]*(async[[:space:]]+)?function\b`,
			`^[[:space:]]*(const|let|var)[[:space:]]+` + ident + `[[:space:]]*=[[:space:]]*(\([^)]*\)|[A-Za-z0-9_,[:space:]]+)[[:space:]]*=>`,
		}, "|"), nil
	case "type":
		return strings.Join([]string{
			`^[[:space:]]*type[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*class[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*interface[[:space:]]+` + ident + `\b`,
		}, "|"), nil
	case "var":
		return strings.Join([]string{
			`^[[:space:]]*var[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*(const|let|var)[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*` + ident + `[[:space:]]*[:=]`,
		}, "|"), nil
	case "any":
		return strings.Join([]string{
			`^[[:space:]]*func[[:space:]]*(\([^)]*\)[[:space:]]*)?` + ident + `[[:space:]]*\(`,
			`^[[:space:]]*type[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*var[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*def[[:space:]]+` + ident + `[[:space:]]*\(`,
			`^[[:space:]]*class[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*function[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*(const|let|var)[[:space:]]+` + ident + `\b`,
			`^[[:space:]]*` + ident + `[[:space:]]*[:=]`,
		}, "|"), nil
	default:
		return "", fmt.Errorf("invalid kind: %s", kind)
	}
}

func detectSymbolKind(line string) string {
	content := line
	if parts := strings.SplitN(line, ":", 3); len(parts) == 3 {
		content = parts[2]
	}
	content = strings.TrimSpace(content)

	switch {
	case strings.HasPrefix(content, "func "), strings.HasPrefix(content, "def "), strings.HasPrefix(content, "function "):
		return "function"
	case strings.HasPrefix(content, "type "), strings.HasPrefix(content, "class "), strings.HasPrefix(content, "interface "):
		return "type"
	case strings.HasPrefix(content, "var "), strings.HasPrefix(content, "const "), strings.HasPrefix(content, "let "):
		return "var"
	default:
		return "any"
	}
}

func extractImports(path, content string) []importMatch {
	ext := strings.ToLower(filepath.Ext(path))
	lines := strings.Split(content, "\n")

	switch ext {
	case ".go":
		return extractGoImports(lines)
	case ".py":
		return extractPythonImports(lines)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return extractJSImports(lines)
	default:
		return extractGenericImports(lines)
	}
}

func extractGoImports(lines []string) []importMatch {
	singleRe := regexp.MustCompile(`^\s*import\s+"([^"]+)"`)
	blockStartRe := regexp.MustCompile(`^\s*import\s*\(\s*$`)
	blockItemRe := regexp.MustCompile(`^\s*(?:[A-Za-z0-9_.]+\s+)?"([^"]+)"`)

	var imports []importMatch
	inBlock := false
	for i, line := range lines {
		switch {
		case blockStartRe.MatchString(line):
			inBlock = true
		case inBlock && strings.TrimSpace(line) == ")":
			inBlock = false
		case inBlock:
			if matches := blockItemRe.FindStringSubmatch(line); len(matches) == 2 {
				imports = append(imports, importMatch{Line: i + 1, Kind: "import", Module: matches[1], Raw: strings.TrimSpace(line)})
			}
		default:
			if matches := singleRe.FindStringSubmatch(line); len(matches) == 2 {
				imports = append(imports, importMatch{Line: i + 1, Kind: "import", Module: matches[1], Raw: strings.TrimSpace(line)})
			}
		}
	}
	return imports
}

func extractPythonImports(lines []string) []importMatch {
	importRe := regexp.MustCompile(`^\s*import\s+(.+)$`)
	fromRe := regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_\.]+)\s+import\s+(.+)$`)

	var imports []importMatch
	for i, line := range lines {
		if matches := fromRe.FindStringSubmatch(line); len(matches) == 3 {
			imports = append(imports, importMatch{Line: i + 1, Kind: "from", Module: matches[1], Raw: strings.TrimSpace(line)})
			continue
		}
		if matches := importRe.FindStringSubmatch(line); len(matches) == 2 {
			for _, part := range strings.Split(matches[1], ",") {
				module := strings.Fields(strings.TrimSpace(part))
				if len(module) > 0 {
					imports = append(imports, importMatch{Line: i + 1, Kind: "import", Module: module[0], Raw: strings.TrimSpace(line)})
				}
			}
		}
	}
	return imports
}

func extractJSImports(lines []string) []importMatch {
	importRe := regexp.MustCompile(`^\s*import\s+.*?\s+from\s+["']([^"']+)["']`)
	sideEffectRe := regexp.MustCompile(`^\s*import\s+["']([^"']+)["']`)
	requireRe := regexp.MustCompile(`require\(\s*["']([^"']+)["']\s*\)`)

	var imports []importMatch
	for i, line := range lines {
		switch {
		case importRe.MatchString(line):
			matches := importRe.FindStringSubmatch(line)
			imports = append(imports, importMatch{Line: i + 1, Kind: "import", Module: matches[1], Raw: strings.TrimSpace(line)})
		case sideEffectRe.MatchString(line):
			matches := sideEffectRe.FindStringSubmatch(line)
			imports = append(imports, importMatch{Line: i + 1, Kind: "import", Module: matches[1], Raw: strings.TrimSpace(line)})
		case requireRe.MatchString(line):
			matches := requireRe.FindStringSubmatch(line)
			imports = append(imports, importMatch{Line: i + 1, Kind: "require", Module: matches[1], Raw: strings.TrimSpace(line)})
		}
	}
	return imports
}

func extractGenericImports(lines []string) []importMatch {
	genericRe := regexp.MustCompile(`^\s*(import|from)\s+(.+)$`)

	var imports []importMatch
	for i, line := range lines {
		if matches := genericRe.FindStringSubmatch(line); len(matches) == 3 {
			imports = append(imports, importMatch{Line: i + 1, Kind: matches[1], Module: strings.TrimSpace(matches[2]), Raw: strings.TrimSpace(line)})
		}
	}
	return imports
}
