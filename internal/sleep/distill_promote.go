package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Forest-Isle/daimon/internal/world"
)

const promoteMaxPerCycle = 10

const promoteSystemPrompt = `You are the skill-distillation promotion phase of a personal agent's sleep cycle. You are given a repeatedly successful task pattern: a name and a one-line description. Output ONLY the markdown body for a SKILL.md draft. Do not include YAML frontmatter, code fences, or explanations before or after the body. Use concise imperative steps that describe how to complete this kind of task repeatably. Be conservative and specific. If there is not enough information to write useful steps, output empty.`

// DraftSink writes lazy SKILL.md drafts into a staging area that is not loaded as
// an active skill directory.
type DraftSink interface {
	WriteDraft(ctx context.Context, slug string, content []byte) (created bool, err error)
}

type PromoteJob struct {
	world      *world.Store
	summarizer Summarizer
	sink       DraftSink
}

func NewPromoteJob(w *world.Store, s Summarizer, sink DraftSink) *PromoteJob {
	return &PromoteJob{w, s, sink}
}

func (j *PromoteJob) Name() string { return "distill-promote" }

func (j *PromoteJob) Run(ctx context.Context) (string, error) {
	if j.world == nil || j.summarizer == nil || j.sink == nil {
		return "", fmt.Errorf("distill-promote: world, summarizer and sink are required")
	}

	// Window-independent fetch: candidates with no draft marker yet, oldest first,
	// capped at promoteMaxPerCycle. The store excludes already-processed candidates
	// in SQL, so a candidate is never starved by journal growth (an earlier recent-N
	// scan could push older un-promoted candidates out of view), and the cap bounds
	// real work (one LLM call each) rather than scans.
	candidates, err := j.world.ListDistillCandidatesWithoutDraft(ctx, promoteMaxPerCycle)
	if err != nil {
		return "", fmt.Errorf("distill-promote: list candidates: %w", err)
	}
	if len(candidates) == promoteMaxPerCycle {
		// A full page may mean more remain; the next cycle drains them (never silent).
		slog.Warn("distill-promote: candidate cap reached; deferring rest to next cycle", "cap", promoteMaxPerCycle)
	}

	staged := 0
	for _, e := range candidates {
		// e.ID is the candidate id distill wrote (distillCandidateID of its name); use
		// it directly so the draft marker the store correlated on ("distill_draft_"+id)
		// always matches, with no risk of a recompute drifting from the stored id.
		candidateID := e.ID
		draftID := "distill_draft_" + candidateID

		name := oneLine(strings.TrimPrefix(e.Summary, distillCandidate))
		if name == "" {
			// Malformed candidate with no usable name (distill never writes one, but a
			// corrupt or hand-edited row could exist). Mark it processed so it cannot
			// occupy a LIMIT slot every cycle and starve valid candidates; write no draft.
			if err := j.recordMarker(ctx, draftID, "distilled draft skipped (empty name)",
				"candidate="+candidateID+" | empty candidate name, no draft written"); err != nil {
				return "", err
			}
			slog.Warn("distill-promote: empty candidate name; recorded skip", "id", candidateID)
			continue
		}

		skillDesc, episodeIDs := parsePromoteDetail(e.Detail)
		body, err := j.summarizer.Complete(ctx, promoteSystemPrompt, promoteUserInput(name, skillDesc, episodeIDs))
		if err != nil {
			return "", fmt.Errorf("distill-promote: draft skill %q: %w", name, err)
		}
		body = strings.TrimSpace(body)
		if body == "" {
			// The model judged the pattern not skill-worthy (the prompt tells it to
			// output empty when there is not enough to write useful steps). Record a
			// skip marker so the candidate is processed exactly once and never
			// re-billed to the LLM; no draft file is written.
			if err := j.recordMarker(ctx, draftID, "distilled draft skipped (empty body): "+name,
				"candidate="+candidateID+" | empty body, no draft written"); err != nil {
				return "", err
			}
			slog.Warn("distill-promote: empty skill body; recorded skip", "candidate", name)
			continue
		}

		content, err := promoteDraftContent(name, skillDesc, candidateID, episodeIDs, body)
		if err != nil {
			return "", err
		}
		// Path is keyed by the (unique) candidate id, not just the human slug, so two
		// distinct names that slugify identically never collide and silently lose a
		// draft. A WriteDraft that finds the file already there (created=false) is then
		// only the same candidate's crash-recovery window, so writing the marker is
		// correct rather than mislabeling a colliding draft as done.
		slug := promoteStagingSlug(name, candidateID)
		if _, err := j.sink.WriteDraft(ctx, slug, content); err != nil {
			return "", fmt.Errorf("distill-promote: write draft %q: %w", slug, err)
		}
		if err := j.recordMarker(ctx, draftID, "distilled draft: "+name,
			"staged SKILL.md draft | candidate="+candidateID+" | slug="+slug); err != nil {
			return "", err
		}
		staged++
	}

	if staged == 0 {
		return "no distill candidates to promote", nil
	}
	return fmt.Sprintf("staged %d distilled skill draft(s)", staged), nil
}

