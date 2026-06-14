package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// proposalsHorizonHours bounds how far ahead the job looks: only commitments due
// within this window seed proposals, so the agent anticipates the near future
// rather than speculating about the distant one.
const proposalsHorizonHours = 72

// proposalsDailyCap bounds the LIVE pending proposal queue depth, so a noisy
// model or repeated sleep cycles cannot flood the user. A cycle adds at most
// (cap - current pending) proposals; excess is dropped and reconsidered next
// cycle once the user has acted on some.
const proposalsDailyCap = 5

const proposalsSystemPrompt = `You are the anticipation phase of a personal agent's sleep cycle. You are given the user's UPCOMING COMMITMENTS (things due soon). Propose only concrete next actions the user will plausibly need in order to meet these commitments — preparation, follow-ups, reminders grounded in the commitments listed. Invent nothing that is not grounded in a listed commitment; when nothing useful is anticipatable, return an empty array. Respond with ONLY a JSON array of objects {"title":"<short imperative>","body":"<one or two sentences of context>","action_plan":"<the goal to pursue if accepted>","urgency":<integer 0-3>} and nothing else. Return [] when there is nothing worth proposing.`

// CommitmentBrief is one upcoming commitment, flattened to just what the
// anticipation prompt needs. DueAt is epoch seconds.
type CommitmentBrief struct {
	ID    string
	Kind  string
	Title string
	Body  string
	DueAt int64
}

// ProposedItem is one anticipatory proposal the job emits. ExpiresAt is epoch
// seconds (the job stamps it to the horizon); Urgency is 0 low .. 3 urgent.
type ProposedItem struct {
	Title            string
	Body             string
	ActionPlan       string
	Urgency          int
	SourceCommitment string
	ExpiresAt        int64
}

// commitmentLister yields commitments due within a horizon. The query lives in
// the boundary adapter so this job stays pure logic.
type commitmentLister interface {
	DueCommitments(ctx context.Context, withinUnix int64) ([]CommitmentBrief, error)
}

// proposalWriter is the slice of the proposals store the job needs: the set of
// titles still live at now (for dedup) and a bulk append. The adapter owns the
// store and the clock for created_at.
type proposalWriter interface {
	PendingTitles(ctx context.Context, now int64) (map[string]bool, error)
	Add(ctx context.Context, items []ProposedItem) error
}

// ProposalsJob mines upcoming commitments into a queue of anticipatory proposals,
// so the agent surfaces "you'll likely need X next" before the user asks. It is
// conservative by construction: it proposes only when commitments are actually
// due soon, it grounds every proposal in those commitments (the model is told to
// invent nothing), it never re-queues a title already pending, and it caps the
// batch. An unparseable model reply degrades to "no proposals" rather than
// failing the sleep cycle.
type ProposalsJob struct {
	commitments commitmentLister
	proposals   proposalWriter
	summarizer  Summarizer
	now         func() int64
}

// NewProposalsJob builds the job over a commitment source, a proposals sink, an
// LLM summarizer (the anticipation judge), and a clock. The clock is injected at
// the boundary (the gateway) — the sleep job never reads the wall clock itself,
// keeping it pure and deterministically testable; a nil clock is a wiring error
// the job reports at Run time rather than silently falling back to time.Now.
func NewProposalsJob(c commitmentLister, w proposalWriter, s Summarizer, now func() int64) *ProposalsJob {
	return &ProposalsJob{commitments: c, proposals: w, summarizer: s, now: now}
}

func (j *ProposalsJob) Name() string { return "proposals" }

