package evolution

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolPattern records statistics for a recurring tool-usage pattern discovered
// across multiple episodes. The pattern key is the sorted, pipe-joined list of
// tool names (e.g. "bash|file_write").
type ToolPattern struct {
	ID        string    // canonical key: sorted tool names joined by "|"
	Tools     []string  // individual tool names (sorted)
	AvgReward float64   // running average of TotalReward across episodes
	Count     int       // number of episodes containing this pattern
	FirstSeen time.Time // timestamp of the first episode that contained this pattern
	LastSeen  time.Time // timestamp of the most recent episode
	// Context from the most recent episode that included this pattern (for drafts / LLM).
	LastGoal              string
	LastComplexity        string
	LastSucceeded         bool
	LastLessons           []string
	LastCollapsedSequence []string
	LastTotalReward       float64
}

// PatternTracker extracts contiguous tool-subsequences from episodes and
// maintains running statistics for each unique pattern. It is safe for
// concurrent use.
type PatternTracker struct {
	mu       sync.Mutex
	patterns map[string]*ToolPattern
}

// NewPatternTracker creates an empty tracker ready for use.
func NewPatternTracker() *PatternTracker {
	return &PatternTracker{
		patterns: make(map[string]*ToolPattern),
	}
}

// collapseConsecutiveTools turns raw traces like [bash, bash, bash, file_read]
// into a step list [bash, file_read] so we learn *task* phases, not repeated
// invocations of the same tool.
func collapseConsecutiveTools(seq []string) []string {
	var out []string
	for _, t := range seq {
		if t == "" {
			continue
		}
		if len(out) == 0 || t != out[len(out)-1] {
			out = append(out, t)
		}
	}
	return out
}

// countUniqueInSorted returns the number of distinct tool names in a sorted slice.
func countUniqueInSorted(tools []string) int {
	if len(tools) == 0 {
		return 0
	}
	u := 1
	for i := 1; i < len(tools); i++ {
		if tools[i] != tools[i-1] {
			u++
		}
	}
	return u
}

// patternKey builds a canonical, order-independent key by sorting tool names
// alphabetically and joining them with "|".
func patternKey(tools []string) string {
	sorted := make([]string, len(tools))
	copy(sorted, tools)
	sort.Strings(sorted)
	return strings.Join(sorted, "|")
}

// TrackEpisode extracts every contiguous subsequence of length 2–4 from a
// run-length collapsed view of event.ToolSequence and updates the
// corresponding pattern statistics.
func (pt *PatternTracker) TrackEpisode(event EpisodeEvent) {
	seq := collapseConsecutiveTools(event.ToolSequence)
	n := len(seq)
	if n < 2 {
		return
	}

	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	goal := event.Goal
	lessons := append([]string(nil), event.LessonsLearned...)
	collapsedCopy := append([]string(nil), seq...)

	maxLen := 4
	if n < maxLen {
		maxLen = n
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	for length := 2; length <= maxLen; length++ {
		for start := 0; start <= n-length; start++ {
			window := seq[start : start+length]
			key := patternKey(window)
			// A window like [bash, bash] collapses to one element before we start — still guard.
			if countUniqueInSorted(sortedCopy(window)) < 2 {
				continue
			}

			p, exists := pt.patterns[key]
			if !exists {
				tools := make([]string, len(window))
				copy(tools, window)
				sort.Strings(tools)

				p = &ToolPattern{
					ID:        key,
					Tools:     tools,
					FirstSeen: now,
				}
				pt.patterns[key] = p
			}

			// Welford's online mean: avg' = avg + (x - avg) / n
			p.Count++
			p.AvgReward += (event.TotalReward - p.AvgReward) / float64(p.Count)
			p.LastSeen = now
			p.LastGoal = goal
			p.LastComplexity = event.Complexity
			p.LastSucceeded = event.Succeeded
			p.LastLessons = append([]string(nil), lessons...)
			p.LastCollapsedSequence = append([]string(nil), collapsedCopy...)
			p.LastTotalReward = event.TotalReward
		}
	}
}

func sortedCopy(tools []string) []string {
	s := make([]string, len(tools))
	copy(s, tools)
	sort.Strings(s)
	return s
}

// GetCandidates returns all patterns whose Count >= countThreshold AND
// AvgReward >= rewardThreshold AND unique tool count >= minUniqueTools.
// The returned slice contains copies so callers may use them freely.
func (pt *PatternTracker) GetCandidates(countThreshold int, rewardThreshold float64, minUniqueTools int) []ToolPattern {
	if minUniqueTools < 1 {
		minUniqueTools = 1
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	var out []ToolPattern
	for _, p := range pt.patterns {
		if p.Count < countThreshold || p.AvgReward < rewardThreshold {
			continue
		}
		if countUniqueInSorted(p.Tools) < minUniqueTools {
			continue
		}
		cp := *p
		cp.Tools = make([]string, len(p.Tools))
		copy(cp.Tools, p.Tools)
		if len(p.LastLessons) > 0 {
			cp.LastLessons = append([]string(nil), p.LastLessons...)
		}
		if len(p.LastCollapsedSequence) > 0 {
			cp.LastCollapsedSequence = append([]string(nil), p.LastCollapsedSequence...)
		}
		out = append(out, cp)
	}
	return out
}
