package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	distillOutcomeLimit = 200 // scan the last N episode outcomes (window-independent over outcomes)
	distillMinOutcomes  = 3   // need at least this many clean outcomes before judging
	distillMinPattern   = 3   // a candidate must cite at least this many real episodes
	distillCandidate    = "distill candidate: "
)

const distillSystemPrompt = `You are the skill-distillation phase of a personal agent's sleep cycle. You are given summaries of episodes the agent COMPLETED (each with a stable id). Find recurring task PATTERNS — the SAME kind of task handled successfully three or more times — that are worth turning into a reusable skill so the agent stops re-reasoning them from scratch. Be conservative: only group episodes that clearly performed the same repeatable task and succeeded; never group merely related, one-off, or failed/blocked tasks. For each pattern give a short stable name, a one-line description of the skill to build, and the ids of the covered episodes (use only ids from the list; a pattern needs at least three). Respond with ONLY a JSON object {"candidates":[{"name":"<short name>","skill":"<one-line skill description>","episode_ids":["<id>","<id>","<id>"]}]} and nothing else. Return {"candidates":[]} when no task recurs often enough to be worth automating.`

// DistillJob mines the journal for recurring, successful episode patterns and
// surfaces each as a "distill candidate" decision entry — the detection half of
// blueprint §4.8 skill distillation (情节→技能→反射). It does NOT generate skills,
// register reflexes, or promote anything: turning a candidate into a live skill is
// a separate, canary-gated step (the replay Canary gate, blueprint §4.10 mode 3),
// because an auto-promoted skill executes autonomously and is the blueprint's
// highest "带病转正" risk (§706). This job only writes append-only candidate
// decisions an operator (or a later promotion job) can act on.
//
// "All verified" (blueprint's distill criterion) has no full action-level source
// yet — Receipt.Verified is not aggregated per episode. As a conservative proxy
// this job mines only outcomes that both closed through episode_close (not
// framework salvage / failure) AND made no failing tool calls (the outcome
// detail's tool_failures=N signal), and leans on the conservative judge prompt to
// require genuine success. This is acceptable precisely because nothing here
// promotes; the proxy gates detection only, not execution.
type DistillJob struct {
	world      *world.Store
	summarizer Summarizer
}

// NewDistillJob builds the distill job over a world store (the episode-outcome
// source and candidate sink) and an LLM summarizer (the pattern judge).
func NewDistillJob(w *world.Store, s Summarizer) *DistillJob {
	return &DistillJob{world: w, summarizer: s}
}

func (j *DistillJob) Name() string { return "distill" }

