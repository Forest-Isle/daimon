package evolution

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// PreferenceLearner implements the Hook interface for Loop 1 of the
// self-evolution engine. It observes successful reflection events and
// extracts user-preference signals across three categories:
//
//   - tool_preference:      which tools consistently appear in successful episodes
//   - complexity_handling:   which task-complexity levels the user succeeds at
//   - replan_tendency:       how frequently replanning occurs on successful tasks
//
// Preferences gain confidence through repeated observation (Confidence =
// min(1.0, Count*0.2)). When the in-memory store reaches MaxPreferences, the
// entry with the lowest confidence (ties broken by oldest LastSeen) is evicted.
//
// All public methods are safe for concurrent use.
type PreferenceLearner struct {
	cfg         PreferenceConfig
	preferences map[string]*PreferenceEntry
	mu          sync.RWMutex
}

// PreferenceEntry represents a single learned preference with confidence
// tracking. Confidence grows with each observation: min(1.0, Count*0.2).
type PreferenceEntry struct {
	Category   string    // e.g. "tool_preference", "complexity_handling", "replan_tendency"
	Key        string    // unique identifier within the category
	Value      string    // human-readable preference value
	Confidence float64   // [0.0, 1.0]
	Count      int       // observation count
	LastSeen   time.Time // most recent observation
}

// NewPreferenceLearner creates a PreferenceLearner with the given configuration.
func NewPreferenceLearner(cfg PreferenceConfig) *PreferenceLearner {
	return &PreferenceLearner{
		cfg:         cfg,
		preferences: make(map[string]*PreferenceEntry),
	}
}

// Name returns the hook identifier used for logging.
func (p *PreferenceLearner) Name() string { return "preference_learner" }

// OnReflectionComplete extracts preference signals from a successful
// reflection event. Only events that both succeeded and meet the configured
// MinConfidence threshold produce preference signals.
func (p *PreferenceLearner) OnReflectionComplete(_ context.Context, event ReflectionEvent) {
	if !p.cfg.Enabled || !event.Succeeded {
		return
	}
	if event.Confidence < p.cfg.MinConfidence {
		return
	}

	// Signal 1 – tool_preference: reinforce each tool that contributed to success.
	for _, tool := range event.ToolsUsed {
		if tool != "" {
			p.recordPreference("tool_preference", tool, "preferred")
		}
	}

	// Signal 2 – complexity_handling: reinforce the complexity level on success.
	if event.Complexity != "" {
		p.recordPreference("complexity_handling", event.Complexity, "handles_well")
	}

	// Signal 3 – replan_tendency: categorise the user's replanning behaviour.
	// ReplanCount == 1 is considered ambiguous and intentionally skipped.
	switch {
	case event.ReplanCount >= 2:
		p.recordPreference("replan_tendency", "uses_replans", "approved")
	case event.ReplanCount == 0:
		p.recordPreference("replan_tendency", "no_replans", "preferred")
	}

	slog.Debug("preference_learner: signals extracted",
		"session_id", event.SessionID,
		"tools", len(event.ToolsUsed),
		"complexity", event.Complexity,
		"replan_count", event.ReplanCount,
	)
}

// OnEpisodeComplete is a no-op for PreferenceLearner.
func (p *PreferenceLearner) OnEpisodeComplete(_ context.Context, _ EpisodeEvent) {}

// OnToolExecuted is a no-op for PreferenceLearner.
func (p *PreferenceLearner) OnToolExecuted(_ context.Context, _ ToolExecEvent) {}

// recordPreference upserts a preference entry under the write lock.
// New entries start with Count=1 and Confidence=0.2. Existing entries have
// their count incremented and confidence recalculated. When the store exceeds
// MaxPreferences the lowest-confidence entry is evicted.
func (p *PreferenceLearner) recordPreference(category, key, value string) {
	prefKey := prefMapKey(category, key)
	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.preferences[prefKey]; ok {
		entry.Count++
		entry.Confidence = clampConfidence(entry.Count)
		entry.LastSeen = now
		return
	}

	// New entry — evict before inserting if at capacity.
	if p.cfg.MaxPreferences > 0 && len(p.preferences) >= p.cfg.MaxPreferences {
		p.evictLowestLocked()
	}

	p.preferences[prefKey] = &PreferenceEntry{
		Category:   category,
		Key:        key,
		Value:      value,
		Confidence: 0.2,
		Count:      1,
		LastSeen:   now,
	}
}

