package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	distillScreenCap                 = 3
	distillScreenDismissCooldownDays = 14
	distillScreenBodyLimit           = 12000
)

const distillScreenSystemPrompt = `You are screening one distilled SKILL.md draft for possible promotion to an active skill. Judge whether the draft faithfully captures a reusable task pattern, is safe (no dangerous or destructive instructions), and has concrete executable steps. Respond with ONLY JSON {"promote":<true|false>,"reason":"<one sentence>"} and nothing else. Be conservative: when uncertain, use promote:false.`

// DraftCandidate is a valid staged distilled skill draft. Body is the SKILL.md
// body after frontmatter.
type DraftCandidate struct {
	Slug        string
	Name        string
	Description string
	Body        string
	Episodes    int
}

type draftSource interface {
	StagedDrafts(ctx context.Context) ([]DraftCandidate, error)
}

// PromoteProposal is a human-signed proposal to promote a staged skill draft.
// Slug becomes the proposal action_ref; accept performs deterministic promotion.
type PromoteProposal struct {
	Slug  string
	Title string
	Body  string
}

type screenProposalSink interface {
	PendingPromoteRefs(ctx context.Context, now int64) (map[string]bool, error)
	RecentlyDismissedPromoteRefs(ctx context.Context, since int64) (map[string]bool, error)
	AddPromote(ctx context.Context, items []PromoteProposal) error
}

// DistillScreenJob screens staged distilled skill drafts and queues typed
// promote_skill proposals for human signature. This is not a behavior replay
// Canary: skills are read lazily through read_skill rather than injected into the
// system prompt, so this slice only judges draft quality, safety, and structure.
// Behavior canaries are a later increment; promotion here remains deterministic
// and gated by proposal accept.
type DistillScreenJob struct {
	drafts     draftSource
	sink       screenProposalSink
	summarizer Summarizer
	now        func() int64
}

func NewDistillScreenJob(d draftSource, sink screenProposalSink, s Summarizer, now func() int64) *DistillScreenJob {
	return &DistillScreenJob{drafts: d, sink: sink, summarizer: s, now: now}
}

func (j *DistillScreenJob) Name() string { return "distill-screen" }

func (j *DistillScreenJob) Run(ctx context.Context) (string, error) {
	if j.drafts == nil || j.sink == nil || j.summarizer == nil || j.now == nil {
		return "", fmt.Errorf("distill-screen: draft source, proposal sink, summarizer, and clock are required")
	}

	drafts, err := j.drafts.StagedDrafts(ctx)
	if err != nil {
		return "", fmt.Errorf("distill-screen: list staged drafts: %w", err)
	}
	if len(drafts) == 0 {
		return "no staged drafts", nil
	}

	now := j.now()
	pending, err := j.sink.PendingPromoteRefs(ctx, now)
	if err != nil {
		return "", fmt.Errorf("distill-screen: read pending promote refs: %w", err)
	}
	cooldownStart := now - int64(distillScreenDismissCooldownDays)*86400
	dismissed, err := j.sink.RecentlyDismissedPromoteRefs(ctx, cooldownStart)
	if err != nil {
		return "", fmt.Errorf("distill-screen: read dismissed promote refs: %w", err)
	}

	var queued []PromoteProposal
	for _, d := range drafts {
		if len(queued) >= distillScreenCap {
			break
		}
		slug := strings.TrimSpace(d.Slug)
		title := "Promote distilled skill: " + strings.TrimSpace(d.Name)
		if slug == "" || strings.TrimSpace(d.Name) == "" || pending[slug] || dismissed[slug] {
			continue
		}

		raw, err := j.summarizer.Complete(ctx, distillScreenSystemPrompt, buildDistillScreenInput(d))
		if err != nil {
			return "", fmt.Errorf("distill-screen: judge %q: %w", slug, err)
		}
		decision, ok := parseDistillScreenDecision(raw)
		if !ok || !decision.Promote {
			continue
		}
		body := strings.TrimSpace(d.Description)
		if body != "" {
			body += " "
		}
		body += fmt.Sprintf("(covers %d episodes)", d.Episodes)
		if reason := oneLine(decision.Reason); reason != "" {
			body += " | screen: " + reason
		}
		queued = append(queued, PromoteProposal{
			Slug:  slug,
			Title: title,
			Body:  body,
		})
	}

	if len(queued) == 0 {
		return "no skill-promotion proposals", nil
	}
	if err := j.sink.AddPromote(ctx, queued); err != nil {
		return "", fmt.Errorf("distill-screen: queue: %w", err)
	}
	return fmt.Sprintf("queued %d skill-promotion proposal(s)", len(queued)), nil
}

func buildDistillScreenInput(d DraftCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %s\n", oneLine(d.Name))
	fmt.Fprintf(&b, "Description: %s\n", oneLine(d.Description))
	fmt.Fprintf(&b, "Episodes: %d\n", d.Episodes)
	b.WriteString("Body:\n")
	b.WriteString(truncateForPrompt(d.Body, distillScreenBodyLimit))
	return b.String()
}

type distillScreenDecision struct {
	Promote bool   `json:"promote"`
	Reason  string `json:"reason"`
}

func parseDistillScreenDecision(raw string) (distillScreenDecision, bool) {
	for _, candidate := range jsonObjectCandidates(raw) {
		if !strings.Contains(candidate, `"promote"`) {
			continue
		}
		var d distillScreenDecision
		if err := json.Unmarshal([]byte(candidate), &d); err == nil {
			d.Reason = strings.TrimSpace(d.Reason)
			return d, true
		}
	}
	return distillScreenDecision{}, false
}

func truncateForPrompt(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "\n[truncated]"
}
