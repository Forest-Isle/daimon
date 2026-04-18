package agent

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseReflectResponse tests
// ---------------------------------------------------------------------------

func TestParseReflectResponse_WithReasoning(t *testing.T) {
	input := `{
  "reasoning": {
    "completeness": {"score": 24, "explanation": "All files analyzed"},
    "accuracy": {"score": 22, "explanation": "Minor false positive"},
    "efficiency": {"score": 20, "explanation": "One redundant step"},
    "relevance": {"score": 25, "explanation": "Directly answers the question"},
    "key_improvement": "Deduplicate search results"
  },
  "overall_confidence": 0.91,
  "succeeded": true,
  "lessons_learned": ["Parallel analysis saves time"],
  "suggested_adjustment": "",
  "final_answer": "Analyzed files successfully.",
  "needs_replan": false,
  "replan_reason": ""
}`

	ref, err := parseReflectResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Reasoning == nil {
		t.Fatal("expected Reasoning to be populated")
	}
	if ref.Reasoning.Completeness.Score != 24 {
		t.Errorf("completeness score = %d, want 24", ref.Reasoning.Completeness.Score)
	}
	if ref.Reasoning.Accuracy.Score != 22 {
		t.Errorf("accuracy score = %d, want 22", ref.Reasoning.Accuracy.Score)
	}
	if ref.Reasoning.Efficiency.Score != 20 {
		t.Errorf("efficiency score = %d, want 20", ref.Reasoning.Efficiency.Score)
	}
	if ref.Reasoning.Relevance.Score != 25 {
		t.Errorf("relevance score = %d, want 25", ref.Reasoning.Relevance.Score)
	}
	if ref.Reasoning.KeyImprovement != "Deduplicate search results" {
		t.Errorf("key_improvement = %q, want %q", ref.Reasoning.KeyImprovement, "Deduplicate search results")
	}
	if !ref.Succeeded {
		t.Error("expected succeeded = true")
	}
	if ref.FinalAnswer != "Analyzed files successfully." {
		t.Errorf("final_answer = %q", ref.FinalAnswer)
	}
}

func TestParseReflectResponse_WithoutReasoning(t *testing.T) {
	// Legacy format without the reasoning field — must still parse.
	input := `{
  "overall_confidence": 0.75,
  "succeeded": true,
  "lessons_learned": ["lesson1"],
  "suggested_adjustment": "",
  "final_answer": "Done.",
  "needs_replan": false,
  "replan_reason": ""
}`

	ref, err := parseReflectResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Reasoning != nil {
		t.Error("expected Reasoning to be nil for legacy format")
	}
	if ref.OverallConfidence != 0.75 {
		t.Errorf("overall_confidence = %v, want 0.75", ref.OverallConfidence)
	}
}

func TestParseReflectResponse_JSONCodeBlock(t *testing.T) {
	// LLM wraps JSON in a markdown code block — fallback parsing should handle it.
	input := "Here is my evaluation:\n```json\n" + `{
  "overall_confidence": 0.60,
  "succeeded": false,
  "lessons_learned": [],
  "suggested_adjustment": "retry",
  "final_answer": "Partial.",
  "needs_replan": true,
  "replan_reason": "failures"
}` + "\n```"

	ref, err := parseReflectResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.OverallConfidence != 0.60 {
		t.Errorf("overall_confidence = %v, want 0.60", ref.OverallConfidence)
	}
	if !ref.NeedsReplan {
		t.Error("expected needs_replan = true")
	}
}

func TestParseReflectResponse_BracketExtraction(t *testing.T) {
	// LLM outputs prose around JSON — third fallback extracts first {...}.
	input := `I analyzed the results carefully.

{"overall_confidence":0.42,"succeeded":false,"lessons_learned":[],"suggested_adjustment":"","final_answer":"Failed.","needs_replan":true,"replan_reason":"all tools denied"}

Hope this helps.`

	ref, err := parseReflectResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.OverallConfidence != 0.42 {
		t.Errorf("overall_confidence = %v, want 0.42", ref.OverallConfidence)
	}
}

