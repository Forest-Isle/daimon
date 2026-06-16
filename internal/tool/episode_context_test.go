package tool

import (
	"context"
	"testing"
)

func TestEpisodeIDContextRoundTrip(t *testing.T) {
	if got := EpisodeIDFromContext(context.Background()); got != "" {
		t.Fatalf("EpisodeIDFromContext(empty) = %q, want empty", got)
	}
	ctx := WithEpisodeID(context.Background(), "ep1")
	if got := EpisodeIDFromContext(ctx); got != "ep1" {
		t.Fatalf("EpisodeIDFromContext() = %q, want ep1", got)
	}
}
