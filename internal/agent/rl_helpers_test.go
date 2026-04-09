package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/rl"
)

func TestBuildInitialRLState(t *testing.T) {
	state := &CognitiveState{
		Goal: Goal{
			Raw:        "Write a shell script to deploy the app",
			Complexity: ComplexityComplex,
		},
		UserMessage:      "Write a shell script to deploy the app",
		RelevantMemories: make([]memory.SearchResult, 3),
		RecentHistory:    make([]CompletionMessage, 5),
		KnowledgeContext: []string{"doc1", "doc2"},
		GraphContext:     []string{"triple1"},
		Skills:           "some skills",
		Agents:           "",
		Personality:      "friendly",
	}

	rlState := buildInitialRLState(state, 8)

	// Complexity should be complex (one-hot)
	if rlState.ComplexityComplex != 1 {
		t.Errorf("expected ComplexityComplex=1, got %f", rlState.ComplexityComplex)
	}
	if rlState.ComplexitySimple != 0 {
		t.Errorf("expected ComplexitySimple=0, got %f", rlState.ComplexitySimple)
	}
	if rlState.ComplexityModerate != 0 {
		t.Errorf("expected ComplexityModerate=0, got %f", rlState.ComplexityModerate)
	}

	// Memory count: 3/10 = 0.3
	if rlState.MemoryCount != 0.3 {
		t.Errorf("expected MemoryCount=0.3, got %f", rlState.MemoryCount)
	}

	// Knowledge count: 2/10 = 0.2
	if rlState.KnowledgeCount != 0.2 {
		t.Errorf("expected KnowledgeCount=0.2, got %f", rlState.KnowledgeCount)
	}

	// Graph count: 1/10 = 0.1
	if rlState.GraphCount != 0.1 {
		t.Errorf("expected GraphCount=0.1, got %f", rlState.GraphCount)
	}

	// History: 5/20 = 0.25
	if rlState.HistoryLength != 0.25 {
		t.Errorf("expected HistoryLength=0.25, got %f", rlState.HistoryLength)
	}

	// Tools: 8/20 = 0.4
	if rlState.ToolCount != 0.4 {
		t.Errorf("expected ToolCount=0.4, got %f", rlState.ToolCount)
	}

	// Binary features
	if rlState.HasSkills != 1 {
		t.Errorf("expected HasSkills=1, got %f", rlState.HasSkills)
	}
	if rlState.HasAgents != 0 {
		t.Errorf("expected HasAgents=0, got %f", rlState.HasAgents)
	}
	if rlState.HasPersonality != 1 {
		t.Errorf("expected HasPersonality=1, got %f", rlState.HasPersonality)
	}

	// Word count: 8 words / 100 = 0.08
	if rlState.WordCount != 0.08 {
		t.Errorf("expected WordCount=0.08, got %f", rlState.WordCount)
	}
}

func TestUpdateRLStateWithPlan(t *testing.T) {
	s := &rl.RLState{}
	plan := &TaskPlan{
		SubTasks:          make([]*SubTask, 4),
		OverallConfidence: 0.85,
		ReplanCount:       1,
	}

	updateRLStateWithPlan(s, plan)

	// SubTaskCount: 4/10 = 0.4
	if s.SubTaskCount != 0.4 {
		t.Errorf("expected SubTaskCount=0.4, got %f", s.SubTaskCount)
	}
	if s.PlanConfidence != 0.85 {
		t.Errorf("expected PlanConfidence=0.85, got %f", s.PlanConfidence)
	}
	// ReplanCount: 1/5 = 0.2
	if s.ReplanCount != 0.2 {
		t.Errorf("expected ReplanCount=0.2, got %f", s.ReplanCount)
	}
}

func TestUpdateRLStateWithObservation(t *testing.T) {
	s := &rl.RLState{}
	obs := &ObservationResult{
		SuccessCount:    3,
		FailureCount:    1,
		DeniedCount:     0,
		OverallProgress: 0.75,
		ErrorPatterns:   []string{"network_error"},
	}

	updateRLStateWithObservation(s, obs)

	if s.SuccessCount != 0.3 {
		t.Errorf("expected SuccessCount=0.3, got %f", s.SuccessCount)
	}
	if s.FailureCount != 0.1 {
		t.Errorf("expected FailureCount=0.1, got %f", s.FailureCount)
	}
	if s.DeniedCount != 0 {
		t.Errorf("expected DeniedCount=0, got %f", s.DeniedCount)
	}
	if s.Progress != 0.75 {
		t.Errorf("expected Progress=0.75, got %f", s.Progress)
	}
	if s.ErrorPatternCnt != 0.2 {
		t.Errorf("expected ErrorPatternCnt=0.2, got %f", s.ErrorPatternCnt)
	}
}

