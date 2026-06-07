package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// ScorerSpec is the declarative, JSON-serializable form of a Scorer. It is
// materialized into a concrete Scorer by BuildScorers.
type ScorerSpec struct {
	Type    string `json:"type"`              // file_exists | file_contains | output_contains | command_succeeds
	Path    string `json:"path,omitempty"`    // for file_* scorers
	Substr  string `json:"substr,omitempty"`  // for *_contains scorers
	Command string `json:"command,omitempty"` // for command_succeeds
}

// Task is a single eval case: a prompt to give the agent plus the scorers that
// determine success. Setup, if non-empty, is run (via sh -c) in the task's
// workdir before the agent starts — e.g. to scaffold a broken file to fix.
type Task struct {
	ID      string       `json:"id"`
	Prompt  string       `json:"prompt"`
	Setup   string       `json:"setup,omitempty"`
	Scorers []ScorerSpec `json:"scorers"`
}

// LoadTask reads and parses a single task JSON file. It does not validate
// scorer types — call BuildScorers for that.
func LoadTask(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read task: %w", err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", path, err)
	}
	if t.ID == "" {
		return nil, fmt.Errorf("task %s: missing id", path)
	}
	return &t, nil
}

// BuildScorers materializes each ScorerSpec into a concrete Scorer, returning an
// error if any spec names an unknown type.
func (t *Task) BuildScorers() ([]Scorer, error) {
	scorers := make([]Scorer, 0, len(t.Scorers))
	for i, spec := range t.Scorers {
		switch spec.Type {
		case "file_exists":
			scorers = append(scorers, FileExists{Path: spec.Path})
		case "file_contains":
			scorers = append(scorers, FileContains{Path: spec.Path, Substr: spec.Substr})
		case "output_contains":
			scorers = append(scorers, OutputContains{Substr: spec.Substr})
		case "command_succeeds":
			scorers = append(scorers, CommandSucceeds{Command: spec.Command})
		default:
			return nil, fmt.Errorf("task %s scorer[%d]: unknown type %q", t.ID, i, spec.Type)
		}
	}
	return scorers, nil
}
