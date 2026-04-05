package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FileReadTool reads the contents of a file with optional offset/limit and line numbers.
type FileReadTool struct{}

func NewFileReadTool() *FileReadTool { return &FileReadTool{} }

func (t *FileReadTool) Name() string        { return "file_read" }
func (t *FileReadTool) Description() string { return "Read the contents of a file. Returns line-numbered output." }
func (t *FileReadTool) RequiresApproval() bool { return false }
func (t *FileReadTool) IsReadOnly() bool       { return true }

func (t *FileReadTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
	}
}

func (t *FileReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "1-based line number to start reading from (optional)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to return (optional)",
			},
		},
		"required": []string{"path"},
	}
}

type fileReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *FileReadTool) Execute(_ context.Context, input []byte) (Result, error) {
	var in fileReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Path == "" {
		return Result{Error: "path is required"}, nil
	}

	data, err := os.ReadFile(in.Path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	lines := strings.Split(string(data), "\n")
	// Remove trailing empty line from final newline split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Apply offset (1-based)
	start := 0
	if in.Offset > 0 {
		start = in.Offset - 1
	}
	if start >= len(lines) {
		return Result{Output: "", Type: ResultFile, FilePath: in.Path}, nil
	}
	lines = lines[start:]

	// Apply limit
	if in.Limit > 0 && in.Limit < len(lines) {
		lines = lines[:in.Limit]
	}

	// Format with line numbers (cat -n style: right-justified line number, tab, content)
	var sb strings.Builder
	lineNum := start + 1
	for _, line := range lines {
		sb.WriteString(fmt.Sprintf("%6d\t%s\n", lineNum, line))
		lineNum++
	}

	output := sb.String()
	isPartial := false
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n[truncated]"
		isPartial = true
	}

	return Result{
		Output:    output,
		Type:      ResultFile,
		FilePath:  in.Path,
		IsPartial: isPartial,
	}, nil
}
