package memory

import "context"

// RLEventHandler receives notifications about memory lifecycle events.
// Implementations convert these events into RL reward signals.
// All methods are fire-and-forget: they must not block and must not return errors.
// Implementations should handle failures silently (e.g., via structured logging).
type RLEventHandler interface {
	// OnMemoryAdd is called after a new memory is successfully stored.
	OnMemoryAdd(ctx context.Context, factID, content string, importance int)

	// OnMemoryUpdate is called when a memory is replaced with an updated version.
	// oldID is the archived fact ID, newID is the new fact ID, content is the new content.
	OnMemoryUpdate(ctx context.Context, oldID, newID, content string)

	// OnMemoryDelete is called after a memory is archived (invalidated).
	OnMemoryDelete(ctx context.Context, factID string)

	// OnMemoryConflict is called when a new fact conflicts with existing memories.
	// content is the text of the incoming fact (it has no ID yet at conflict-detection time);
	// conflictIDs are the IDs of the existing memories that were flagged as conflicting.
	OnMemoryConflict(ctx context.Context, content string, conflictIDs []string)
}
