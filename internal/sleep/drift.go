package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/values"
	"github.com/Forest-Isle/daimon/internal/world"
)

const driftJournalLimit = 30

const driftSystemPrompt = `You are the drift-detection phase of a personal agent's sleep cycle. You are given the agent's ACTIVE values (durable principles that authorize autonomous action) and a log of RECENT activity (decisions, outcomes, corrections). Identify only values that recent activity CLEARLY CONTRADICTS or that the user has visibly moved away from — never values that are merely untested or absent from recent activity. Be conservative: when in doubt, do not flag. Marking a value drifting revokes its authority until the user reconfirms it, so a false positive only causes a harmless re-ask, but flagging sound values erodes trust — prefer silence over guessing. Respond with ONLY a JSON object of the form {"drifting":[{"id":"<value id>","reason":"<one sentence>"}]} and nothing else. Return {"drifting":[]} when no active value is contradicted.`

// valueLister is the slice of *values.Store the drift job needs: enumerate
// entries and transition one to drifting. The narrow interface keeps the job
// unit-testable without a real markdown store.
type valueLister interface {
	List() []values.Entry
	MarkDrifting(ctx context.Context, id, reason string) (values.Entry, bool, error)
}

// DriftJob detects active values that recent activity contradicts and marks them
// drifting, so they stop authorizing autonomous action until the user reconfirms
// them (which re-runs ask-once via the action value gate). It mutates only value
// state and appends one journal note per flagged value; it never deletes a value.
type DriftJob struct {
	values     valueLister
	world      *world.Store
	summarizer Summarizer
}

// NewDriftJob builds the drift job over a value store, a world store (for recent
// activity and the audit note), and an LLM summarizer (the drift judge).
func NewDriftJob(v valueLister, w *world.Store, s Summarizer) *DriftJob {
	return &DriftJob{values: v, world: w, summarizer: s}
}

func (j *DriftJob) Name() string { return "drift" }

func (j *DriftJob) Run(ctx context.Context) (string, error) {
	if j.values == nil || j.world == nil || j.summarizer == nil {
		return "", fmt.Errorf("drift: value store, world store, and summarizer are required")
	}

	active := activeValues(j.values.List())
	if len(active) == 0 {
		return "no active values to check", nil
	}
	entries, err := j.world.ListJournal(ctx, "", driftJournalLimit)
	if err != nil {
		return "", fmt.Errorf("drift: read journal: %w", err)
	}

	content, empty := buildDriftInput(active, entries)
	if empty {
		return "no recent activity to compare", nil
	}

	raw, err := j.summarizer.Complete(ctx, driftSystemPrompt, content)
	if err != nil {
		return "", fmt.Errorf("drift: judge: %w", err)
	}
	flags, err := parseDriftVerdict(raw)
	if err != nil {
		return "", fmt.Errorf("drift: parse verdict: %w", err)
	}

	valid := activeIDs(active)
	marked := 0
	for _, f := range flags {
		id := strings.TrimSpace(f.ID)
		if id == "" || !valid[id] {
			continue // ignore hallucinated or already-inactive ids
		}
		_, changed, err := j.values.MarkDrifting(ctx, id, f.Reason)
		if err != nil {
			return "", fmt.Errorf("drift: mark %s: %w", id, err)
		}
		if !changed {
			continue
		}
		marked++
		note, mErr := json.Marshal(world.JournalEntry{
			Kind:    "drift",
			Summary: fmt.Sprintf("Value %s marked drifting", id),
			Detail:  strings.TrimSpace(f.Reason),
		})
		if mErr != nil {
			return "", fmt.Errorf("drift: marshal note: %w", mErr)
		}
		if err := j.world.Apply(ctx, "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
			return "", fmt.Errorf("drift: record note: %w", err)
		}
	}

	if marked == 0 {
		return "no drift detected", nil
	}
	return fmt.Sprintf("marked %d value(s) drifting", marked), nil
}

func activeValues(all []values.Entry) []values.Entry {
	var out []values.Entry
	for _, e := range all {
		if e.State == values.StateActive {
			out = append(out, e)
		}
	}
	return out
}

func activeIDs(active []values.Entry) map[string]bool {
	m := make(map[string]bool, len(active))
	for _, e := range active {
		m[e.ID] = true
	}
	return m
}

// buildDriftInput renders the active values and recent activity for the judge.
// It returns empty=true when there is no recent activity to compare against, so
// the job can skip a pointless (and false-positive-prone) LLM call.
func buildDriftInput(active []values.Entry, entries []world.JournalEntry) (string, bool) {
	var jb strings.Builder
	hasJournal := false
	for _, e := range entries {
		summary := oneLine(e.Summary)
		detail := oneLine(e.Detail)
		if summary == "" && detail == "" {
			continue // truly empty entry: not activity
		}
		kind := e.Kind
		if kind == "" {
			kind = "entry"
		}
		// Include detail: contradictions are often recorded there (outcome/correction
		// bodies), and a detail-only entry must still count as activity or drift would
		// wrongly skip and leave an unsafe value active.
		switch {
		case summary != "" && detail != "":
			fmt.Fprintf(&jb, "- [%s] %s — %s\n", kind, summary, detail)
		case summary != "":
			fmt.Fprintf(&jb, "- [%s] %s\n", kind, summary)
		default:
			fmt.Fprintf(&jb, "- [%s] %s\n", kind, detail)
		}
		hasJournal = true
	}
	if !hasJournal {
		return "", true
	}

	var b strings.Builder
	b.WriteString("## Active values\n")
	for _, e := range active {
		fmt.Fprintf(&b, "- id=%s [%s] %s\n", e.ID, e.Domain, oneLine(e.Statement))
	}
	b.WriteString("\n## Recent activity\n")
	b.WriteString(jb.String())
	return b.String(), false
}

type driftFlag struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// parseDriftVerdict extracts the {"drifting":[...]} object from the judge's
// reply, tolerating code fences and surrounding prose. It scans for balanced,
// string-aware top-level JSON objects and unmarshals the first one that carries
// a "drifting" key, so braces inside prose or reason strings do not corrupt the
// parse. Anything unparseable is treated as "no drift" (conservative, fail-safe)
// rather than failing the whole sleep cycle.
func parseDriftVerdict(raw string) ([]driftFlag, error) {
	for _, candidate := range jsonObjectCandidates(raw) {
		if !strings.Contains(candidate, `"drifting"`) {
			continue
		}
		var v struct {
			Drifting []driftFlag `json:"drifting"`
		}
		if err := json.Unmarshal([]byte(candidate), &v); err == nil {
			return v.Drifting, nil
		}
	}
	return nil, nil
}

// jsonObjectCandidates returns the balanced top-level {...} spans in s, in order.
// It tracks JSON string state (and escapes) so braces inside string values do not
// throw off the brace depth count.
func jsonObjectCandidates(s string) []string {
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
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
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

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
