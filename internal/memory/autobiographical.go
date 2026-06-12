package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type DecisionRecord struct {
	Decision string
	Reason   string
	Context  string
	Outcome  string
	Tags     []string
}

type AutobiographicalStore struct {
	store Store
	audit *AuditLogger
}

func NewAutobiographicalStore(store Store, audit *AuditLogger) *AutobiographicalStore {
	return &AutobiographicalStore{store: store, audit: audit}
}

func (s *AutobiographicalStore) RecordDecision(ctx context.Context, record DecisionRecord, sessionID, userID string) (string, error) {
	if s == nil || s.store == nil {
		return "", nil
	}
	record.Decision = strings.TrimSpace(record.Decision)
	if record.Decision == "" {
		return "", fmt.Errorf("decision is required")
	}

	now := time.Now()
	id := fmt.Sprintf("decision_%d", now.UnixNano())
	content := formatDecisionRecord(record)
	metadata := map[string]string{
		"type":     string(Autobiographical),
		"category": "decision",
		"source":   "agent_self_observation",
	}
	if record.Reason != "" {
		metadata["reason"] = truncateMetadata(record.Reason, 240)
	}
	if record.Outcome != "" {
		metadata["outcome"] = truncateMetadata(record.Outcome, 240)
	}
	if len(record.Tags) > 0 {
		metadata["tags"] = strings.Join(record.Tags, ",")
	}

	if err := s.store.Save(ctx, Entry{
		ID:        id,
		SessionID: sessionID,
		UserID:    userID,
		Scope:     ScopeUser,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", err
	}
	if s.audit != nil {
		s.audit.Log(ctx, id, "record_decision", "agent", "autobiographical decision memory")
	}
	return id, nil
}

func formatDecisionRecord(record DecisionRecord) string {
	var lines []string
	lines = append(lines, "Decision: "+strings.TrimSpace(record.Decision))
	if strings.TrimSpace(record.Reason) != "" {
		lines = append(lines, "Reason: "+strings.TrimSpace(record.Reason))
	}
	if strings.TrimSpace(record.Context) != "" {
		lines = append(lines, "Context: "+strings.TrimSpace(record.Context))
	}
	if strings.TrimSpace(record.Outcome) != "" {
		lines = append(lines, "Outcome: "+strings.TrimSpace(record.Outcome))
	}
	if len(record.Tags) > 0 {
		lines = append(lines, "Tags: "+strings.Join(record.Tags, ", "))
	}
	return strings.Join(lines, "\n")
}

func truncateMetadata(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
