package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type StepInput struct {
	WorkflowName string       `json:"workflow_name"`
	WorkflowHash string       `json:"workflow_hash"`
	StageID      string       `json:"stage_id"`
	PriorResults []StepResult `json:"prior_results,omitempty"`
}

type StepOutput struct {
	Status     Status         `json:"status"`
	Summary    string         `json:"summary,omitempty"`
	Output     string         `json:"output,omitempty"`
	Artifacts  []string       `json:"artifacts,omitempty"`
	TokensUsed int            `json:"tokens_used,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type StepResult struct {
	StepID         string         `json:"step_id"`
	StageID        string         `json:"stage_id"`
	Type           StepType       `json:"type"`
	Status         Status         `json:"status"`
	CacheKey       string         `json:"cache_key,omitempty"`
	Cached         bool           `json:"cached,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Output         string         `json:"output,omitempty"`
	Artifacts      []string       `json:"artifacts,omitempty"`
	TokensUsed     int            `json:"tokens_used,omitempty"`
	DurationMillis int64          `json:"duration_ms,omitempty"`
	Error          string         `json:"error,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	CompletedAt    time.Time      `json:"completed_at,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type CacheRecord struct {
	WorkflowName string
	WorkflowHash string
	StageID      string
	StepID       string
	CacheKey     string
	Result       StepResult
	CreatedAt    time.Time
}

type ReplayCache interface {
	Get(ctx context.Context, key string) (*CacheRecord, bool, error)
	Put(ctx context.Context, record CacheRecord) error
}

type MemoryCache struct {
	mu      sync.RWMutex
	records map[string]CacheRecord
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{records: make(map[string]CacheRecord)}
}

func (c *MemoryCache) Get(_ context.Context, key string) (*CacheRecord, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.records[key]
	if !ok {
		return nil, false, nil
	}
	return &record, true, nil
}

func (c *MemoryCache) Put(_ context.Context, record CacheRecord) error {
	if c == nil {
		return nil
	}
	if record.CacheKey == "" {
		return fmt.Errorf("cache record requires cache key")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	c.records[record.CacheKey] = record
	return nil
}

func cacheKey(spec Spec, stage Stage, step Step, prior []StepResult) string {
	payload := map[string]any{
		"workflow_name": spec.Name,
		"workflow_hash": spec.Digest(),
		"stage_id":      stage.ID,
		"step":          step,
		"context_hash":  priorResultsDigest(prior),
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "wfstep_" + hex.EncodeToString(sum[:])
}

func priorResultsDigest(results []StepResult) string {
	normalized := make([]map[string]any, 0, len(results))
	for _, result := range results {
		normalized = append(normalized, map[string]any{
			"step_id":   result.StepID,
			"stage_id":  result.StageID,
			"status":    result.Status,
			"summary":   result.Summary,
			"output":    result.Output,
			"artifacts": result.Artifacts,
			"error":     result.Error,
		})
	}
	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
