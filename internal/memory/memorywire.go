// Package memory provides memorywire-compatible wire format types and HTTP handler.
//
// memorywire is a vendor-neutral wire format for agent memory operations
// (remember, recall, forget, merge, expire) designed to compose with MCP.
// See: https://arxiv.org/html/2606.01138v1
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ── memorywire operation types ──

// MWOps is a batch of memorywire operations.
type MWOps struct {
	Operations []MWOp `json:"operations"`
}

// MWOp is a single memorywire operation.
type MWOp struct {
	Op      string          `json:"op"`                // "remember" | "recall" | "forget" | "merge" | "expire"
	ID      string          `json:"id,omitempty"`      // memory ID (required for forget/merge/expire)
	Scope   string          `json:"scope,omitempty"`   // "user" | "session" | "global"
	UserID  string          `json:"user_id,omitempty"` // owner user
	Content string          `json:"content,omitempty"` // memory content
	Query   string          `json:"query,omitempty"`   // search query (recall)
	Limit   int             `json:"limit,omitempty"`   // max results (recall, default 10)
	Meta    json.RawMessage `json:"metadata,omitempty"` // arbitrary metadata
	Version int             `json:"version,omitempty"` // expected version for merge
	ValidTo string          `json:"valid_to,omitempty"` // RFC3339 timestamp for expire
}

// MWResult is the result of a single memorywire operation.
type MWResult struct {
	Op       string           `json:"op"`
	ID       string           `json:"id,omitempty"`
	Status   string           `json:"status"` // "ok" | "error"
	Error    string           `json:"error,omitempty"`
	Memories []MWMemoryResult `json:"memories,omitempty"` // recall results
}

// MWMemoryResult is a single memory entry returned by recall.
type MWMemoryResult struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score,omitempty"`
}

// MWResponse wraps a batch of operation results.
type MWResponse struct {
	Results []MWResult `json:"results"`
}

// ── HTTP Handler ──

// MemorywireHandler serves memorywire protocol requests against a Store.
type MemorywireHandler struct {
	store Store
}

// NewMemorywireHandler creates a handler backed by the given Store.
func NewMemorywireHandler(store Store) *MemorywireHandler {
	return &MemorywireHandler{store: store}
}

// ServeHTTP handles POST /memorywire requests.
func (h *MemorywireHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var ops MWOps
	if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
		writeMWError(w, http.StatusBadRequest, "parse request: "+err.Error())
		return
	}

	if len(ops.Operations) == 0 {
		writeMWError(w, http.StatusBadRequest, "no operations provided")
		return
	}

	results := make([]MWResult, 0, len(ops.Operations))
	for _, op := range ops.Operations {
		results = append(results, h.executeOp(r.Context(), op))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MWResponse{Results: results}); err != nil {
		slog.Warn("memorywire: failed to encode response", "err", err)
	}
}

func (h *MemorywireHandler) executeOp(ctx context.Context, op MWOp) MWResult {
	switch op.Op {
	case "remember":
		return h.handleRemember(ctx, op)
	case "recall":
		return h.handleRecall(ctx, op)
	case "forget":
		return h.handleForget(ctx, op)
	case "merge":
		return h.handleMerge(ctx, op)
	case "expire":
		return h.handleExpire(ctx, op)
	default:
		return MWResult{Op: op.Op, Status: "error", Error: fmt.Sprintf("unknown operation: %q", op.Op)}
	}
}

func (h *MemorywireHandler) handleRemember(ctx context.Context, op MWOp) MWResult {
	if op.Content == "" {
		return MWResult{Op: "remember", Status: "error", Error: "content is required"}
	}

	scope := parseScope(op.Scope)
	now := time.Now().UTC()

	id := op.ID
	if id == "" {
		id = fmt.Sprintf("mw_%d", now.UnixNano())
	}

	entry := Entry{
		ID:        id,
		Scope:     scope,
		UserID:    op.UserID,
		Content:   op.Content,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  make(map[string]string),
	}
	if len(op.Meta) > 0 {
		_ = json.Unmarshal(op.Meta, &entry.Metadata)
	}

	if err := h.store.Save(ctx, entry); err != nil {
		return MWResult{Op: "remember", ID: id, Status: "error", Error: err.Error()}
	}
	return MWResult{Op: "remember", ID: id, Status: "ok"}
}

