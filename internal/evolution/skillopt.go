package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

// SkillOpt implements the bounded text-space optimizer for agent skill documents
// described in SkillOpt (Yang et al., May 2026). It treats skills as external,
// trainable state of a frozen agent — applying add/delete/replace edits with
// validation gating to iteratively improve task performance.
//
// Key properties:
//   - Bounded edits: only add, delete, or replace individual sentences/lines
//   - Validation gating: every edit is verified via a scoring function before acceptance
//   - Budget decay: the edit budget shrinks as optimization converges
//   - Rejected-edit buffer: prevents re-trying previously rejected edits
type SkillOpt struct {
	budget        float64       // remaining edit budget (learning rate)
	initialBudget float64       // starting budget
	decayRate     float64       // budget decay per epoch
	minBudget     float64       // floor for budget
	rejected      map[string]bool // set of rejected edit hashes
	epoch         int
	stats         SkillOptStats
}

// SkillOptStats tracks optimization progress.
type SkillOptStats struct {
	Epochs       int   `json:"epochs"`
	EditsTried   int   `json:"edits_tried"`
	EditsAccepted int  `json:"edits_accepted"`
	EditsRejected int  `json:"edits_rejected"`
	BestScore    float64 `json:"best_score"`
	StartScore   float64 `json:"start_score"`
}

// SkillDocument is a skill text that can be optimized.
type SkillDocument struct {
	Name    string   // skill name
	Content string   // full text content
	Lines   []string // parsed lines (populated on load)
}

// ParseSkillDocument parses a skill document into editable lines.
func ParseSkillDocument(name, content string) *SkillDocument {
	return &SkillDocument{
		Name:    name,
		Content: content,
		Lines:   strings.Split(content, "\n"),
	}
}

// Rebuild reconstructs the full text from lines.
func (d *SkillDocument) Rebuild() string {
	return strings.Join(d.Lines, "\n")
}

// EditOp represents a single text edit operation.
type EditOp struct {
	Type    EditType
	LineIdx int    // 0-based line index
	OldText string // the line being replaced/deleted
	NewText string // the replacement line (add/replace only)
	Hash    string // content hash for rejection tracking
}

type EditType string

const (
	EditAdd     EditType = "add"
	EditDelete  EditType = "delete"
	EditReplace EditType = "replace"
)

// ScoreFunc evaluates a skill document and returns a score (higher = better).
type ScoreFunc func(ctx context.Context, doc *SkillDocument) (float64, error)

// NewSkillOpt creates a new SkillOpt optimizer.
// initialBudget controls how aggressive the optimizer is (1.0 = full, 0.1 = conservative).
func NewSkillOpt(initialBudget float64) *SkillOpt {
	if initialBudget <= 0 {
		initialBudget = 0.3
	}
	return &SkillOpt{
		budget:        initialBudget,
		initialBudget: initialBudget,
		decayRate:     0.85, // budget × 0.85 each epoch
		minBudget:     0.05, // stop when budget < 0.05
		rejected:      make(map[string]bool),
	}
}

// Optimize runs the full optimization loop. It takes a skill document and a
// scoring function, and returns the optimized document with stats.
// maxEpochs limits the number of optimization rounds.
func (o *SkillOpt) Optimize(ctx context.Context, doc *SkillDocument, scorer ScoreFunc, maxEpochs int) (*SkillDocument, SkillOptStats, error) {
	if maxEpochs <= 0 {
		maxEpochs = 10
	}

	baseline, err := scorer(ctx, doc)
	if err != nil {
		return nil, o.stats, fmt.Errorf("baseline scoring failed: %w", err)
	}
	o.stats.StartScore = baseline
	o.stats.BestScore = baseline

	for o.epoch = 0; o.epoch < maxEpochs; o.epoch++ {
		if o.budget < o.minBudget {
			slog.Info("skillopt: budget exhausted, stopping",
				"skill", doc.Name, "epoch", o.epoch, "budget", o.budget)
			break
		}

		improved := o.runEpoch(ctx, doc, scorer)
		if !improved {
			// No edit improved the score this epoch → converge
			slog.Info("skillopt: converged (no improvement)",
				"skill", doc.Name, "epoch", o.epoch, "best", o.stats.BestScore)
			break
		}

		// Decay budget
		o.budget *= o.decayRate
		o.stats.Epochs++
	}

	return doc, o.stats, nil
}

// runEpoch generates candidate edits and applies the best one (if any improves score).
func (o *SkillOpt) runEpoch(ctx context.Context, doc *SkillDocument, scorer ScoreFunc) bool {
	candidates := o.generateCandidates(doc)
	if len(candidates) == 0 {
		return false
	}

	type scoredEdit struct {
		edit  EditOp
		score float64
		doc   *SkillDocument
	}

	var best *scoredEdit

	for _, edit := range candidates {
		// Check rejection buffer
		if o.rejected[edit.Hash] {
			continue
		}
		o.stats.EditsTried++

		// Apply edit to a copy
		editedDoc := o.applyEdit(doc, edit)
		score, err := scorer(ctx, editedDoc)
		if err != nil {
			slog.Debug("skillopt: edit scoring failed", "hash", edit.Hash, "err", err)
			o.rejected[edit.Hash] = true
			o.stats.EditsRejected++
			continue
		}

		if score > o.stats.BestScore {
			if best == nil || score > best.score {
				best = &scoredEdit{edit: edit, score: score, doc: editedDoc}
			}
		} else {
			o.rejected[edit.Hash] = true
			o.stats.EditsRejected++
		}
	}

	if best == nil {
		return false
	}

	// Apply the best edit
	doc.Lines = best.doc.Lines
	doc.Content = best.doc.Content
	o.stats.BestScore = best.score
	o.stats.EditsAccepted++
	slog.Info("skillopt: edit accepted",
		"skill", doc.Name, "type", best.edit.Type,
		"line", best.edit.LineIdx, "score", best.score,
		"delta", best.score-o.stats.StartScore)
	return true
}

