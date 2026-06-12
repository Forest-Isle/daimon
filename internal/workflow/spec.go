package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type StepType string

const (
	StepTypeAgent StepType = "agent"
	StepTypeTool  StepType = "tool"
)

type Status string

const (
	StatusSuccess Status = "success"
	StatusError   Status = "error"
	StatusCached  Status = "cached"
)

type FailureStrategy string

const (
	FailureStop FailureStrategy = "stop"
	FailureBest FailureStrategy = "best_effort"
)

type Spec struct {
	Version         string          `json:"version" yaml:"version"`
	Name            string          `json:"name" yaml:"name"`
	Description     string          `json:"description,omitempty" yaml:"description,omitempty"`
	FailureStrategy FailureStrategy `json:"failure_strategy,omitempty" yaml:"failure_strategy,omitempty"`
	Budget          Budget          `json:"budget,omitempty" yaml:"budget,omitempty"`
	Stages          []Stage         `json:"stages" yaml:"stages"`
}

type Stage struct {
	ID       string `json:"id" yaml:"id"`
	Parallel bool   `json:"parallel,omitempty" yaml:"parallel,omitempty"`
	Steps    []Step `json:"steps" yaml:"steps"`
}

type Step struct {
	ID       string         `json:"id" yaml:"id"`
	Type     StepType       `json:"type" yaml:"type"`
	Agent    string         `json:"agent,omitempty" yaml:"agent,omitempty"`
	Tool     string         `json:"tool,omitempty" yaml:"tool,omitempty"`
	Task     string         `json:"task,omitempty" yaml:"task,omitempty"`
	Input    map[string]any `json:"input,omitempty" yaml:"input,omitempty"`
	Cache    *bool          `json:"cache,omitempty" yaml:"cache,omitempty"`
	Budget   Budget         `json:"budget,omitempty" yaml:"budget,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type Budget struct {
	MaxSteps  int `json:"max_steps,omitempty" yaml:"max_steps,omitempty"`
	MaxTokens int `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
}

func ParseSpec(data []byte) (*Spec, error) {
	var spec Spec
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("workflow spec is empty")
	}
	var err error
	if strings.HasPrefix(trimmed, "{") {
		err = json.Unmarshal([]byte(trimmed), &spec)
	} else {
		err = yaml.Unmarshal([]byte(trimmed), &spec)
	}
	if err != nil {
		return nil, fmt.Errorf("parse workflow spec: %w", err)
	}
	if err := spec.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *Spec) NormalizeAndValidate() error {
	if strings.TrimSpace(s.Version) == "" {
		s.Version = "v1"
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if s.FailureStrategy == "" {
		s.FailureStrategy = FailureStop
	}
	switch s.FailureStrategy {
	case FailureStop, FailureBest:
	default:
		return fmt.Errorf("unsupported failure_strategy %q", s.FailureStrategy)
	}
	if len(s.Stages) == 0 {
		return fmt.Errorf("workflow must define at least one stage")
	}

	seenStages := make(map[string]struct{}, len(s.Stages))
	seenSteps := make(map[string]struct{})
	for i := range s.Stages {
		stage := &s.Stages[i]
		stage.ID = strings.TrimSpace(stage.ID)
		if stage.ID == "" {
			stage.ID = fmt.Sprintf("stage_%d", i+1)
		}
		if _, ok := seenStages[stage.ID]; ok {
			return fmt.Errorf("duplicate stage id %q", stage.ID)
		}
		seenStages[stage.ID] = struct{}{}
		if len(stage.Steps) == 0 {
			return fmt.Errorf("stage %q must define at least one step", stage.ID)
		}
		for j := range stage.Steps {
			step := &stage.Steps[j]
			step.ID = strings.TrimSpace(step.ID)
			if step.ID == "" {
				step.ID = fmt.Sprintf("%s_step_%d", stage.ID, j+1)
			}
			if _, ok := seenSteps[step.ID]; ok {
				return fmt.Errorf("duplicate step id %q", step.ID)
			}
			seenSteps[step.ID] = struct{}{}
			if err := validateStep(*step); err != nil {
				return fmt.Errorf("stage %q step %q: %w", stage.ID, step.ID, err)
			}
		}
	}
	return nil
}

func validateStep(step Step) error {
	switch step.Type {
	case StepTypeAgent:
		if strings.TrimSpace(step.Agent) == "" {
			return fmt.Errorf("agent step requires agent")
		}
		if strings.TrimSpace(step.Task) == "" {
			return fmt.Errorf("agent step requires task")
		}
	case StepTypeTool:
		if strings.TrimSpace(step.Tool) == "" {
			return fmt.Errorf("tool step requires tool")
		}
		if step.Input == nil {
			step.Input = map[string]any{}
		}
	default:
		return fmt.Errorf("unsupported step type %q", step.Type)
	}
	return nil
}

func (s Spec) Digest() string {
	data, _ := json.Marshal(canonicalSpec(s))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalSpec(s Spec) Spec {
	out := s
	out.Description = strings.TrimSpace(s.Description)
	out.Stages = make([]Stage, len(s.Stages))
	for i := range s.Stages {
		out.Stages[i] = s.Stages[i]
		out.Stages[i].Steps = make([]Step, len(s.Stages[i].Steps))
		copy(out.Stages[i].Steps, s.Stages[i].Steps)
		for j := range out.Stages[i].Steps {
			step := &out.Stages[i].Steps[j]
			step.Task = strings.TrimSpace(step.Task)
			step.Agent = strings.TrimSpace(step.Agent)
			step.Tool = strings.TrimSpace(step.Tool)
		}
	}
	return out
}

func stepCacheEnabled(step Step) bool {
	if step.Cache == nil {
		return true
	}
	return *step.Cache
}