func (j *ProposalsJob) Run(ctx context.Context) (string, error) {
	if j.commitments == nil || j.proposals == nil || j.summarizer == nil || j.now == nil {
		return "", fmt.Errorf("proposals: commitment source, proposal writer, summarizer, and clock are required")
	}

	now := j.now()
	horizon := now + int64(proposalsHorizonHours)*3600
	due, err := j.commitments.DueCommitments(ctx, horizon)
	if err != nil {
		return "", fmt.Errorf("proposals: read due commitments: %w", err)
	}
	if len(due) == 0 {
		return "no upcoming commitments", nil // nothing to anticipate; skip the model
	}

	raw, err := j.summarizer.Complete(ctx, proposalsSystemPrompt, buildProposalsInput(due))
	if err != nil {
		return "", fmt.Errorf("proposals: judge: %w", err)
	}
	items := parseProposals(raw)
	if len(items) == 0 {
		return "no proposals", nil
	}

	pending, err := j.proposals.PendingTitles(ctx, now)
	if err != nil {
		return "", fmt.Errorf("proposals: read pending titles: %w", err)
	}

	// The cap bounds the LIVE pending queue depth, not one cycle's batch: if the
	// queue already holds proposalsDailyCap live proposals, this cycle adds none.
	// This honors the blueprint's anti-spam "硬上限" robustly across multiple sleep
	// cycles in a day (a per-batch cap would let each cycle add a fresh N).
	budget := proposalsDailyCap - len(pending)
	if budget <= 0 {
		return "no new proposals (queue full)", nil
	}

	// Stamp the expiry to the horizon and best-effort attribute each proposal to a
	// source commitment (the first one — the model is not asked to map per-item,
	// and the attribution is advisory). Drop blank titles, ones already pending, and
	// duplicates within this same batch (the model may repeat a title).
	source := ""
	if len(due) > 0 {
		source = due[0].ID
	}
	seen := make(map[string]bool, len(items))
	kept := make([]ProposedItem, 0, budget)
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" || pending[title] || seen[title] {
			continue
		}
		seen[title] = true
		it.Title = title
		it.ExpiresAt = horizon
		if it.SourceCommitment == "" {
			it.SourceCommitment = source
		}
		kept = append(kept, it)
		if len(kept) >= budget {
			break
		}
	}
	if len(kept) == 0 {
		return "no new proposals", nil
	}
	if err := j.proposals.Add(ctx, kept); err != nil {
		return "", fmt.Errorf("proposals: queue: %w", err)
	}
	return fmt.Sprintf("queued %d proposal(s)", len(kept)), nil
}

// buildProposalsInput renders the due commitments for the anticipation prompt,
// one per line with the fields the model needs to ground a proposal.
func buildProposalsInput(due []CommitmentBrief) string {
	var b strings.Builder
	b.WriteString("## Upcoming commitments\n")
	for _, c := range due {
		kind := c.Kind
		if kind == "" {
			kind = "commitment"
		}
		fmt.Fprintf(&b, "- id=%s [%s] %s", c.ID, kind, oneLine(c.Title))
		if body := oneLine(c.Body); body != "" {
			fmt.Fprintf(&b, " — %s", body)
		}
		if c.DueAt > 0 {
			fmt.Fprintf(&b, " (due %d)", c.DueAt)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type proposalReply struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	ActionPlan string `json:"action_plan"`
	Urgency    int    `json:"urgency"`
}

// parseProposals extracts the JSON array of proposals from the model's reply,
// tolerating code fences and surrounding prose by scanning every balanced
// top-level [...] span and unmarshalling each until one yields proposals. A
// bracketed phrase in the preamble (which does not unmarshal) or an empty array
// before the real one therefore does not shadow a valid later array. An
// unparseable reply yields nil (degrade to "no proposals") rather than failing
// the sleep cycle.
func parseProposals(raw string) []ProposedItem {
	for _, span := range jsonArrayCandidates(raw) {
		var replies []proposalReply
		if err := json.Unmarshal([]byte(span), &replies); err != nil {
			continue
		}
		out := make([]ProposedItem, 0, len(replies))
		for _, r := range replies {
			out = append(out, ProposedItem{
				Title:      strings.TrimSpace(r.Title),
				Body:       strings.TrimSpace(r.Body),
				ActionPlan: strings.TrimSpace(r.ActionPlan),
				Urgency:    r.Urgency,
			})
		}
		if len(out) > 0 {
			return out // first non-empty array wins; skip empty/garbage earlier spans
		}
	}
	return nil
}

// jsonArrayCandidates returns the balanced top-level [...] spans in s, in order,
// tracking JSON string state (and escapes) so brackets inside string values do
// not throw off the depth count.
func jsonArrayCandidates(s string) []string {
	var out []string
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			if depth == 0 {
				start = i
			}
			depth++
		case ']':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, s[start:i+1])
					start = -1
				}
			}
		}
	}
	return out
}
