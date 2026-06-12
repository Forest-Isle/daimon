package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type readObservation struct {
	Size        int64
	ModTimeUnix int64
	ObservedAt  time.Time
}

// ReadBeforeEditTracker records which file versions a session has observed.
type ReadBeforeEditTracker struct {
	mu       sync.RWMutex
	observed map[string]map[string]readObservation
}

func NewReadBeforeEditTracker() *ReadBeforeEditTracker {
	return &ReadBeforeEditTracker{observed: make(map[string]map[string]readObservation)}
}

func (t *ReadBeforeEditTracker) Mark(sessionID, path string, info os.FileInfo) {
	if t == nil || path == "" || info == nil || info.IsDir() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.observed == nil {
		t.observed = make(map[string]map[string]readObservation)
	}
	if t.observed[sessionID] == nil {
		t.observed[sessionID] = make(map[string]readObservation)
	}
	t.observed[sessionID][CanonicalizePath(path)] = readObservation{
		Size:        info.Size(),
		ModTimeUnix: info.ModTime().UnixNano(),
		ObservedAt:  time.Now(),
	}
}

func (t *ReadBeforeEditTracker) HasFresh(sessionID, path string, info os.FileInfo) (bool, string) {
	if t == nil {
		return true, ""
	}
	if path == "" || info == nil || info.IsDir() {
		return true, ""
	}
	canonical := CanonicalizePath(path)

	t.mu.RLock()
	obs, ok := t.observed[sessionID][canonical]
	t.mu.RUnlock()
	if !ok {
		return false, fmt.Sprintf("read-before-edit required: read %s with file_read before modifying it", canonical)
	}
	if obs.Size != info.Size() || obs.ModTimeUnix != info.ModTime().UnixNano() {
		return false, fmt.Sprintf("read-before-edit stale: %s changed since last file_read; read it again before modifying it", canonical)
	}
	return true, ""
}

// ReadBeforeEditInterceptor prevents edits to existing files that the current
// session has not read, or that changed since it was last read.
type ReadBeforeEditInterceptor struct {
	tracker *ReadBeforeEditTracker
}

func NewReadBeforeEditInterceptor(tracker *ReadBeforeEditTracker) *ReadBeforeEditInterceptor {
	if tracker == nil {
		tracker = NewReadBeforeEditTracker()
	}
	return &ReadBeforeEditInterceptor{tracker: tracker}
}

func (i *ReadBeforeEditInterceptor) Name() string { return "read_before_edit" }

func (i *ReadBeforeEditInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if call == nil {
		return next(ctx, call)
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID = call.SessionID
	}

	switch call.ToolName {
	case "file_read":
		result, err := next(ctx, call)
		if err == nil && result != nil && result.Error == "" {
			if path, statErr := resolveToolInputPath(ctx, call.Input); statErr == nil {
				if info, infoErr := os.Stat(path); infoErr == nil {
					i.tracker.Mark(sessionID, path, info)
				}
			}
		}
		return result, err
	case "file_write", "file_edit", "file_patch":
		path, err := resolveToolInputPath(ctx, call.Input)
		if err != nil {
			return &ToolResult{Error: err.Error()}, nil
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) && call.ToolName == "file_write" {
				result, err := next(ctx, call)
				i.markAfterSuccessfulWrite(sessionID, path, result, err)
				return result, err
			}
			return next(ctx, call)
		}
		if ok, reason := i.tracker.HasFresh(sessionID, path, info); !ok {
			return &ToolResult{Error: reason}, nil
		}
		result, err := next(ctx, call)
		i.markAfterSuccessfulWrite(sessionID, path, result, err)
		return result, err
	default:
		return next(ctx, call)
	}
}

func (i *ReadBeforeEditInterceptor) markAfterSuccessfulWrite(sessionID, path string, result *ToolResult, err error) {
	if err != nil || result == nil || result.Error != "" || path == "" {
		return
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return
	}
	i.tracker.Mark(sessionID, path, info)
}

func resolveToolInputPath(ctx context.Context, input string) (string, error) {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return "", fmt.Errorf("read-before-edit input parse failed: %w", err)
	}
	if payload.Path == "" {
		return "", fmt.Errorf("read-before-edit input missing path")
	}
	return ResolveWorkPath(ctx, payload.Path)
}
