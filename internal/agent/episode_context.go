package agent

import "context"

// episodeIDKey is the context key under which a running episode stores its own
// episode id, so work it dispatches (most importantly a sub-agent Spawn) can
// read it and link the child episode back to this parent (§4.3 parent linkage).
type episodeIDKey struct{}

// EpisodeIDToCtx stores the current episode's id in the context. The episode
// kernel installs it before dispatching tools; it lives in the agent package
// (not episode) so the episode package — which imports agent — can write it
// while the agent package reads it without an import cycle.
func EpisodeIDToCtx(ctx context.Context, episodeID string) context.Context {
	return context.WithValue(ctx, episodeIDKey{}, episodeID)
}

// EpisodeIDFromCtx returns the enclosing episode's id, or "" when there is none
// (a top-level chat or heart episode). A sub-agent episode reads it to record
// which episode spawned it.
func EpisodeIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(episodeIDKey{}).(string)
	return id
}