func TestParseReflectResponse_InvalidJSON(t *testing.T) {
	_, err := parseReflectResponse("this is not json at all")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

// ---------------------------------------------------------------------------
// validateConfidenceFromDimensions tests
// ---------------------------------------------------------------------------

func TestValidateConfidenceFromDimensions_WithinThreshold(t *testing.T) {
	reasoning := &ReflectReasoning{
		Completeness: DimensionScore{Score: 23},
		Accuracy:     DimensionScore{Score: 23},
		Efficiency:   DimensionScore{Score: 22},
		Relevance:    DimensionScore{Score: 22},
	}
	// derived = 90/100 = 0.90, llm says 0.92 => deviation 0.02 < 0.15 => keep llm value
	result := validateConfidenceFromDimensions(0.92, reasoning)
	if result != 0.92 {
		t.Errorf("expected 0.92, got %v", result)
	}
}

func TestValidateConfidenceFromDimensions_ExceedsThreshold_Overconfident(t *testing.T) {
	reasoning := &ReflectReasoning{
		Completeness: DimensionScore{Score: 15},
		Accuracy:     DimensionScore{Score: 18},
		Efficiency:   DimensionScore{Score: 10},
		Relevance:    DimensionScore{Score: 15},
	}
	// derived = 58/100 = 0.58, llm says 0.80 => deviation 0.22 > 0.15 => use derived
	result := validateConfidenceFromDimensions(0.80, reasoning)
	if result != 0.58 {
		t.Errorf("expected 0.58, got %v", result)
	}
}

func TestValidateConfidenceFromDimensions_ExceedsThreshold_Underconfident(t *testing.T) {
	reasoning := &ReflectReasoning{
		Completeness: DimensionScore{Score: 20},
		Accuracy:     DimensionScore{Score: 20},
		Efficiency:   DimensionScore{Score: 20},
		Relevance:    DimensionScore{Score: 20},
	}
	// derived = 80/100 = 0.80, llm says 0.50 => deviation 0.30 > 0.15 => use derived
	result := validateConfidenceFromDimensions(0.50, reasoning)
	if result != 0.80 {
		t.Errorf("expected 0.80, got %v", result)
	}
}

func TestValidateConfidenceFromDimensions_NilReasoning(t *testing.T) {
	result := validateConfidenceFromDimensions(0.75, nil)
	if result != 0.75 {
		t.Errorf("expected 0.75, got %v", result)
	}
}

func TestValidateConfidenceFromDimensions_AllZero(t *testing.T) {
	reasoning := &ReflectReasoning{
		Completeness: DimensionScore{Score: 0},
		Accuracy:     DimensionScore{Score: 0},
		Efficiency:   DimensionScore{Score: 0},
		Relevance:    DimensionScore{Score: 0},
	}
	// derived = 0.0, llm says 0.5 => deviation 0.50 > 0.15 => use derived
	result := validateConfidenceFromDimensions(0.5, reasoning)
	if result != 0.0 {
		t.Errorf("expected 0.0, got %v", result)
	}
}

func TestValidateConfidenceFromDimensions_ExtremeImbalance(t *testing.T) {
	reasoning := &ReflectReasoning{
		Completeness: DimensionScore{Score: 25},
		Accuracy:     DimensionScore{Score: 0},
		Efficiency:   DimensionScore{Score: 25},
		Relevance:    DimensionScore{Score: 0},
	}
	// derived = 50/100 = 0.50, llm says 0.90 => deviation 0.40 > 0.15 => use derived
	result := validateConfidenceFromDimensions(0.90, reasoning)
	if result != 0.50 {
		t.Errorf("expected 0.50, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// DerivedConfidence tests
// ---------------------------------------------------------------------------

func TestReflectReasoning_DerivedConfidence(t *testing.T) {
	tests := []struct {
		name string
		r    ReflectReasoning
		want float64
	}{
		{
			name: "perfect score",
			r: ReflectReasoning{
				Completeness: DimensionScore{Score: 25},
				Accuracy:     DimensionScore{Score: 25},
				Efficiency:   DimensionScore{Score: 25},
				Relevance:    DimensionScore{Score: 25},
			},
			want: 1.0,
		},
		{
			name: "zero score",
			r: ReflectReasoning{
				Completeness: DimensionScore{Score: 0},
				Accuracy:     DimensionScore{Score: 0},
				Efficiency:   DimensionScore{Score: 0},
				Relevance:    DimensionScore{Score: 0},
			},
			want: 0.0,
		},
		{
			name: "mixed score",
			r: ReflectReasoning{
				Completeness: DimensionScore{Score: 24},
				Accuracy:     DimensionScore{Score: 22},
				Efficiency:   DimensionScore{Score: 20},
				Relevance:    DimensionScore{Score: 25},
			},
			want: 0.91,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r.DerivedConfidence()
			if got != tt.want {
				t.Errorf("DerivedConfidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDerivedConfidence_ClampAboveOne(t *testing.T) {
	// If LLM somehow outputs scores > 25, DerivedConfidence clamps to 1.0
	r := ReflectReasoning{
		Completeness: DimensionScore{Score: 30},
		Accuracy:     DimensionScore{Score: 30},
		Efficiency:   DimensionScore{Score: 30},
		Relevance:    DimensionScore{Score: 30},
	}
	got := r.DerivedConfidence()
	if got != 1.0 {
		t.Errorf("DerivedConfidence() = %v, want 1.0 (clamped)", got)
	}
}

// ---------------------------------------------------------------------------
// reflectJSONToReflection tests
// ---------------------------------------------------------------------------

func TestReflectJSONToReflection_CopiesReasoning(t *testing.T) {
	rj := reflectJSON{
		OverallConfidence: 0.85,
		Succeeded:         true,
		LessonsLearned:    []string{"lesson"},
		FinalAnswer:       "answer",
		Reasoning: &ReflectReasoning{
			Completeness:   DimensionScore{Score: 22, Explanation: "good"},
			Accuracy:       DimensionScore{Score: 20, Explanation: "ok"},
			Efficiency:     DimensionScore{Score: 21, Explanation: "fine"},
			Relevance:      DimensionScore{Score: 22, Explanation: "relevant"},
			KeyImprovement: "optimize search",
		},
	}

	ref := reflectJSONToReflection(rj)
	if ref.Reasoning == nil {
		t.Fatal("Reasoning should not be nil")
	}
	if ref.Reasoning.Completeness.Score != 22 {
		t.Errorf("Completeness.Score = %d, want 22", ref.Reasoning.Completeness.Score)
	}
	if ref.Reasoning.KeyImprovement != "optimize search" {
		t.Errorf("KeyImprovement = %q", ref.Reasoning.KeyImprovement)
	}
}

func TestReflectJSONToReflection_NilReasoning(t *testing.T) {
	rj := reflectJSON{
		OverallConfidence: 0.60,
		Succeeded:         false,
		FinalAnswer:       "partial",
	}

	ref := reflectJSONToReflection(rj)
	if ref.Reasoning != nil {
		t.Error("Reasoning should be nil for legacy format")
	}
	if ref.OverallConfidence != 0.60 {
		t.Errorf("OverallConfidence = %v, want 0.60", ref.OverallConfidence)
	}
}

// ---------------------------------------------------------------------------
// buildReflectUserMessage tests
// ---------------------------------------------------------------------------

func TestBuildReflectUserMessage_TruncationLimit(t *testing.T) {
	// Build a long output to verify it truncates at 1500 not 500.
	longOutput := strings.Repeat("x", 2000)
	state := &CognitiveState{Goal: Goal{Raw: "test goal"}}
	plan := &TaskPlan{Summary: "test plan"}
	obs := &ObservationResult{
		Observations: []Observation{
			{SubTaskID: "t1", ToolName: "bash", Output: longOutput},
		},
		SuccessCount: 1,
	}

	msg := buildReflectUserMessage(state, plan, obs, 0)

	// The truncated output should be 1500 chars + "...[truncated]"
	if !strings.Contains(msg, "...[truncated]") {
		t.Error("expected truncation marker in message")
	}
	// Should NOT contain the full 2000-char string
	if strings.Contains(msg, longOutput) {
		t.Error("message should not contain the full untruncated output")
	}
	// But should contain the first 1500 chars
	if !strings.Contains(msg, longOutput[:1500]) {
		t.Error("message should contain the first 1500 chars of output")
	}
}

func TestBuildReflectUserMessage_NoTruncationForShortOutput(t *testing.T) {
	shortOutput := strings.Repeat("y", 500)
	state := &CognitiveState{Goal: Goal{Raw: "goal"}}
	plan := &TaskPlan{Summary: "plan"}
	obs := &ObservationResult{
		Observations: []Observation{
			{SubTaskID: "t1", ToolName: "bash", Output: shortOutput},
		},
		SuccessCount: 1,
	}

	msg := buildReflectUserMessage(state, plan, obs, 0)
	if strings.Contains(msg, "...[truncated]") {
		t.Error("short output should not be truncated")
	}
	if !strings.Contains(msg, shortOutput) {
		t.Error("message should contain the full short output")
	}
}
