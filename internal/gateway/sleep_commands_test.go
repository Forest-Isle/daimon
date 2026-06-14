package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/sleep"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

// errSummarizer fails if called; the empty-world digest path must not call it.
type errSummarizer struct{}

func (errSummarizer) Complete(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("summarizer must not be called for an empty world")
}

func TestHandleSleepRunsCycle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sleep_cmd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ws := world.NewStore(db.DB)

	gw := &Gateway{sleep: sleep.NewRunner(sleep.NewDigestJob(ws, errSummarizer{}))}
	resp, err := gw.handleSleep(context.Background(), nil, channel.InboundMessage{Text: "/sleep"})
	if err != nil {
		t.Fatalf("handleSleep: %v", err)
	}
	if !strings.Contains(resp, "Sleep cycle complete") || !strings.Contains(resp, "digest") {
		t.Fatalf("unexpected sleep report:\n%s", resp)
	}
}

func TestHandleSleepNilRunner(t *testing.T) {
	gw := &Gateway{} // no sleep runner
	resp, err := gw.handleSleep(context.Background(), nil, channel.InboundMessage{Text: "/sleep"})
	if err != nil {
		t.Fatalf("handleSleep: %v", err)
	}
	if !strings.Contains(resp, "not available") {
		t.Fatalf("expected unavailable message, got %q", resp)
	}
}
