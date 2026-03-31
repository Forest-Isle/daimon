package memory

import (
	"testing"
)

func TestParseFactsAllFields(t *testing.T) {
	input := `[{"content":"User likes Go","category":"preference","type":"procedural","importance":8,"emotion":"positive"}]`
	facts, err := parseFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Content != "User likes Go" {
		t.Errorf("content = %q, want %q", f.Content, "User likes Go")
	}
	if f.Category != "preference" {
		t.Errorf("category = %q, want %q", f.Category, "preference")
	}
	if f.Type != "procedural" {
		t.Errorf("type = %q, want %q", f.Type, "procedural")
	}
	if f.Importance != 8 {
		t.Errorf("importance = %d, want 8", f.Importance)
	}
	if f.Emotion != "positive" {
		t.Errorf("emotion = %q, want %q", f.Emotion, "positive")
	}
}

func TestParseFactsMissingFields(t *testing.T) {
	// Only content and category provided; type, importance, emotion are missing.
	input := `[{"content":"User lives in Tokyo","category":"fact"}]`
	facts, err := parseFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Type != "semantic" {
		t.Errorf("missing type should default to 'semantic', got %q", f.Type)
	}
	if f.Importance != 1 {
		t.Errorf("missing importance (zero value) should default to 1, got %d", f.Importance)
	}
	if f.Emotion != "neutral" {
		t.Errorf("missing emotion should default to 'neutral', got %q", f.Emotion)
	}
}

func TestParseFactsInvalidType(t *testing.T) {
	input := `[{"content":"some fact","category":"fact","type":"unknown","importance":5,"emotion":"positive"}]`
	facts, err := parseFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Type != "semantic" {
		t.Errorf("invalid type should default to 'semantic', got %q", facts[0].Type)
	}
	// Other fields should be preserved.
	if facts[0].Importance != 5 {
		t.Errorf("importance = %d, want 5", facts[0].Importance)
	}
	if facts[0].Emotion != "positive" {
		t.Errorf("emotion = %q, want 'positive'", facts[0].Emotion)
	}
}

func TestParseFactsOutOfRangeImportance(t *testing.T) {
	input := `[
		{"content":"too low","category":"fact","type":"semantic","importance":-5,"emotion":"neutral"},
		{"content":"too high","category":"fact","type":"semantic","importance":42,"emotion":"neutral"},
		{"content":"zero","category":"fact","type":"semantic","importance":0,"emotion":"neutral"}
	]`
	facts, err := parseFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %d", len(facts))
	}
	if facts[0].Importance != 1 {
		t.Errorf("importance < 1 should become 1, got %d", facts[0].Importance)
	}
	if facts[1].Importance != 10 {
		t.Errorf("importance > 10 should become 10, got %d", facts[1].Importance)
	}
	if facts[2].Importance != 1 {
		t.Errorf("importance 0 should become 1, got %d", facts[2].Importance)
	}
}

func TestParseFactsInvalidEmotion(t *testing.T) {
	input := `[{"content":"weird emotion","category":"fact","type":"semantic","importance":5,"emotion":"angry"}]`
	facts, err := parseFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Emotion != "neutral" {
		t.Errorf("invalid emotion should default to 'neutral', got %q", facts[0].Emotion)
	}
}
