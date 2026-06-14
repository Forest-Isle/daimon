package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/world"
)

// digestFactID is the stable id of the consolidated self-digest fact. Using a
// fixed id means each cycle REPLACES the previous digest (fact.upsert is
// delete-then-insert), so the journal accrues one always-current synthesis
// rather than a growing pile of stale digests.
const digestFactID = "fact_sleep_digest"

const digestJournalLimit = 30

const digestSystemPrompt = `You are the consolidation phase of a personal agent. Given the agent's active commitments and recent journal (decisions, outcomes, corrections, facts), write a concise self-digest (5-10 sentences) capturing: what the agent is currently responsible for, the through-lines in recent activity, and anything that looks unresolved or worth surfacing. Write in plain prose, second person ("You are ..."). Do not invent facts not present in the input.`

// DigestJob regenerates a single consolidated self-digest from recent world
// state and persists it as a kind=fact journal entry (via fact.upsert, so it
// replaces the prior digest in place). It is non-destructive: it adds/overwrites
// exactly one fact and never deletes other world state. The digest is then
// retrievable like any other world fact, keeping the agent's self-summary fresh.
type DigestJob struct {
	world      *world.Store
	summarizer Summarizer
}

// NewDigestJob builds the digest job over a world store and an LLM summarizer.
func NewDigestJob(w *world.Store, s Summarizer) *DigestJob {
	return &DigestJob{world: w, summarizer: s}
}

func (j *DigestJob) Name() string { return "digest" }

func (j *DigestJob) Run(ctx context.Context) (string, error) {
	if j.world == nil || j.summarizer == nil {
		return "", fmt.Errorf("digest: world store and summarizer are required")
	}

	commitments, err := j.world.CommitmentsDigest(ctx, "")
	if err != nil {
		return "", fmt.Errorf("digest: read commitments: %w", err)
	}
	entries, err := j.world.ListJournal(ctx, "", digestJournalLimit)
	if err != nil {
		return "", fmt.Errorf("digest: read journal: %w", err)
	}

	content, empty := buildDigestInput(commitments, entries)
	if empty {
		return "nothing to digest (empty world)", nil
	}

	digest, err := j.summarizer.Complete(ctx, digestSystemPrompt, content)
	if err != nil {
		return "", fmt.Errorf("digest: summarize: %w", err)
	}
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return "summarizer returned empty digest", nil
	}

	body, err := json.Marshal(world.JournalEntry{
		ID:      digestFactID,
		Summary: "Consolidated self-digest",
		Detail:  digest,
	})
	if err != nil {
		return "", fmt.Errorf("digest: marshal fact: %w", err)
	}
	if err := j.world.Apply(ctx, "sleep", []world.Mutation{{Op: "fact.upsert", Body: body}}); err != nil {
		return "", fmt.Errorf("digest: persist fact: %w", err)
	}
	return fmt.Sprintf("regenerated self-digest (%d chars)", len(digest)), nil
}

// buildDigestInput renders the world state the digest is computed from and
// reports empty=true when there is nothing worth summarizing (no commitments and
// no usable journal entries) so the job can skip a pointless LLM call. The
// "(none)" placeholders keep the prompt well-formed when only one section is empty.
func buildDigestInput(commitments string, entries []world.JournalEntry) (string, bool) {
	hasCommitments := false
	if c := strings.TrimSpace(commitments); c != "" && !strings.EqualFold(c, "None.") {
		hasCommitments = true
	}

	var b strings.Builder
	b.WriteString("## Active commitments\n")
	if hasCommitments {
		b.WriteString(strings.TrimSpace(commitments) + "\n")
	} else {
		b.WriteString("(none)\n")
	}
	b.WriteString("\n## Recent journal\n")
	hasJournal := false
	for _, e := range entries {
		if e.ID == digestFactID {
			continue // never feed the prior digest back into itself
		}
		summary := strings.TrimSpace(e.Summary)
		if summary == "" {
			continue
		}
		kind := e.Kind
		if kind == "" {
			kind = "entry"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", kind, summary)
		hasJournal = true
	}
	if !hasJournal {
		b.WriteString("(none)\n")
	}
	return b.String(), !hasCommitments && !hasJournal
}