// recordMarker appends an idempotency/accounting marker for a candidate (the dedup
// anchor ListDistillCandidatesWithoutDraft excludes on the next cycle). One marker
// per candidate, whether it was drafted or skipped, so each is processed once.
func (j *PromoteJob) recordMarker(ctx context.Context, id, summary, detail string) error {
	note, err := json.Marshal(world.JournalEntry{ID: id, Kind: "decision", Summary: summary, Detail: detail})
	if err != nil {
		return fmt.Errorf("distill-promote: marshal marker: %w", err)
	}
	if err := j.world.Apply(ctx, "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
		return fmt.Errorf("distill-promote: record marker %q: %w", id, err)
	}
	return nil
}

// promoteStagingSlug builds a filesystem-safe, collision-free staging directory
// name: a human-readable slug of the name plus the candidate id's hash tail, so
// two distinct names that slugify identically (e.g. "foo!" and "foo?") never share
// a path and overwrite one another's draft. Falls back to the candidate id when the
// name has no slug-able characters.
func promoteStagingSlug(name, candidateID string) string {
	base := promoteSlug(name)
	suffix := candidateID
	if i := strings.LastIndex(candidateID, "_"); i >= 0 && i+1 < len(candidateID) {
		suffix = candidateID[i+1:] // the deterministic hash tail of distillCandidateID
	}
	if base == "" {
		return suffix
	}
	return base + "-" + suffix
}

type draftFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	Metadata    struct {
		Distilled       bool     `yaml:"distilled"`
		SourceCandidate string   `yaml:"source_candidate"`
		SourceEpisodes  []string `yaml:"source_episodes"`
	} `yaml:"metadata"`
}

func promoteDraftContent(name, skillDesc, candidateID string, episodeIDs []string, body string) ([]byte, error) {
	desc := skillDesc
	if desc == "" {
		desc = "Distilled skill draft for: " + name
	}
	fm := draftFrontmatter{
		Name:        name,
		Description: desc,
		Version:     "0.0.1",
	}
	fm.Metadata.Distilled = true
	fm.Metadata.SourceCandidate = candidateID
	fm.Metadata.SourceEpisodes = episodeIDs
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("distill-promote: marshal frontmatter: %w", err)
	}
	content := make([]byte, 0, len(yamlBytes)+len(body)+10)
	content = append(content, []byte("---\n")...)
	content = append(content, yamlBytes...)
	content = append(content, []byte("---\n\n")...)
	content = append(content, []byte(body+"\n")...)
	return content, nil
}

func parsePromoteDetail(detail string) (skill string, ids []string) {
	right := detail
	if left, rest, ok := strings.Cut(detail, " | "); ok {
		skill = strings.TrimSpace(left)
		right = rest
	}
	_, after, ok := strings.Cut(right, "episodes:")
	if !ok {
		return skill, nil
	}
	for _, part := range strings.Split(after, ",") {
		id := strings.TrimSpace(part)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return skill, ids
}

func promoteSlug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}

func promoteUserInput(name, skill string, ids []string) string {
	var b strings.Builder
	b.WriteString("Task pattern: ")
	b.WriteString(name)
	if skill != "" {
		b.WriteString("\nIntended skill: ")
		b.WriteString(skill)
	}
	fmt.Fprintf(&b, "\nObserved in %d successful episodes.", len(ids))
	return b.String()
}
