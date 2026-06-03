package evolution

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// LLMSkillEditor generates candidate replacements for skill lines using an LLM.
// This is the key innovation from the SkillOpt paper: the LLM acts as a
// text-space optimizer, proposing bounded edits to improve skill quality.
type LLMSkillEditor struct {
	completer Completer // LLM completion interface
}

// Completer is a minimal LLM interface for skill editing.
type Completer interface {
	Complete(ctx context.Context, system, prompt string) (string, error)
}

// NewLLMSkillEditor creates a new LLM-driven skill editor.
func NewLLMSkillEditor(completer Completer) *LLMSkillEditor {
	return &LLMSkillEditor{completer: completer}
}

// GenerateReplacements asks the LLM to propose improved versions of a skill line.
// Returns up to `n` candidate replacements.
func (e *LLMSkillEditor) GenerateReplacements(ctx context.Context, skillName string, lineIdx int, original string, contextLines []string, n int) ([]string, error) {
	if n <= 0 {
		n = 3
	}
	if e.completer == nil {
		return nil, fmt.Errorf("no LLM completer configured")
	}

	contextStr := strings.Join(contextLines, "\n")
	system := `You are a skill optimization assistant. Your task is to propose improved versions
of a single line in an agent skill document. The improvements should make the instruction:
- More specific and actionable
- Clearer in its expectations
- More robust across different scenarios
- Concise (don't add unnecessary words)

Return EXACTLY the replacement line text — no quotes, no markdown, no explanation.
Just the raw replacement text, one per line.`

	prompt := fmt.Sprintf(`Skill: %s
Context (surrounding lines):
%s

Line to improve (line %d):
%s

Propose %d improved versions of this line. Each version on its own line.
Do NOT include the original text. Make each version genuinely different.`,
		skillName, contextStr, lineIdx, original, n)

	response, err := e.completer.Complete(ctx, system, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Parse response: one replacement per line
	lines := strings.Split(strings.TrimSpace(response), "\n")
	replacements := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == original {
			continue
		}
		// Strip any numbering prefix like "1. " or "- "
		line = strings.TrimLeft(line, "0123456789. -)")
		line = strings.TrimSpace(line)
		if line != "" && line != original {
			replacements = append(replacements, line)
		}
	}
	return replacements, nil
}

// GenerateSectionRewrite asks the LLM to propose an improved version of an
// entire section of a skill document.
func (e *LLMSkillEditor) GenerateSectionRewrite(ctx context.Context, skillName string, section []string) (string, error) {
	if e.completer == nil {
		return "", fmt.Errorf("no LLM completer configured")
	}

	original := strings.Join(section, "\n")
	system := `You are a skill optimization assistant. Rewrite the given skill section to be
more effective. Your rewrite should:
- Keep the same purpose and scope
- Be more specific and actionable
- Remove ambiguity and hedging language
- Maintain the same approximate length
Return ONLY the rewritten section text — no commentary, no markdown wrapping.`

	prompt := fmt.Sprintf(`Skill: %s
Original section:
%s

Rewrite this section to be more effective:`, skillName, original)

	response, err := e.completer.Complete(ctx, system, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM completion failed: %w", err)
	}
	return strings.TrimSpace(response), nil
}

// LLMSkillOpt extends SkillOpt with LLM-driven edit generation.
type LLMSkillOpt struct {
	*SkillOpt
	editor *LLMSkillEditor
}

// NewLLMSkillOpt creates an LLM-enhanced SkillOpt optimizer.
func NewLLMSkillOpt(initialBudget float64, completer Completer) *LLMSkillOpt {
	return &LLMSkillOpt{
		SkillOpt: NewSkillOpt(initialBudget),
		editor:   NewLLMSkillEditor(completer),
	}
}

