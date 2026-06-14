package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	reconcileFactLimit  = 200
	reconcileMaxReason  = 500
	reconcileMinToJudge = 2
)

const reconcileSystemPrompt = `You are the memory-reconciliation phase of a personal agent's sleep cycle. You are given the agent's durable FACTS (each with a stable id). Find groups of facts that DIRECTLY CONTRADICT each other or that are NEAR-DUPLICATES stating the same thing. For each such group choose ONE fact to KEEP as canonical (prefer the most recent and most specific/correct) and list the OTHER ids as superseded. Be conservative: only group facts that clearly conflict or duplicate — never facts that merely relate to the same topic or could both be true. A superseded fact is permanently removed from retrieval, so a false positive destroys real knowledge; when in doubt, leave facts alone. Respond with ONLY a JSON object of the form {"reconcile":[{"canonical_id":"<id to keep>","superseded_ids":["<id>","<id>"],"reason":"<one sentence>"}]} and nothing else. Return {"reconcile":[]} when nothing should be reconciled.`

// ReconcileJob detects contradicting and duplicate durable facts and supersedes
// the stale ones: the canonical fact stays, each superseded fact is deleted from
// the world (and dropped from the retrieval index) and a correction journal entry
// records the supersession for the audit trail. It only ever removes kind=fact
// rows — outcomes/decisions/corrections are append-only audit and untouched.
//
// This absorbs the contradiction half of the legacy memory/lifecycle reconciliation
// (blueprint §4.8 reconcile) onto the world model, advancing invariant #1 (the
// world is the single source of truth and stays internally consistent). It is
// fail-safe: an unparseable verdict or a hallucinated id is ignored rather than
// destroying facts, and the job degrades to a no-op instead of aborting the cycle.
type ReconcileJob struct {
	world      *world.Store
	summarizer Summarizer
}

// NewReconcileJob builds the reconcile job over a world store (the fact source and
// supersession target) and an LLM summarizer (the contradiction/duplicate judge).
func NewReconcileJob(w *world.Store, s Summarizer) *ReconcileJob {
	return &ReconcileJob{world: w, summarizer: s}
}

func (j *ReconcileJob) Name() string { return "reconcile" }

func (j *ReconcileJob) Run(ctx context.Context) (string, error) {
	if j.world == nil || j.summarizer == nil {
		return "", fmt.Errorf("reconcile: world store and summarizer are required")
	}

	facts, err := j.world.ListFacts(ctx, reconcileFactLimit)
	if err != nil {
		return "", fmt.Errorf("reconcile: list facts: %w", err)
	}
	if len(facts) < reconcileMinToJudge {
		return "nothing to reconcile", nil
	}

	content := buildReconcileInput(facts)
	raw, err := j.summarizer.Complete(ctx, reconcileSystemPrompt, content)
	if err != nil {
		return "", fmt.Errorf("reconcile: judge: %w", err)
	}
	groups := parseReconcileVerdict(raw)
	if len(groups) == 0 {
		return "no contradictions found", nil
	}

	byID := factsByID(facts)
	// Never delete a fact that any group chose as canonical: if the judge lists the
	// same id as canonical in one group and superseded in another, keeping it wins
	// (conservative — we do not destroy a fact we were told to keep).
	keep := make(map[string]bool)
	for _, g := range groups {
		if cid := strings.TrimSpace(g.CanonicalID); cid != "" {
			keep[cid] = true
		}
	}

	done := make(map[string]bool)
	superseded := 0
	for _, g := range groups {
		cid := strings.TrimSpace(g.CanonicalID)
		if _, ok := byID[cid]; cid == "" || !ok {
			continue // canonical must be a real fact, else the group is meaningless
		}
		reason := truncateReason(g.Reason)
		for _, sid := range g.SupersededIDs {
			sid = strings.TrimSpace(sid)
			stale, known := byID[sid]
			switch {
			case sid == "" || !known: // hallucinated or unknown id
				continue
			case sid == cid: // cannot supersede itself
				continue
			case keep[sid]: // some group keeps this fact; do not delete it
				continue
			case done[sid]: // already superseded in an earlier group
				continue
			}

			note, mErr := json.Marshal(world.JournalEntry{
				Kind:    "correction",
				Summary: fmt.Sprintf("Fact %s superseded by %s", sid, cid),
				// Preserve the superseded fact's full content in the append-only
				// correction trace. This is the "SoftInvalidate" (blueprint §4.8):
				// the fact leaves the active retrieval set so the contradiction is
				// resolved, but nothing is truly lost — the journal is append-only and
				// the content stays recoverable, honoring invariant #4 (reversibility).
				Detail: supersededDetail(stale, reason),
			})
			if mErr != nil {
				return "", fmt.Errorf("reconcile: marshal correction: %w", mErr)
			}
			// Delete the stale fact and record the correction in one transaction, so a
			// fact is never removed without its audit trace landing (parallels the
			// episode交账 invariant).
			muts := []world.Mutation{
				{Op: "fact.delete", Target: sid},
				{Op: "journal.append", Body: note},
			}
			if err := j.world.Apply(ctx, "sleep", muts); err != nil {
				return "", fmt.Errorf("reconcile: supersede %s: %w", sid, err)
			}
			done[sid] = true
			superseded++
		}
	}

	if superseded == 0 {
		return "no contradictions found", nil
	}
	return fmt.Sprintf("superseded %d stale fact(s)", superseded), nil
}

