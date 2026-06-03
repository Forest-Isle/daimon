// Package memorywire implements the Agent Memory Protocol (AMP) wire format
// for vendor-neutral agent memory operations. Based on the May 2026 arXiv draft
// (arxiv.org/abs/2606.01138), it defines five canonical operations:
// remember, recall, forget, merge, expire — plus four memory types:
// semantic, episodic, procedural, emotional.
//
// IronClaw's implementation maps AMP operations onto its existing FileMemoryStore,
// providing a standards-compliant API for external memory tooling while preserving
// the local-first, file-based storage architecture.
package memorywire

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// Op is a canonical AMP operation.
type Op string

const (
	OpRemember Op = "remember"
	OpRecall   Op = "recall"
	OpForget   Op = "forget"
	OpMerge    Op = "merge"
	OpExpire   Op = "expire"
)

// MemType is an AMP memory type.
type MemType string

const (
	TypeSemantic   MemType = "semantic"
	TypeEpisodic   MemType = "episodic"
	TypeProcedural MemType = "procedural"
	TypeEmotional  MemType = "emotional"
)

// WireRequest is the standard AMP request envelope.
type WireRequest struct {
	Operation Op          `json:"operation"`
	Memory    WireMemory  `json:"memory"`
	Query     *WireQuery  `json:"query,omitempty"`  // for recall
	TargetIDs []string    `json:"target_ids,omitempty"` // for forget/merge/expire
	Metadata  WireMetadata `json:"metadata,omitempty"`
}

// WireMemory is a single memory entry in AMP format.
type WireMemory struct {
	ID        string            `json:"id,omitempty"`
	Type      MemType           `json:"type"`
	Content   string            `json:"content"`
	Strength  float64           `json:"strength,omitempty"` // 0.0-1.0
	Tags      []string          `json:"tags,omitempty"`
	Timestamp time.Time         `json:"timestamp,omitempty"`
	TTL       time.Duration     `json:"ttl,omitempty"` // 0 = no expiry
	Meta      map[string]string `json:"meta,omitempty"`
}

