package evolution

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillActivator manages the lifecycle of skill drafts: validation through
// safety gates and promotion to the active skills directory.
type SkillActivator struct {
	gates     []SafetyGate
	draftsDir string // directory containing draft .md files
	activeDir string // directory for promoted (active) skills
}

// NewSkillActivator creates an activator with the default safety gates.
func NewSkillActivator(draftsDir, activeDir string) *SkillActivator {
	return &SkillActivator{
		gates:     DefaultGates(),
		draftsDir: draftsDir,
		activeDir: activeDir,
	}
}

// SetGates overrides the default safety gates (useful for testing).
func (a *SkillActivator) SetGates(gates []SafetyGate) {
	a.gates = gates
}

// SetSandboxValidator configures the SandboxTestGate in the gate list with the
// given validator and enabled flag. If no SandboxTestGate is found, it is
// appended. This allows the caller (e.g. gateway) to inject sandbox validation
// without coupling the evolution package to the sandbox package.
func (a *SkillActivator) SetSandboxValidator(enabled bool, validator SandboxValidator) {
	for _, g := range a.gates {
		if sg, ok := g.(*SandboxTestGate); ok {
			sg.Enabled = enabled
			sg.Validator = validator
			return
		}
	}
	// No SandboxTestGate found; append one.
	a.gates = append(a.gates, &SandboxTestGate{
		Enabled:   enabled,
		Validator: validator,
	})
}

// PromoteDraft validates a draft through all safety gates. If all pass, returns
// promoted=true. The caller is responsible for moving the file.
func (a *SkillActivator) PromoteDraft(draft SkillDraft) (promoted bool, failedGate string, reason string) {
	return RunGates(draft, a.gates)
}

// ScanAndPromote scans draftsDir for eligible .md drafts, validates each through
// safety gates, and moves passing drafts to activeDir.
func (a *SkillActivator) ScanAndPromote() (promoted, rejected int) {
	entries, err := os.ReadDir(a.draftsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("skill_activator: cannot read drafts dir", "err", err)
		}
		return 0, 0
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		path := filepath.Join(a.draftsDir, e.Name())
		draft, err := parseDraftFromFile(path)
		if err != nil {
			slog.Warn("skill_activator: skip unparseable draft", "file", e.Name(), "err", err)
			rejected++
			continue
		}

		passed, failedGate, reason := RunGates(draft, a.gates)
		if !passed {
			slog.Info("skill_activator: draft rejected",
				"file", e.Name(),
				"gate", failedGate,
				"reason", reason,
			)
			rejected++
			continue
		}

		if err := a.moveDraft(path, e.Name()); err != nil {
			slog.Warn("skill_activator: failed to promote draft",
				"file", e.Name(), "err", err)
			rejected++
			continue
		}

		slog.Info("skill_activator: draft promoted", "file", e.Name())
		promoted++
	}

	return promoted, rejected
}

// moveDraft moves a draft file from draftsDir to activeDir, updating its status.
func (a *SkillActivator) moveDraft(srcPath, filename string) error {
	if err := os.MkdirAll(a.activeDir, 0o755); err != nil {
		return fmt.Errorf("create active dir: %w", err)
	}

	// Read the file, update status from "draft" to "active", then write to active dir
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	content := string(data)
	content = strings.Replace(content, "status: draft", "status: active", 1)

	dstPath := filepath.Join(a.activeDir, filename)
	if err := os.WriteFile(dstPath, []byte(content), 0o644); err != nil {
		return err
	}

	// Remove the draft file after successful write
	return os.Remove(srcPath)
}

// draftFrontmatter is used to parse YAML frontmatter from draft files.
type draftFrontmatter struct {
	Name              string   `yaml:"name"`
	Description       string   `yaml:"description"`
	Status            string   `yaml:"status"`
	OccurrenceCount   int      `yaml:"occurrence_count"`
	AvgReward         float64  `yaml:"avg_reward"`
	ToolSequence      []string `yaml:"tool_sequence"`
	UserRejected      bool     `yaml:"user_rejected"`
	ConflictingSkills []string `yaml:"conflicting_skills"`
}

// parseDraftFromFile reads a .md file, extracts its YAML frontmatter, and builds
// a SkillDraft for gate evaluation.
func parseDraftFromFile(path string) (SkillDraft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillDraft{}, err
	}

	fm, _ := splitSkillFrontmatter(data)
	if fm == nil {
		return SkillDraft{}, fmt.Errorf("no frontmatter in %s", path)
	}

	var meta draftFrontmatter
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return SkillDraft{}, fmt.Errorf("unmarshal frontmatter: %w", err)
	}

	return SkillDraft{
		Name:              meta.Name,
		Description:       meta.Description,
		ToolSequence:      meta.ToolSequence,
		OccurrenceCount:   meta.OccurrenceCount,
		AvgReward:         meta.AvgReward,
		UserRejected:      meta.UserRejected,
		ConflictingSkills: meta.ConflictingSkills,
	}, nil
}
