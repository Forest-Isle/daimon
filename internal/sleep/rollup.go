package sleep

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	// rollupKeepRecent is how many recent regular journal entries stay raw; only
	// entries older than these are folded, so the working window keeps full detail.
	rollupKeepRecent = 50
	// rollupMaxFold caps how many entries a single pass folds, bounding the prompt.
	rollupMaxFold = 500
	// rollupMinFold is the smallest batch worth folding: rolling up one entry just
	// trades it for a rollup entry, so wait until a few have accrued.
	rollupMinFold = 3
)

const rollupSystemPrompt = `You are the consolidation phase of a personal agent. Given a chronological batch of older journal entries (decisions, outcomes, corrections, facts), write a compact rollup (3-8 sentences) preserving the durable throughlines: what was done, what was decided, and anything still open. Drop incidental detail; keep what future-you would need to understand this period. Plain prose, no preamble. Do not invent anything not present in the input.`

// RollupJob folds older journal entries into a single rollup summary, keeping the
// recent window detailed while preventing unbounded journal growth. It is
// non-destructive: folded entries are tagged (rollup_id), never deleted, so the
// rollup is a lossy index over still-present detail. Facts and prior rollups are
// never folded.
type RollupJob struct {
	world      *world.Store
	summarizer Summarizer
	// keepRecent/maxFold default to the package constants when zero (overridable
	// in tests). A negative keepRecent is clamped to 0 by the store.
	keepRecent int
	maxFold    int
}

// NewRollupJob builds the rollup job over a world store and an LLM summarizer.
func NewRollupJob(w *world.Store, s Summarizer) *RollupJob {
	return &RollupJob{world: w, summarizer: s}
}

func (j *RollupJob) Name() string { return "rollup" }

func (j *RollupJob) Run(ctx context.Context) (string, error) {
	if j.world == nil || j.summarizer == nil {
		return "", fmt.Errorf("rollup: world store and summarizer are required")
	}
	keepRecent := j.keepRecent
	if keepRecent <= 0 {
		keepRecent = rollupKeepRecent
	}
	maxFold := j.maxFold
	if maxFold <= 0 {
		maxFold = rollupMaxFold
	}

	entries, err := j.world.UnrolledBeyond(ctx, keepRecent, maxFold)
	if err != nil {
		return "", fmt.Errorf("rollup: list foldable entries: %w", err)
	}

	// ids covers only entries that actually rendered content, so a rollup never
	// claims to summarize an entry it didn't see (and content-less rows are left
	// untagged rather than silently hidden).
	content, ids := buildRollupInput(entries)
	if len(ids) < rollupMinFold {
		return "nothing to roll up", nil
	}
	summary, err := j.summarizer.Complete(ctx, rollupSystemPrompt, content)
	if err != nil {
		return "", fmt.Errorf("rollup: summarize: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "summarizer returned empty rollup", nil
	}

	if _, err := j.world.Rollup(ctx, summary, ids); err != nil {
		return "", fmt.Errorf("rollup: persist: %w", err)
	}
	return fmt.Sprintf("rolled up %d journal entries", len(ids)), nil
}

// buildRollupInput renders the foldable entries chronologically and returns the
// ids of only those that rendered content (the set to tag). Entries arrive
// oldest-first from the store; a content-less entry is neither rendered nor
// folded, so the rollup never claims to cover what it never saw.
func buildRollupInput(entries []world.JournalEntry) (string, []string) {
	var b strings.Builder
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		summary := oneLine(e.Summary)
		detail := oneLine(e.Detail)
		kind := e.Kind
		if kind == "" {
			kind = "entry"
		}
		switch {
		case summary != "" && detail != "":
			fmt.Fprintf(&b, "- [%s] %s — %s\n", kind, summary, detail)
		case summary != "":
			fmt.Fprintf(&b, "- [%s] %s\n", kind, summary)
		case detail != "":
			fmt.Fprintf(&b, "- [%s] %s\n", kind, detail)
		default:
			continue // content-less: do not fold
		}
		ids = append(ids, e.ID)
	}
	return b.String(), ids
}