// GetPreferences returns preference entries for the given category, sorted by
// confidence descending. Entries below MinConfidence are excluded.
func (p *PreferenceLearner) GetPreferences(category string) []PreferenceEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []PreferenceEntry
	for _, entry := range p.preferences {
		if entry.Category == category && entry.Confidence >= p.cfg.MinConfidence {
			result = append(result, *entry)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})
	return result
}

// GetTopPreferences returns the top n preferences across all categories,
// sorted by confidence descending. If fewer than n entries exist, all are
// returned. Entries below MinConfidence are excluded.
func (p *PreferenceLearner) GetTopPreferences(n int) []PreferenceEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]PreferenceEntry, 0, len(p.preferences))
	for _, entry := range p.preferences {
		if entry.Confidence >= p.cfg.MinConfidence {
			result = append(result, *entry)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	if n > len(result) {
		n = len(result)
	}
	return result[:n]
}

// BuildPromptSection returns a human-readable preference summary suitable for
// injection into the PLAN or PERCEIVE phase prompt. Returns empty string when
// no preferences have reached MinConfidence yet.
func (p *PreferenceLearner) BuildPromptSection() string {
	toolPrefs := p.GetPreferences("tool_preference")
	compPrefs := p.GetPreferences("complexity_handling")
	replanPrefs := p.GetPreferences("replan_tendency")

	if len(toolPrefs) == 0 && len(compPrefs) == 0 && len(replanPrefs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("USER PREFERENCES (learned from past interactions):\n")

	if len(toolPrefs) > 0 {
		b.WriteString("- Preferred tools: ")
		names := make([]string, 0, len(toolPrefs))
		for _, tp := range toolPrefs {
			if len(names) >= 5 {
				break
			}
			names = append(names, tp.Key)
		}
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n")
	}

	if len(compPrefs) > 0 {
		b.WriteString("- Handles well: ")
		levels := make([]string, 0, len(compPrefs))
		for _, cp := range compPrefs {
			levels = append(levels, cp.Key+" complexity")
		}
		b.WriteString(strings.Join(levels, ", "))
		b.WriteString("\n")
	}

	if len(replanPrefs) > 0 {
		top := replanPrefs[0]
		if top.Key == "uses_replans" {
			b.WriteString("- This user benefits from replanning on failure\n")
		} else if top.Key == "no_replans" {
			b.WriteString("- This user prefers direct execution without replanning\n")
		}
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// prefMapKey builds the composite map key "category:key".
func prefMapKey(category, key string) string {
	return category + ":" + key
}

// clampConfidence computes min(1.0, count*0.2).
func clampConfidence(count int) float64 {
	c := float64(count) * 0.2
	if c > 1.0 {
		return 1.0
	}
	return c
}

// evictLowestLocked removes the entry with the lowest confidence; ties are
// broken by oldest LastSeen. Caller must hold p.mu in write mode.
func (p *PreferenceLearner) evictLowestLocked() {
	var (
		evictKey  string
		evictConf = 2.0 // higher than any possible confidence
		evictTime time.Time
		first     = true
	)
	for k, entry := range p.preferences {
		if first ||
			entry.Confidence < evictConf ||
			(entry.Confidence == evictConf && entry.LastSeen.Before(evictTime)) {
			evictKey = k
			evictConf = entry.Confidence
			evictTime = entry.LastSeen
			first = false
		}
	}
	if evictKey != "" {
		slog.Debug("preference_learner: evicted entry",
			"key", evictKey,
			"confidence", evictConf,
		)
		delete(p.preferences, evictKey)
	}
}
