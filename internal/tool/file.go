package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type FileTool struct {
	approval bool
}

type fileInput struct {
	Action  string `json:"action"`  // read, write, list
	Path    string `json:"path"`
	Content string `json:"content"` // for write
}

func NewFileTool(requiresApproval bool) *FileTool {
	return &FileTool{approval: requiresApproval}
}

func (f *FileTool) Name() string        { return "file" }
func (f *FileTool) Description() string  { return "Read, write, or list files and directories." }
func (f *FileTool) RequiresApproval() bool { return f.approval }

func (f *FileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"read", "write", "list"},
				"description": "The file operation to perform",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory path",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write (only for write action)",
			},
		},
		"required": []string{"action", "path"},
	}
}

func (f *FileTool) Execute(_ context.Context, input []byte) (Result, error) {
	var in fileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	switch in.Action {
	case "read":
		data, err := os.ReadFile(in.Path)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		output := string(data)
		if len(output) > maxOutputSize {
			output = output[:maxOutputSize] + "\n... (truncated)"
		}
		return Result{Output: output}, nil

	case "write":
		dir := filepath.Dir(in.Path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{Error: err.Error()}, nil
		}
		if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
			return Result{Error: err.Error()}, nil
		}
		return Result{Output: "file written: " + in.Path}, nil

	case "list":
		entries, err := os.ReadDir(in.Path)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		var listing string
		for _, e := range entries {
			prefix := "  "
			if e.IsDir() {
				prefix = "d "
			}
			listing += prefix + e.Name() + "\n"
		}
		return Result{Output: listing}, nil

	default:
		return Result{Error: "unknown action: " + in.Action}, nil
	}
}