// generateCandidates produces candidate edits bounded by the current budget.
func (o *SkillOpt) generateCandidates(doc *SkillDocument) []EditOp {
	numEdits := int(math.Ceil(o.budget * float64(len(doc.Lines))))
	if numEdits < 1 {
		numEdits = 1
	}
	if numEdits > len(doc.Lines) {
		numEdits = len(doc.Lines)
	}

	var candidates []EditOp

	for i := 0; i < numEdits; i++ {
		lineIdx := i // spread edits across the document
		if lineIdx >= len(doc.Lines) {
			break
		}
		line := strings.TrimSpace(doc.Lines[lineIdx])

		// Skip empty lines and section headers for structural edits
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "#"):
			// Section headers: only consider replace (not delete/add)
			if o.budget > 0.5 {
				candidates = append(candidates, EditOp{
					Type:    EditReplace,
					LineIdx: lineIdx,
					OldText: line,
					NewText: line + " (optimized)",
					Hash:    fmt.Sprintf("replace_%d_%s", lineIdx, hashStr(line)),
				})
			}

		case len(line) < 20:
			// Very short lines: consider expanding
			candidates = append(candidates, EditOp{
				Type:    EditReplace,
				LineIdx: lineIdx,
				OldText: line,
				NewText: line,
				Hash:    fmt.Sprintf("expand_%d_%s", lineIdx, hashStr(line)),
			})

		default:
			// Regular content lines: try all three operations
			// Delete
			candidates = append(candidates, EditOp{
				Type:    EditDelete,
				LineIdx: lineIdx,
				OldText: line,
				Hash:    fmt.Sprintf("del_%d_%s", lineIdx, hashStr(line)),
			})
			// Replace (with slightly modified version)
			replacement := o.generateReplacement(line)
			candidates = append(candidates, EditOp{
				Type:    EditReplace,
				LineIdx: lineIdx,
				OldText: line,
				NewText: replacement,
				Hash:    fmt.Sprintf("rep_%d_%s", lineIdx, hashStr(line)),
			})
		}
	}

	// Add insert candidates between existing lines
	addBudget := int(o.budget * 3)
	if addBudget > 5 {
		addBudget = 5
	}
	for i := 0; i < addBudget && i < len(doc.Lines)-1; i++ {
		candidates = append(candidates, EditOp{
			Type:    EditAdd,
			LineIdx: i + 1,
			NewText: "",
			Hash:    fmt.Sprintf("add_%d", i),
		})
	}

	return candidates
}

// generateReplacement produces a candidate replacement for a line.
// In a full implementation, this would use an LLM to generate alternatives.
// For now, it applies structural transformations that commonly improve skills.
func (o *SkillOpt) generateReplacement(original string) string {
	// Apply common skill-improvement transformations
	transformations := []struct {
		from string
		to   string
	}{
		{"you should", "you must"},
		{"try to", ""},
		{"consider", "always"},
		{"if possible", ""},
		{"might want to", "should"},
		{"could", "must"},
	}
	result := original
	for _, t := range transformations {
		if strings.Contains(strings.ToLower(result), t.from) {
			result = strings.ReplaceAll(result, t.from, t.to)
			result = strings.TrimSpace(result)
			return result
		}
	}
	// No applicable transformation
	return original
}

func (o *SkillOpt) applyEdit(doc *SkillDocument, edit EditOp) *SkillDocument {
	lines := make([]string, len(doc.Lines))
	copy(lines, doc.Lines)

	switch edit.Type {
	case EditAdd:
		// Insert new line at position
		lines = append(lines[:edit.LineIdx], append([]string{edit.NewText}, lines[edit.LineIdx:]...)...)
	case EditDelete:
		// Remove line
		if edit.LineIdx < len(lines) {
			lines = append(lines[:edit.LineIdx], lines[edit.LineIdx+1:]...)
		}
	case EditReplace:
		// Replace line content
		if edit.LineIdx < len(lines) {
			lines[edit.LineIdx] = edit.NewText
		}
	}

	return &SkillDocument{
		Name:    doc.Name,
		Lines:   lines,
		Content: strings.Join(lines, "\n"),
	}
}

// Stats returns a copy of the current optimization statistics.
func (o *SkillOpt) Stats() SkillOptStats {
	return o.stats
}

// GetRejected returns the hashes of rejected edits (for debugging).
func (o *SkillOpt) GetRejected() []string {
	hashes := make([]string, 0, len(o.rejected))
	for h := range o.rejected {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	return hashes
}

func hashStr(s string) string {
	// Simple content-based hash — sufficient for rejection tracking
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%x", h)
}
