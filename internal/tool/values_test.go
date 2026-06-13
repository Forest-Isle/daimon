package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/values"
)

func TestValuesToolRecordAndList(t *testing.T) {
	store := values.NewStore(t.TempDir())
	tl := NewValuesTool(store)
	ctx := context.Background()

	res, err := tl.Execute(ctx, []byte(`{"action":"record","domain":"travel","statement":"no red-eye flights","confidence":0.9,"episode":"ep-7","quote":"I hate red-eyes"}`))
	if err != nil {
		t.Fatalf("Execute(record): %v", err)
	}
	if res.Error != "" || !strings.Contains(res.Output, "value recorded") {
		t.Fatalf("record result = %+v", res)
	}

	if _, ok := store.Lookup("travel"); !ok {
		t.Fatal("recorded value not in store")
	}

	list, err := tl.Execute(ctx, []byte(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute(list): %v", err)
	}
	if !strings.Contains(list.Output, "travel") || !strings.Contains(list.Output, "red-eye") {
		t.Fatalf("list result = %+v", list)
	}
}

func TestValuesToolRecordRequiresDomain(t *testing.T) {
	tl := NewValuesTool(values.NewStore(t.TempDir()))
	res, err := tl.Execute(context.Background(), []byte(`{"action":"record","statement":"no domain"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for missing domain")
	}
}

func TestValuesToolUnknownAction(t *testing.T) {
	tl := NewValuesTool(values.NewStore(t.TempDir()))
	res, _ := tl.Execute(context.Background(), []byte(`{"action":"frobnicate"}`))
	if res.Error == "" {
		t.Fatal("expected error for unknown action")
	}
}
