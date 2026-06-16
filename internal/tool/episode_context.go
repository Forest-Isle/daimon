package tool

import "context"

type episodeIDKey struct{}

// WithEpisodeID stores the running episode's id in ctx so the action interceptor
// (which imports tool, not agent) can stamp it onto undo records for episode-level
// rollback. agent.EpisodeIDToCtx delegates here so there is a single key.
func WithEpisodeID(ctx context.Context, episodeID string) context.Context {
	return context.WithValue(ctx, episodeIDKey{}, episodeID)
}

func EpisodeIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(episodeIDKey{}).(string)
	return id
}
