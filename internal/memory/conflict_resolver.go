package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ConflictResolution struct {
	HasConflict    bool     `json:"has_conflict"`
	Action         string   `json:"action"` // update|keep_both|flag_review
	Reason         string   `json:"reason"`
	ConflictingIDs []string `json:"conflicting_ids"`
}

type ConflictResolver struct {
	store     *SQLiteStore
	completer Completer
}

func NewConflictResolver(s *SQLiteStore, c Completer) *ConflictResolver {
	return &ConflictResolver{store: s, completer: c}
}

func (cr *ConflictResolver) CheckConflict(ctx context.Context, newFact Entry) (*ConflictResolution, error) {
	// Find similar facts
	similar, err := cr.store.Search(ctx, SearchQuery{
		Text:   newFact.Content,
		Limit:  3,
		UserID: newFact.UserID,
	})
	if err != nil || len(similar) == 0 {
		return &ConflictResolution{HasConflict: false}, nil
	}

	// Format similar facts
	var sb strings.Builder
	for i, res := range similar {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s (created: %s)\n",
			i+1, res.Entry.ID, res.Entry.Content, res.Entry.CreatedAt.Format("2006-01-02")))
	}

	prompt := fmt.Sprintf(`新信息: %s (时间: %s)

现有记忆:
%s

判断是否冲突，以及如何处理。

JSON格式:
{
  "has_conflict": bool,
  "action": "update|keep_both|flag_review",
  "reason": "...",
  "conflicting_ids": ["id1", "id2"]
}`, newFact.Content, newFact.CreatedAt.Format("2006-01-02"), sb.String())

	resp, err := cr.completer.Complete(ctx, "你是记忆冲突检测器", prompt)
	if err != nil {
		return nil, err
	}

	var resolution ConflictResolution
	if err := json.Unmarshal([]byte(extractJSON(resp)), &resolution); err != nil {
		return nil, err
	}

	return &resolution, nil
}

func (cr *ConflictResolver) Resolve(ctx context.Context, newFact Entry, resolution ConflictResolution) error {
	switch resolution.Action {
	case "update":
		// Archive old facts
		for _, id := range resolution.ConflictingIDs {
			cr.store.db.Exec(`UPDATE memory_facts SET scope = 'archive' WHERE id = ?`, id)
		}
		return cr.store.SaveFact(ctx, newFact)

	case "keep_both":
		// Mark relationship
		if newFact.Metadata == nil {
			newFact.Metadata = make(map[string]string)
		}
		newFact.Metadata["related_to"] = strings.Join(resolution.ConflictingIDs, ",")
		return cr.store.SaveFact(ctx, newFact)

	case "flag_review":
		// Flag for manual review
		if newFact.Metadata == nil {
			newFact.Metadata = make(map[string]string)
		}
		newFact.Metadata["needs_review"] = "true"
		if len(resolution.ConflictingIDs) > 0 {
			newFact.Metadata["conflicts_with"] = resolution.ConflictingIDs[0]
		}
		return cr.store.SaveFact(ctx, newFact)
	}

	return nil
}

func extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}
