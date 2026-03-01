package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SkillContentProvider is the interface the skill manager must satisfy.
// Defined here to avoid importing the skill package (no circular deps).
type SkillContentProvider interface {
	GetContent(name string) (string, error)
	ListNames() []string
}

// SkillTool implements progressive disclosure for skills.
// The agent sees only skill metadata in the system prompt; when it decides
// to activate a skill, it calls this tool to lazily load the full instructions.
type SkillTool struct {
	provider SkillContentProvider
}

type skillInput struct {
	Action string `json:"action"` // read, list
	Name   string `json:"name"`   // skill name (for read)
}

func NewSkillTool(provider SkillContentProvider) *SkillTool {
	return &SkillTool{provider: provider}
}

func (s *SkillTool) Name() string { return "read_skill" }
func (s *SkillTool) Description() string {
	return "Read a skill's full instructions by name. Use this to activate a skill after seeing its metadata in the system prompt."
}
func (s *SkillTool) RequiresApproval() bool { return false }

func (s *SkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"read", "list"},
				"description": "read: get full instructions for a skill; list: show all available skill names",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "The skill name to read (required for action=read)",
			},
		},
		"required": []string{"action"},
	}
}

func (s *SkillTool) Execute(_ context.Context, input []byte) (Result, error) {
	var in skillInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch in.Action {
	case "list":
		names := s.provider.ListNames()
		if len(names) == 0 {
			return Result{Output: "No skills available."}, nil
		}
		return Result{Output: strings.Join(names, "\n")}, nil

	case "read":
		if in.Name == "" {
			return Result{Error: "name is required for action=read"}, nil
		}
		content, err := s.provider.GetContent(in.Name)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		return Result{Output: content}, nil

	default:
		return Result{Error: fmt.Sprintf("unknown action: %s (use read or list)", in.Action)}, nil
	}
}
