package agent

import (
	"context"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// EpisodeIDToCtx stores the current episode's id in the context. The episode
// kernel installs it before dispatching tools; the key lives in the tool package
// so the action interceptor can also read it without importing agent.
func EpisodeIDToCtx(ctx context.Context, episodeID string) context.Context {
	return tool.WithEpisodeID(ctx, episodeID)
}

// EpisodeIDFromCtx returns the enclosing episode's id, or "" when there is none
// (a top-level chat or heart episode). A sub-agent episode reads it to record
// which episode spawned it.
func EpisodeIDFromCtx(ctx context.Context) string {
	return tool.EpisodeIDFromContext(ctx)
}
