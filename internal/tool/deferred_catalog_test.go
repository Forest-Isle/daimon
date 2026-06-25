package tool

import (
	"context"
	"slices"
	"testing"
)

func TestDeferredCatalogRemoveByPrefix(t *testing.T) {
	catalog := NewDeferredCatalog()
	for _, name := range []string{"mcp_x_a", "mcp_x_b", "other"} {
		name := name
		if err := catalog.Add(DeferredToolSpec{
			Name:        name,
			Description: "test tool",
			Source:      "test",
			Load: func(context.Context) (Tool, error) {
				return &deferredTestTool{name: name}, nil
			},
		}); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
	}

	if _, err := catalog.Resolve(context.Background(), "mcp_x_a"); err != nil {
		t.Fatalf("Resolve(mcp_x_a) error = %v", err)
	}
	if _, err := catalog.Resolve(context.Background(), "mcp_x_b"); err != nil {
		t.Fatalf("Resolve(mcp_x_b) error = %v", err)
	}

	removed := catalog.RemoveByPrefix("mcp_x_")
	slices.Sort(removed)
	want := []string{"mcp_x_a", "mcp_x_b"}
	if !slices.Equal(removed, want) {
		t.Fatalf("removed = %#v, want %#v", removed, want)
	}

	if matches := catalog.Search("mcp_x", 10); len(matches) != 0 {
		t.Fatalf("removed tools still searchable: %+v", matches)
	}
	if matches := catalog.Search("other", 10); len(matches) != 1 || matches[0].Name != "other" {
		t.Fatalf("unrelated tool not preserved: %+v", matches)
	}
	if _, ok := catalog.resolved["mcp_x_a"]; ok {
		t.Fatal("resolved mcp_x_a was not cleared")
	}
	if _, ok := catalog.resolved["mcp_x_b"]; ok {
		t.Fatal("resolved mcp_x_b was not cleared")
	}
}
