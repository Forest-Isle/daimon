package memory

import "testing"

func TestProfileSectionRegistry(t *testing.T) {
	reg := NewProfileSectionRegistry()

	sections := reg.All()
	if len(sections) != 6 {
		t.Fatalf("expected 6 sections, got %d", len(sections))
	}

	comm, ok := reg.Get("communication")
	if !ok {
		t.Fatal("communication section not found")
	}
	if comm.Priority != 0 {
		t.Errorf("communication priority: want 0, got %d", comm.Priority)
	}
	if comm.FactThreshold != 3 {
		t.Errorf("communication fact threshold: want 3, got %d", comm.FactThreshold)
	}

	ts, ok := reg.Get("tech_stack")
	if !ok {
		t.Fatal("tech_stack section not found")
	}
	if ts.Priority != 0 {
		t.Errorf("tech_stack priority: want 0, got %d", ts.Priority)
	}

	fb, ok := reg.Get("feedback")
	if !ok {
		t.Fatal("feedback section not found")
	}
	if fb.Priority != 2 {
		t.Errorf("feedback priority: want 2, got %d", fb.Priority)
	}
	if fb.FactThreshold != 8 {
		t.Errorf("feedback fact threshold: want 8, got %d", fb.FactThreshold)
	}
}

func TestProfileSectionRegistry_ByPriority(t *testing.T) {
	reg := NewProfileSectionRegistry()
	sorted := reg.ByPriority()
	if len(sorted) < 2 {
		t.Fatal("expected at least 2 sections")
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Priority < sorted[i-1].Priority {
			t.Errorf("sections not sorted: %s (P%d) before %s (P%d)",
				sorted[i-1].ID, sorted[i-1].Priority, sorted[i].ID, sorted[i].Priority)
		}
	}
}

func TestRouteCategoryToSection(t *testing.T) {
	reg := NewProfileSectionRegistry()

	tests := []struct {
		category string
		want     string
	}{
		{"preference", "communication"},
		{"identity", "identity"},
		{"relationship", "identity"},
		{"task", "projects"},
	}
	for _, tt := range tests {
		got, ok := reg.RouteCategory(tt.category)
		if !ok {
			t.Errorf("RouteCategory(%q) returned not-ok", tt.category)
			continue
		}
		if got != tt.want {
			t.Errorf("RouteCategory(%q) = %q, want %q", tt.category, got, tt.want)
		}
	}

	_, ok := reg.RouteCategory("fact")
	if ok {
		t.Error("RouteCategory(\"fact\") should return not-ok")
	}
}