// WireQuery is an AMP recall query.
type WireQuery struct {
	Text      string   `json:"text,omitempty"`
	Types     []MemType `json:"types,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	MinStrength float64 `json:"min_strength,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// WireMetadata carries request-level metadata.
type WireMetadata struct {
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// WireResponse is the standard AMP response envelope.
type WireResponse struct {
	Status   string       `json:"status"` // "ok" or "error"
	Memories []WireMemory `json:"memories,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// Adapter bridges AMP wire format to IronClaw's FileMemoryStore.
type Adapter struct {
	store    memory.Store
	embedder memory.EmbeddingProvider
}

// NewAdapter creates a new Memorywire adapter.
func NewAdapter(store memory.Store, embedder memory.EmbeddingProvider) *Adapter {
	return &Adapter{store: store, embedder: embedder}
}

// Handle processes an AMP wire request and returns a wire response.
func (a *Adapter) Handle(ctx context.Context, req WireRequest) WireResponse {
	switch req.Operation {
	case OpRemember:
		return a.doRemember(ctx, req)
	case OpRecall:
		return a.doRecall(ctx, req)
	case OpForget:
		return a.doForget(ctx, req)
	case OpMerge:
		return a.doMerge(ctx, req)
	case OpExpire:
		return a.doExpire(ctx, req)
	default:
		return WireResponse{Status: "error", Error: fmt.Sprintf("unknown operation: %s", req.Operation)}
	}
}

func (a *Adapter) doRemember(ctx context.Context, req WireRequest) WireResponse {
	now := time.Now()
	entry := memory.Entry{
		ID:        req.Memory.ID,
		Scope:     a.mapTypeToScope(req.Memory.Type),
		Content:   req.Memory.Content,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  req.Memory.Meta,
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("amp_%d", now.UnixNano())
	}
	if req.Memory.TTL > 0 {
		exp := now.Add(req.Memory.TTL)
		entry.ExpiresAt = &exp
	}
	if req.Metadata.UserID != "" {
		entry.UserID = req.Metadata.UserID
	}
	if req.Metadata.SessionID != "" {
		entry.SessionID = req.Metadata.SessionID
	}
	if err := a.store.Save(ctx, entry); err != nil {
		return WireResponse{Status: "error", Error: err.Error()}
	}
	req.Memory.ID = entry.ID
	req.Memory.Timestamp = now
	return WireResponse{Status: "ok", Memories: []WireMemory{req.Memory}}
}

func (a *Adapter) doRecall(ctx context.Context, req WireRequest) WireResponse {
	if req.Query == nil {
		return WireResponse{Status: "error", Error: "recall requires a query"}
	}
	limit := req.Query.Limit
	if limit <= 0 {
		limit = 5
	}
	query := memory.SearchQuery{
		Text:      req.Query.Text,
		Limit:     limit,
		UserID:    req.Metadata.UserID,
		SessionID: req.Metadata.SessionID,
		Scopes:    a.mapTypesToScopes(req.Query.Types),
	}
	if a.embedder != nil && req.Query.Text != "" {
		emb, err := a.embedder.Embed(ctx, req.Query.Text)
		if err == nil {
			query.Embedding = emb
		}
	}
	results, err := a.store.Search(ctx, query)
	if err != nil {
		return WireResponse{Status: "error", Error: err.Error()}
	}
	memories := make([]WireMemory, 0, len(results))
	for _, r := range results {
		memories = append(memories, WireMemory{
			ID:        r.Entry.ID,
			Content:   r.Entry.Content,
			Strength:  r.Score,
			Timestamp: r.Entry.CreatedAt,
			Meta:      r.Entry.Metadata,
		})
	}
	return WireResponse{Status: "ok", Memories: memories}
}

func (a *Adapter) doForget(ctx context.Context, req WireRequest) WireResponse {
	if len(req.TargetIDs) == 0 && req.Memory.ID == "" {
		return WireResponse{Status: "error", Error: "forget requires target_ids or memory.id"}
	}
	ids := req.TargetIDs
	if len(ids) == 0 {
		ids = []string{req.Memory.ID}
	}
	var lastErr error
	for _, id := range ids {
		if err := a.store.Delete(ctx, id); err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return WireResponse{Status: "error", Error: lastErr.Error()}
	}
	return WireResponse{Status: "ok"}
}

func (a *Adapter) doMerge(ctx context.Context, req WireRequest) WireResponse {
	if len(req.TargetIDs) < 2 {
		return WireResponse{Status: "error", Error: "merge requires at least 2 target_ids"}
	}
	// Merge: recall all targets, combine content, save as merged entry, forget originals
	mergedContent := req.Memory.Content
	if mergedContent == "" {
		// Auto-merge by concatenating content from all targets
		for _, id := range req.TargetIDs {
			results, err := a.store.Search(ctx, memory.SearchQuery{
				Text:  id,
				Limit: 1,
			})
			if err == nil && len(results) > 0 {
				if mergedContent != "" {
					mergedContent += "\n---\n"
				}
				mergedContent += results[0].Entry.Content
			}
		}
	}
	if mergedContent == "" {
		return WireResponse{Status: "error", Error: "no content to merge"}
	}
	now := time.Now()
	mergedID := fmt.Sprintf("amp_merged_%d", now.UnixNano())
	entry := memory.Entry{
		ID:        mergedID,
		Scope:     memory.ScopeUser,
		Content:   mergedContent,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]string{"merged_from": fmt.Sprintf("%v", req.TargetIDs)},
	}
	if err := a.store.Save(ctx, entry); err != nil {
		return WireResponse{Status: "error", Error: err.Error()}
	}
	// Forget originals
	for _, id := range req.TargetIDs {
		_ = a.store.Delete(ctx, id)
	}
	return WireResponse{Status: "ok", Memories: []WireMemory{{
		ID:        mergedID,
		Content:   mergedContent,
		Timestamp: now,
	}}}
}

func (a *Adapter) doExpire(ctx context.Context, req WireRequest) WireResponse {
	if len(req.TargetIDs) == 0 {
		return WireResponse{Status: "error", Error: "expire requires target_ids"}
	}
	// Expire: set TTL to now (immediate expiry via forget)
	for _, id := range req.TargetIDs {
		_ = a.store.Delete(ctx, id)
	}
	return WireResponse{Status: "ok"}
}

func (a *Adapter) mapTypeToScope(t MemType) memory.MemoryScope {
	switch t {
	case TypeEpisodic:
		return memory.ScopeSession
	default:
		return memory.ScopeUser
	}
}

func (a *Adapter) mapTypesToScopes(types []MemType) []memory.MemoryScope {
	if len(types) == 0 {
		return []memory.MemoryScope{memory.ScopeSession, memory.ScopeUser}
	}
	scopes := make([]memory.MemoryScope, 0, 2)
	for _, t := range types {
		scopes = append(scopes, a.mapTypeToScope(t))
	}
	return scopes
}

// MarshalRequest serializes a WireRequest to JSON bytes.
func MarshalRequest(req WireRequest) ([]byte, error) {
	return json.Marshal(req)
}

// UnmarshalRequest deserializes JSON bytes to a WireRequest.
func UnmarshalRequest(data []byte) (*WireRequest, error) {
	var req WireRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// MarshalResponse serializes a WireResponse to JSON bytes.
func MarshalResponse(resp WireResponse) ([]byte, error) {
	return json.Marshal(resp)
}