func factsByID(facts []world.JournalEntry) map[string]world.JournalEntry {
	m := make(map[string]world.JournalEntry, len(facts))
	for _, f := range facts {
		if f.ID != "" {
			m[f.ID] = f
		}
	}
	return m
}

// supersededDetail renders the superseded fact's content plus the judge's reason
// into the correction note, so the append-only journal retains what was removed.
func supersededDetail(stale world.JournalEntry, reason string) string {
	content := oneLine(stale.Summary)
	if d := oneLine(stale.Detail); d != "" {
		if content != "" {
			content += " — " + d
		} else {
			content = d
		}
	}
	switch {
	case content != "" && reason != "":
		return "superseded content: " + content + " | reason: " + reason
	case content != "":
		return "superseded content: " + content
	default:
		return reason
	}
}

// buildReconcileInput renders the facts (id + summary + detail) for the judge.
func buildReconcileInput(facts []world.JournalEntry) string {
	var b strings.Builder
	b.WriteString("## Facts\n")
	for _, f := range facts {
		summary := oneLine(f.Summary)
		detail := oneLine(f.Detail)
		switch {
		case summary != "" && detail != "":
			fmt.Fprintf(&b, "- id=%s %s — %s\n", f.ID, summary, detail)
		case summary != "":
			fmt.Fprintf(&b, "- id=%s %s\n", f.ID, summary)
		case detail != "":
			fmt.Fprintf(&b, "- id=%s %s\n", f.ID, detail)
		default:
			fmt.Fprintf(&b, "- id=%s (empty)\n", f.ID)
		}
	}
	return b.String()
}

type reconcileGroup struct {
	CanonicalID   string   `json:"canonical_id"`
	SupersededIDs []string `json:"superseded_ids"`
	Reason        string   `json:"reason"`
}

// parseReconcileVerdict extracts the {"reconcile":[...]} object from the judge's
// reply, tolerating code fences and surrounding prose via the same string-aware
// balanced-object scan the drift job uses. Anything unparseable is treated as "no
// reconciliation" (conservative, fail-safe) rather than failing the sleep cycle.
func parseReconcileVerdict(raw string) []reconcileGroup {
	for _, candidate := range jsonObjectCandidates(raw) {
		if !strings.Contains(candidate, `"reconcile"`) {
			continue
		}
		var v struct {
			Reconcile []reconcileGroup `json:"reconcile"`
		}
		if err := json.Unmarshal([]byte(candidate), &v); err == nil {
			return v.Reconcile
		}
	}
	return nil
}

func truncateReason(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > reconcileMaxReason {
		return string(r[:reconcileMaxReason])
	}
	return s
}
