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

// patternKey builds a canonical, order-independent key by sorting tool names
// alphabetically and joining them with "|".
func patternKey(tools []string) string {
	sorted := make([]string, len(tools))
	copy(sorted, tools)
	sort.Strings(sorted)
	return strings.Join(sorted, "|")
}

// TrackEpisode extracts every contiguous subsequence of length 2–4 from
// event.ToolSequence and updates the corresponding pattern statistics.
//
// For a sequence [A, B, C, D] the extracted windows are:
//
//	len 2: [A,B] [B,C] [C,D]
//	len 3: [A,B,C] [B,C,D]
//	len 4: [A,B,C,D]
func (pt *PatternTracker) TrackEpisode(event EpisodeEvent) {
	seq := event.ToolSequence
	n := len(seq)
	if n < 2 {
		return
	}

	maxLen := 4
	if n < maxLen {
		maxLen = n
	}

	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	for length := 2; length <= maxLen; length++ {
		for start := 0; start <= n-length; start++ {
			window := seq[start : start+length]
			key := patternKey(window)

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
		}
	}
}

// GetCandidates returns all patterns whose Count >= countThreshold AND
// AvgReward >= rewardThreshold. The returned slice contains copies so callers
// may use them freely without holding the tracker lock.
func (pt *PatternTracker) GetCandidates(countThreshold int, rewardThreshold float64) []ToolPattern {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	var out []ToolPattern
	for _, p := range pt.patterns {
		if p.Count >= countThreshold && p.AvgReward >= rewardThreshold {
			cp := *p
			cp.Tools = make([]string, len(p.Tools))
			copy(cp.Tools, p.Tools)
			out = append(out, cp)
		}
	}
	return out
}