func (h *MemorywireHandler) handleRecall(ctx context.Context, op MWOp) MWResult {
	limit := op.Limit
	if limit <= 0 {
		limit = 10
	}

	results, err := h.store.Search(ctx, SearchQuery{
		Text:     op.Query,
		UserID:   op.UserID,
		Limit:    limit,
		Scopes:   scopeList(parseScope(op.Scope)),
	})
	if err != nil {
		return MWResult{Op: "recall", Status: "error", Error: err.Error()}
	}

	memories := make([]MWMemoryResult, 0, len(results))
	for _, r := range results {
		memories = append(memories, MWMemoryResult{
			ID:        r.Entry.ID,
			Scope:     string(r.Entry.Scope),
			Content:   r.Entry.Content,
			CreatedAt: r.Entry.CreatedAt,
			Score:     r.Score,
		})
	}
	return MWResult{Op: "recall", Status: "ok", Memories: memories}
}

func (h *MemorywireHandler) handleForget(ctx context.Context, op MWOp) MWResult {
	if op.ID == "" {
		return MWResult{Op: "forget", Status: "error", Error: "id is required"}
	}

	// Soft-invalidate: preserves the fact for audit trails, excludes it from default search.
	if fs, ok := h.store.(*FileMemoryStore); ok {
		if err := fs.SoftInvalidate(ctx, op.ID); err != nil {
			return MWResult{Op: "forget", ID: op.ID, Status: "error", Error: err.Error()}
		}
		return MWResult{Op: "forget", ID: op.ID, Status: "ok"}
	}

	// Non-temporal stores: hard delete.
	if err := h.store.Delete(ctx, op.ID); err != nil {
		return MWResult{Op: "forget", ID: op.ID, Status: "error", Error: err.Error()}
	}
	return MWResult{Op: "forget", ID: op.ID, Status: "ok"}
}

func (h *MemorywireHandler) handleMerge(ctx context.Context, op MWOp) MWResult {
	if op.ID == "" || op.Content == "" {
		return MWResult{Op: "merge", Status: "error", Error: "id and content are required"}
	}

	if err := h.store.Update(ctx, op.ID, op.Content, op.Version); err != nil {
		return MWResult{Op: "merge", ID: op.ID, Status: "error", Error: err.Error()}
	}
	return MWResult{Op: "merge", ID: op.ID, Status: "ok"}
}

func (h *MemorywireHandler) handleExpire(ctx context.Context, op MWOp) MWResult {
	if op.ID == "" {
		return MWResult{Op: "expire", Status: "error", Error: "id is required"}
	}

	// expiration = soft invalidation with a future valid_to.
	if fs, ok := h.store.(*FileMemoryStore); ok {
		if err := fs.SoftInvalidate(ctx, op.ID); err != nil {
			return MWResult{Op: "expire", ID: op.ID, Status: "error", Error: err.Error()}
		}
		return MWResult{Op: "expire", ID: op.ID, Status: "ok"}
	}
	return MWResult{Op: "expire", ID: op.ID, Status: "error", Error: "store does not support soft invalidation"}
}

// ── helpers ──

func parseScope(s string) MemoryScope {
	switch s {
	case "session":
		return ScopeSession
	case "global":
		return ScopeGlobal
	default:
		return ScopeUser
	}
}

func scopeList(s MemoryScope) []MemoryScope {
	if s == "" || s == ScopeUser {
		return nil // nil = all scopes
	}
	return []MemoryScope{s}
}

func writeMWError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(MWResponse{
		Results: []MWResult{{Status: "error", Error: msg}},
	})
}
