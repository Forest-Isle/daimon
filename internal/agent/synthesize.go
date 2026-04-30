package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/skill"
)

const (
	defaultSynthesizeRewardThreshold = 0.7
	defaultSynthesizeOutputDir       = "~/.IronClaw/skills/synthesized"
)

type SynthesizeConfig struct {
	RewardThreshold float64
	OutputDir       string
	Enabled         bool
}

type SynthesizeNode struct {
	cfg SynthesizeConfig
}

func NewSynthesizeNode(cfg SynthesizeConfig) *SynthesizeNode {
	if cfg.RewardThreshold == 0 {
		cfg.RewardThreshold = defaultSynthesizeRewardThreshold
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		cfg.OutputDir = defaultSynthesizeOutputDir
	}

	return &SynthesizeNode{cfg: cfg}
}

func (s *SynthesizeNode) NodeType() NodeType {
	return NodeSynthesize
}

func (s *SynthesizeNode) Execute(ctx context.Context, state GraphState) (NodeResult, error) {
	if s == nil || !s.cfg.Enabled {
		return NodeResult{ShouldTerminate: true}, nil
	}

	steps := extractSynthesisSteps(state.Events)
	if len(steps) < 2 {
		return NodeResult{ShouldTerminate: true}, nil
	}

	select {
	case <-ctx.Done():
		return NodeResult{ShouldTerminate: true}, ctx.Err()
	default:
	}

	outputDir := expandHome(s.cfg.OutputDir)
	if outputDir == "" {
		return NodeResult{ShouldTerminate: true}, fmt.Errorf("synthesize: empty output dir")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return NodeResult{ShouldTerminate: true}, fmt.Errorf("synthesize: create output dir: %w", err)
	}

	sessionPrefix := sanitizeSessionPrefix(state.SessionID)
	filename := fmt.Sprintf("%s_%s.md", sessionPrefix, time.Now().UTC().Format("20060102T150405Z"))
	fullPath := filepath.Join(outputDir, filename)

	draft := buildSynthesizedSkill(skill.Skill{
		Name:        fmt.Sprintf("synthesized-%s", sessionPrefix),
		Description: fmt.Sprintf("Synthesized from session %s with %d action steps.", state.SessionID, len(steps)),
	}, steps)

	if err := os.WriteFile(fullPath, []byte(draft), 0o644); err != nil {
		return NodeResult{ShouldTerminate: true}, fmt.Errorf("synthesize: write skill: %w", err)
	}

	return NodeResult{
		ShouldTerminate: true,
		Output:          "synthesized: " + fullPath,
	}, nil
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path[0] != '~' {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}

	if path == "~" {
		return home
	}

	if len(path) > 1 && (path[1] == '/' || path[1] == '\\') {
		return filepath.Join(home, path[2:])
	}

	return path
}

func extractSynthesisSteps(events []GraphEvent) []string {
	steps := make([]string, 0, len(events))
	for _, event := range events {
		if event.NodeType != NodeAct {
			continue
		}

		snapshot := strings.TrimSpace(truncateRunes(event.OutputSnapshot, 200))
		if snapshot == "" {
			continue
		}

		steps = append(steps, snapshot)
	}

	return steps
}

func buildSynthesizedSkill(meta skill.Skill, steps []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(yamlQuote(meta.Name))
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(yamlQuote(meta.Description))
	b.WriteString("\n")
	b.WriteString("steps:\n")
	for _, step := range steps {
		b.WriteString("  - ")
		b.WriteString(yamlQuote(step))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(meta.Name)
	b.WriteString("\n\n")
	b.WriteString(meta.Description)
	b.WriteString("\n\n")
	b.WriteString("## Steps\n")
	for i, step := range steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}

	return b.String()
}

func sanitizeSessionPrefix(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "session"
	}

	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= 8 {
			break
		}
	}

	if b.Len() == 0 {
		return "session"
	}

	return b.String()
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	return string(runes[:limit])
}

func yamlQuote(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ")
	return `"` + replacer.Replace(strings.TrimSpace(s)) + `"`
}
