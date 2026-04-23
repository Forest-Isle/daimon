package evolution

import (
	"math"
	"testing"
	"time"
)

func TestPatternKey(t *testing.T) {
	tests := []struct {
		tools []string
		want  string
	}{
		{[]string{"write", "read"}, "read|write"},
		{[]string{"c", "a", "b"}, "a|b|c"},
		{[]string{"x", "x"}, "x|x"},
	}
	for _, tt := range tests {
		got := patternKey(tt.tools)
		if got != tt.want {
			t.Errorf("patternKey(%v) = %q, want %q", tt.tools, got, tt.want)
		}
	}
}

func TestPatternTracker_EmptyAndShort(t *testing.T) {
	pt := NewPatternTracker()

	// Empty sequence produces nothing.
	pt.TrackEpisode(EpisodeEvent{ToolSequence: nil, TotalReward: 1})
	if got := pt.GetCandidates(1, 0, 2); len(got) != 0 {
		t.Fatalf("nil sequence: want 0 candidates, got %d", len(got))
	}

	// Single-tool sequence also produces nothing (min window = 2).
	pt.TrackEpisode(EpisodeEvent{ToolSequence: []string{"a"}, TotalReward: 1})
	if got := pt.GetCandidates(1, 0, 2); len(got) != 0 {
		t.Fatalf("single tool: want 0 candidates, got %d", len(got))
	}
}

func TestPatternTracker_WindowCounts(t *testing.T) {
	tests := []struct {
		name  string
		seq   []string
		count int // expected distinct pattern keys
	}{
		// [A,B] → 1 window of len 2
		{"len2", []string{"a", "b"}, 1},
		// [A,B,C] → len2: [a,b],[b,c] = 2; len3: [a,b,c] = 1 → 3
		{"len3", []string{"a", "b", "c"}, 3},
		// [A,B,C,D] → len2: 3, len3: 2, len4: 1 → 6
		{"len4", []string{"a", "b", "c", "d"}, 6},
		// [A,B,C,D,E] → len2: 4, len3: 3, len4: 2 → 9
		{"len5", []string{"a", "b", "c", "d", "e"}, 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := NewPatternTracker()
			pt.TrackEpisode(EpisodeEvent{
				ToolSequence: tt.seq,
				TotalReward:  1.0,
				Timestamp:    time.Now(),
			})
			got := pt.GetCandidates(1, 0, 2)
			if len(got) != tt.count {
				t.Errorf("seq %v: want %d patterns, got %d", tt.seq, tt.count, len(got))
			}
		})
	}
}

func TestPatternTracker_RunningAverage(t *testing.T) {
	pt := NewPatternTracker()

	rewards := []float64{1.0, 0.5, 1.5}
	for _, r := range rewards {
		pt.TrackEpisode(EpisodeEvent{
			ToolSequence: []string{"a", "b"},
			TotalReward:  r,
			Timestamp:    time.Now(),
		})
	}

	candidates := pt.GetCandidates(1, 0, 2)
	var found bool
	for _, c := range candidates {
		if c.ID == "a|b" {
			found = true
			if c.Count != 3 {
				t.Errorf("count: want 3, got %d", c.Count)
			}
			// (1.0 + 0.5 + 1.5) / 3 = 1.0
			if math.Abs(c.AvgReward-1.0) > 1e-9 {
				t.Errorf("avg reward: want 1.0, got %f", c.AvgReward)
			}
		}
	}
	if !found {
		t.Fatal("pattern a|b not found")
	}
}

func TestPatternTracker_OrderIndependent(t *testing.T) {
	pt := NewPatternTracker()

	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"write", "read"},
		TotalReward:  0.8,
		Timestamp:    time.Now(),
	})
	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"read", "write"},
		TotalReward:  0.6,
		Timestamp:    time.Now(),
	})

	candidates := pt.GetCandidates(2, 0, 2)
	if len(candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "read|write" {
		t.Errorf("key: want read|write, got %s", candidates[0].ID)
	}
	if candidates[0].Count != 2 {
		t.Errorf("count: want 2, got %d", candidates[0].Count)
	}
}

func TestPatternTracker_GetCandidates_Filtering(t *testing.T) {
	pt := NewPatternTracker()

	// Pattern "a|b": 5 episodes, reward 0.8 each → avg 0.8
	for i := 0; i < 5; i++ {
		pt.TrackEpisode(EpisodeEvent{
			ToolSequence: []string{"a", "b"},
			TotalReward:  0.8,
			Timestamp:    time.Now(),
		})
	}
	// Pattern "c|d": 5 episodes, reward 0.3 each → avg 0.3
	for i := 0; i < 5; i++ {
		pt.TrackEpisode(EpisodeEvent{
			ToolSequence: []string{"c", "d"},
			TotalReward:  0.3,
			Timestamp:    time.Now(),
		})
	}
	// Pattern "e|f": 1 episode, reward 0.9 → below count threshold
	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"e", "f"},
		TotalReward:  0.9,
		Timestamp:    time.Now(),
	})

	// count >= 3, reward >= 0.5 → only a|b qualifies
	candidates := pt.GetCandidates(3, 0.5, 2)
	if len(candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "a|b" {
		t.Errorf("want a|b, got %s", candidates[0].ID)
	}
}

func TestPatternTracker_RepeatedToolRunCollapses(t *testing.T) {
	pt := NewPatternTracker()
	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"bash", "bash", "bash", "bash", "file_write"},
		TotalReward:  0.8,
		Timestamp:    time.Now(),
	})
	// No window should be only "bash" repeated — high-signal pattern is bash|file_write.
	candidates := pt.GetCandidates(1, 0, 2)
	for _, c := range candidates {
		if c.ID == "bash" || c.ID == "bash|bash" {
			t.Fatalf("unexpected homogeneous pattern: %q", c.ID)
		}
	}
}

func TestPatternTracker_TimestampTracking(t *testing.T) {
	pt := NewPatternTracker()

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"a", "b"},
		TotalReward:  1,
		Timestamp:    t1,
	})
	pt.TrackEpisode(EpisodeEvent{
		ToolSequence: []string{"a", "b"},
		TotalReward:  1,
		Timestamp:    t2,
	})

	candidates := pt.GetCandidates(1, 0, 2)
	for _, c := range candidates {
		if c.ID == "a|b" {
			if !c.FirstSeen.Equal(t1) {
				t.Errorf("FirstSeen: want %v, got %v", t1, c.FirstSeen)
			}
			if !c.LastSeen.Equal(t2) {
				t.Errorf("LastSeen: want %v, got %v", t2, c.LastSeen)
			}
		}
	}
}