func (j *DistillJob) Run(ctx context.Context) (string, error) {
	if j.world == nil || j.summarizer == nil {
		return "", fmt.Errorf("distill: world store and summarizer are required")
	}

	// Source the last N episode OUTCOMES directly (kind-filtered), not the last N
	// mixed journal entries: as the journal accumulates decision/fact/correction rows
	// (including the distill candidates this job itself writes), clean outcomes get
	// crowded out of any all-kinds window, so a pattern that recurs across time is
	// never co-visible to the judge. ListOutcomes keeps detection window-independent
	// over outcomes.
	entries, err := j.world.ListOutcomes(ctx, distillOutcomeLimit)
	if err != nil {
		return "", fmt.Errorf("distill: list outcomes: %w", err)
	}

	// Clean outcomes = episodes that closed cleanly. validIDs is the set a
	// candidate's cited episode ids must come from — a guard against the judge
	// hallucinating ids. Candidate de-duplication is handled per-candidate by a
	// deterministic id below (JournalExists), so it stays correct even after an old
	// candidate scrolls out of this bounded scan.
	var cleanOutcomes []world.JournalEntry
	validIDs := make(map[string]bool)
	for _, e := range entries {
		// A pattern is only distillation-worthy if the episode reached a clean,
		// fully-verified outcome: it closed through episode_close (not framework
		// salvage / failure), made no failing tool call, and every governed action it
		// took earned objective trust. world.ClassifyOutcome is the single source of
		// truth for that judgment — it owns the outcome detail encoding (salvaged /
		// tool_failures=N / unverified_actions=N) and the failEpisode summary markers —
		// so anything but OutcomeClean is excluded here. The conservative judge prompt
		// then further requires genuine success before grouping. An auto-closed
		// no-tool conversational turn classifies as clean but is excluded here: a
		// pure chat turn has no tool sequence to encode into a repeatable skill.
		detail := oneLine(e.Detail)
		if world.IsAutoClosed(detail) {
			continue
		}
		if world.ClassifyOutcome(detail, e.Summary) != world.OutcomeClean {
			continue
		}
		cleanOutcomes = append(cleanOutcomes, e)
		if e.EpisodeID != "" {
			validIDs[e.EpisodeID] = true
		}
	}

	if len(cleanOutcomes) < distillMinOutcomes {
		return "not enough successful episodes to distill", nil
	}

	content := buildDistillInput(cleanOutcomes)
	raw, err := j.summarizer.Complete(ctx, distillSystemPrompt, content)
	if err != nil {
		return "", fmt.Errorf("distill: judge: %w", err)
	}
	candidates := parseDistillCandidates(raw)
	if len(candidates) == 0 {
		return "no recurring patterns found", nil
	}

	surfaced := 0
	batch := make(map[string]bool) // candidate ids written this cycle (intra-batch dedup)
	for _, c := range candidates {
		name := oneLine(c.Name)
		if name == "" {
			continue
		}
		// Count only cited ids that are real clean-outcome episodes; a candidate that
		// does not actually cover >= distillMinPattern real episodes is dropped (the
		// judge under-supported it or hallucinated ids).
		realIDs := make([]string, 0, len(c.EpisodeIDs))
		citedSeen := make(map[string]bool)
		for _, id := range c.EpisodeIDs {
			id = strings.TrimSpace(id)
			if id != "" && validIDs[id] && !citedSeen[id] {
				citedSeen[id] = true
				realIDs = append(realIDs, id)
			}
		}
		if len(realIDs) < distillMinPattern {
			continue
		}

		// De-duplicate by a deterministic id derived from the name: a stable pattern
		// is recorded once even after its prior candidate scrolls past the bounded
		// journal scan above (window-independent), and never twice within one batch.
		id := distillCandidateID(name)
		if batch[id] {
			continue
		}
		exists, err := j.world.JournalExists(ctx, id)
		if err != nil {
			return "", fmt.Errorf("distill: dedup check: %w", err)
		}
		if exists {
			batch[id] = true
			continue
		}

		note, mErr := json.Marshal(world.JournalEntry{
			ID:      id,
			Kind:    "decision",
			Summary: distillCandidate + name,
			Detail:  distillDetail(oneLine(c.Skill), realIDs),
		})
		if mErr != nil {
			return "", fmt.Errorf("distill: marshal candidate: %w", mErr)
		}
		if err := j.world.Apply(ctx, "sleep", []world.Mutation{{Op: "journal.append", Body: note}}); err != nil {
			return "", fmt.Errorf("distill: record candidate %q: %w", name, err)
		}
		batch[id] = true
		surfaced++
	}

	if surfaced == 0 {
		return "no recurring patterns found", nil
	}
	return fmt.Sprintf("surfaced %d distill candidate(s)", surfaced), nil
}

// distillCandidateID derives a deterministic journal id from a candidate's name,
// so the same pattern (modulo casing/whitespace/punctuation) dedupes to one row
// via JournalExists regardless of how far back its prior entry is. The id is a
// readable Unicode-aware slug PLUS a hash of the normalized name: the slug keeps
// letters/digits of any script (so a Chinese or other non-ASCII name is not
// collapsed away), and the hash suffix guarantees two genuinely different names
// never share an id even if their slugs coincide.
func distillCandidateID(name string) string {
	norm := strings.ToLower(oneLine(name))
	var b strings.Builder
	prevUnderscore := false
	for _, r := range norm {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	h := fnv.New64a() // 64-bit: collision across a personal agent's candidate set is negligible
	_, _ = h.Write([]byte(norm))
	return fmt.Sprintf("distill_candidate_%s_%016x", slug, h.Sum64())
}

// distillDetail renders the candidate's skill description plus the episodes it
// covers into the decision's detail, so the append-only candidate is self-contained.
func distillDetail(skill string, ids []string) string {
	body := fmt.Sprintf("%d episodes: %s", len(ids), strings.Join(ids, ","))
	if skill != "" {
		return skill + " | " + body
	}
	return body
}

// buildDistillInput renders the clean outcomes (id + summary) for the judge.
func buildDistillInput(outcomes []world.JournalEntry) string {
	var b strings.Builder
	b.WriteString("## Completed episodes\n")
	for _, e := range outcomes {
		summary := oneLine(e.Summary)
		if summary == "" {
			summary = "(no summary)"
		}
		fmt.Fprintf(&b, "- id=%s %s\n", e.EpisodeID, summary)
	}
	return b.String()
}

type distillCandidateJSON struct {
	Name       string   `json:"name"`
	Skill      string   `json:"skill"`
	EpisodeIDs []string `json:"episode_ids"`
}

// parseDistillCandidates extracts the {"candidates":[...]} object from the judge's
// reply, tolerating code fences and surrounding prose via the same string-aware
// balanced-object scan the drift/reconcile jobs use. Anything unparseable is
// treated as "no candidates" (conservative, fail-safe) rather than failing the
// sleep cycle.
func parseDistillCandidates(raw string) []distillCandidateJSON {
	for _, candidate := range jsonObjectCandidates(raw) {
		if !strings.Contains(candidate, `"candidates"`) {
			continue
		}
		var v struct {
			Candidates []distillCandidateJSON `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(candidate), &v); err == nil {
			return v.Candidates
		}
	}
	return nil
}