// generateLLMCandidates enriches the candidate pool with LLM-generated replacements.
func (o *LLMSkillOpt) generateLLMCandidates(ctx context.Context, doc *SkillDocument, indices []int) []EditOp {
	if o.editor == nil || o.editor.completer == nil {
		return nil
	}

	var candidates []EditOp
	for _, idx := range indices {
		if idx >= len(doc.Lines) {
			continue
		}
		line := doc.Lines[idx]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Get context: 2 lines before and after
		start := idx - 2
		if start < 0 {
			start = 0
		}
		end := idx + 3
		if end > len(doc.Lines) {
			end = len(doc.Lines)
		}
		contextLines := doc.Lines[start:end]

		replacements, err := o.editor.GenerateReplacements(ctx, doc.Name, idx, line, contextLines, 3)
		if err != nil {
			continue
		}
		for _, repl := range replacements {
			candidates = append(candidates, EditOp{
				Type:    EditReplace,
				LineIdx: idx,
				OldText: line,
				NewText: repl,
				Hash:    fmt.Sprintf("llm_rep_%d_%s", idx, hashStr(repl)),
			})
		}
	}
	return candidates
}

// OptimizeWithLLM runs the optimization loop with LLM-generated candidates.
func (o *LLMSkillOpt) OptimizeWithLLM(ctx context.Context, doc *SkillDocument, scorer ScoreFunc, maxEpochs int, llmBudgetPerEpoch int) (*SkillDocument, SkillOptStats, error) {
	if maxEpochs <= 0 {
		maxEpochs = 5
	}
	if llmBudgetPerEpoch <= 0 {
		llmBudgetPerEpoch = 5
	}

	baseline, err := scorer(ctx, doc)
	if err != nil {
		return nil, o.stats, fmt.Errorf("baseline scoring failed: %w", err)
	}
	o.stats.StartScore = baseline
	o.stats.BestScore = baseline

	for o.epoch = 0; o.epoch < maxEpochs; o.epoch++ {
		if o.budget < o.minBudget {
			break
		}

		// Phase 1: syntactic candidates (fast, no LLM)
		syntacticImproved := o.runEpoch(ctx, doc, scorer)

		// Phase 2: LLM-generated candidates (slower but smarter)
		// Select top-N lines by "importance" for LLM editing
		indices := o.selectImportantLines(doc, llmBudgetPerEpoch)
		llmCandidates := o.generateLLMCandidates(ctx, doc, indices)
		llmImproved := false
		for _, edit := range llmCandidates {
			if o.rejected[edit.Hash] {
				continue
			}
			o.stats.EditsTried++
			editedDoc := o.applyEdit(doc, edit)
			score, err := scorer(ctx, editedDoc)
			if err != nil {
				o.rejected[edit.Hash] = true
				o.stats.EditsRejected++
				continue
			}
			if score > o.stats.BestScore {
				doc.Lines = editedDoc.Lines
				doc.Content = editedDoc.Content
				o.stats.BestScore = score
				o.stats.EditsAccepted++
				llmImproved = true
				break // Apply only the best LLM edit per epoch
			}
			o.rejected[edit.Hash] = true
			o.stats.EditsRejected++
		}

		if !syntacticImproved && !llmImproved {
			break
		}
		o.budget *= o.decayRate
		o.stats.Epochs++
	}

	return doc, o.stats, nil
}

// selectImportantLines picks the N most important lines for LLM editing.
// Importance heuristic: line length (more content = more room for improvement).
func (o *LLMSkillOpt) selectImportantLines(doc *SkillDocument, n int) []int {
	type scoredLine struct {
		idx   int
		score int
	}
	var scored []scoredLine
	for i, line := range doc.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Score by content length and keyword density
		keywords := []string{"must", "should", "always", "never", "use", "check", "verify", "ensure", "when", "if", "then"}
		keywordCount := 0
		lower := strings.ToLower(trimmed)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				keywordCount++
			}
		}
		score := len(trimmed) + keywordCount*20
		scored = append(scored, scoredLine{idx: i, score: score})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	indices := make([]int, 0, n)
	for i := 0; i < n && i < len(scored); i++ {
		indices = append(indices, scored[i].idx)
	}
	return indices
}
