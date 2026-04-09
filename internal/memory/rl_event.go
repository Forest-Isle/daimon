package memory

import "context"

// RLEventHandler receives notifications about memory lifecycle events.
// Implementations convert these events into RL reward signals.
// All methods are fire-and-forget — errors are logged internally, not propagated.
type RLEventHandler interface {
	// OnMemoryAdd is called after a new memory is successfully stored.
	OnMemoryAdd(ctx context.Context, factID, content string, importance int)

	// OnMemoryUpdate is called after an existing memory is archived and replaced.
	OnMemoryUpdate(ctx context.Context, oldID, newID, content string)

	// OnMemoryDelete is called after a memory is archived (invalidated).
	OnMemoryDelete(ctx context.Context, factID string)

	// OnMemoryConflict is called when a new fact conflicts with existing memories.
	OnMemoryConflict(ctx context.Context, factID string, conflictIDs []string)
}