func TestComputeSimpleEpisodeReward(t *testing.T) {
	tests := []struct {
		name       string
		reflection *Reflection
		obs        *ObservationResult
		wantMin    float64
		wantMax    float64
	}{
		{
			name:       "nil reflection",
			reflection: nil,
			obs:        nil,
			wantMin:    -0.5,
			wantMax:    -0.5,
		},
		{
			name:       "succeeded with full progress",
			reflection: &Reflection{Succeeded: true},
			obs:        &ObservationResult{OverallProgress: 1.0},
			wantMin:    1.4,
			wantMax:    1.6,
		},
		{
			name:       "failed with half progress",
			reflection: &Reflection{Succeeded: false},
			obs:        &ObservationResult{OverallProgress: 0.5},
			wantMin:    -0.8,
			wantMax:    -0.7,
		},
		{
			name:       "succeeded no obs",
			reflection: &Reflection{Succeeded: true},
			obs:        nil,
			wantMin:    0.9,
			wantMax:    1.1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeSimpleEpisodeReward(tc.reflection, tc.obs)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("expected reward in [%f, %f], got %f", tc.wantMin, tc.wantMax, got)
			}
		})
	}
}

func TestComputeReflectionBonus(t *testing.T) {
	tests := []struct {
		name       string
		reflection *Reflection
		want       float64
	}{
		{
			name:       "nil reflection",
			reflection: nil,
			want:       0.0,
		},
		{
			name: "lessons learned gives bonus",
			reflection: &Reflection{
				LessonsLearned: []string{"avoid timeout with large files"},
			},
			want: 0.15,
		},
		{
			name: "suggested adjustment gives small bonus",
			reflection: &Reflection{
				SuggestedAdjustment: "try streaming approach",
			},
			want: 0.05,
		},
		{
			name: "all bonuses combined",
			reflection: &Reflection{
				LessonsLearned:      []string{"lesson 1", "lesson 2"},
				SuggestedAdjustment: "try different approach",
			},
			want: 0.20, // 0.15 + 0.05
		},
		{
			name:       "empty reflection gives no bonus",
			reflection: &Reflection{},
			want:       0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeReflectionBonus(tc.reflection)
			diff := got - tc.want
			if diff < -0.001 || diff > 0.001 {
				t.Errorf("got %f, want %f", got, tc.want)
			}
		})
	}
}

func TestEpisodeCollectorConcurrency(t *testing.T) {
	collector := &EpisodeCollector{
		State:     &rl.RLState{},
		StartTime: time.Now(),
	}

	const goroutines = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			collector.Add(rl.Experience{
				State:  &rl.RLState{ComplexitySimple: float64(idx)},
				Action: []float64{float64(idx)},
				Reward: float64(idx) / 100,
				Done:   false,
				Level:  rl.LevelBandit,
			})
		}(i)
	}
	wg.Wait()

	experiences := collector.GetExperiences()
	if len(experiences) != goroutines {
		t.Errorf("expected %d experiences, got %d", goroutines, len(experiences))
	}
}

func TestPPOStrategyApplication(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		adj        float64
		want       float64
	}{
		{"positive adjustment", 0.7, 0.15, 0.85},
		{"negative adjustment", 0.3, -0.15, 0.15},
		{"clamp to 1", 0.95, 0.2, 1.0},
		{"clamp to 0", 0.05, -0.2, 0.0},
		{"zero adjustment", 0.5, 0.0, 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampRL(tc.confidence+tc.adj, 0, 1)
			if got != tc.want {
				t.Errorf("expected %f, got %f", tc.want, got)
			}
		})
	}
}

func TestApplyDQNReplanAdjustment(t *testing.T) {
	tests := []struct {
		name           string
		llmConfidence  float64
		dqnAction      rl.ReplanActionType
		dqnWeight      float64
		wantConfidence float64
		wantAbort      bool
	}{
		{
			name:           "DQN continue boosts confidence",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionContinue,
			dqnWeight:      0.3,
			wantConfidence: 0.4*0.7 + 1.0*0.3, // 0.58
			wantAbort:      false,
		},
		{
			name:           "DQN adjust keeps confidence unchanged",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionAdjust,
			dqnWeight:      0.3,
			wantConfidence: 0.4*0.7 + 0.5*0.3, // 0.43
			wantAbort:      false,
		},
		{
			name:           "DQN abort signals abort",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionAbort,
			dqnWeight:      0.3,
			wantConfidence: 0.4, // unchanged
			wantAbort:      true,
		},
		{
			name:           "zero weight means DQN has no effect",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionContinue,
			dqnWeight:      0.0,
			wantConfidence: 0.4,
			wantAbort:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adj, abort := applyDQNReplanAdjustment(tc.llmConfidence, tc.dqnAction, tc.dqnWeight)
			if abort != tc.wantAbort {
				t.Errorf("abort: got %v, want %v", abort, tc.wantAbort)
			}
			if !abort {
				diff := adj - tc.wantConfidence
				if diff < -0.01 || diff > 0.01 {
					t.Errorf("confidence: got %.4f, want %.4f", adj, tc.wantConfidence)
				}
			}
		})
	}
}
