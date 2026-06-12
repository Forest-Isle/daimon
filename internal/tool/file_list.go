package tool

import (
	"context"
	"encoding/json"
	"os"
)

// FileListTool lists files and directories in a given path.
type FileListTool struct{}

func NewFileListTool() *FileListTool { return &FileListTool{} }

func (t *FileListTool) Name() string           { return "file_list" }
func (t *FileListTool) Description() string    { return "List files and directories in a given path." }
func (t *FileListTool) RequiresApproval() bool { return false }
func (t *FileListTool) IsReadOnly() bool       { return true }

func (t *FileListTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
	}
}

func (t *FileListTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Directory path to list",
			},
		},
		"required": []string{"path"},
	}
}

type fileListInput struct {
	Path string `json:"path"`
}

func (t *FileListTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in fileListInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if in.Path == "" {
		return Result{Error: "path is required"}, nil
	}

	path, err := ResolveWorkPath(ctx, in.Path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	entries, err := os.ReadDir(path)
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

	return Result{Output: listing, Type: ResultText}, nil
}
