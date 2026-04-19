package memory

import "testing"

func TestSearchQuery_ExcludeTypes(t *testing.T) {
	q := SearchQuery{
		Text:         "test",
		ExcludeTypes: []string{"profile"},
	}
	if len(q.ExcludeTypes) != 1 || q.ExcludeTypes[0] != "profile" {
		t.Fatalf("ExcludeTypes not set correctly: %v", q.ExcludeTypes)
	}
}
