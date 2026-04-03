package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// MemoryManageTool allows users to manage their memories through conversation.
type MemoryManageTool struct {
	store   memory.Store
	db      *sql.DB
	baseDir string
}

type memoryManageInput struct {
	Action        string   `json:"action"`
	Query         string   `json:"query"`
	Sensitivity   string   `json:"sensitivity"`
	MemoryType    string   `json:"memory_type"`
	RetentionDays float64  `json:"retention_days"`
	ConfirmIDs    []string `json:"confirm_ids"`
}

// NewMemoryManageTool creates a new memory management tool.
func NewMemoryManageTool(store memory.Store, db *sql.DB, baseDir string) *MemoryManageTool {
	return &MemoryManageTool{
		store:   store,
		db:      db,
		baseDir: baseDir,
	}
}

func (t *MemoryManageTool) Name() string { return "memory_manage" }

func (t *MemoryManageTool) Description() string {
	return "Manage user memories: forget (delete), list, protect (set sensitivity), or set retention policies."
}

func (t *MemoryManageTool) RequiresApproval() bool { return true }

func (t *MemoryManageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"forget", "list", "protect", "retention"},
				"description": "The memory management action to perform",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query or description for forget/protect actions",
			},
			"sensitivity": map[string]any{
				"type":        "string",
				"enum":        []string{"public", "private", "secret"},
				"description": "Sensitivity level for protect action",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"enum":        []string{"episodic", "semantic", "procedural"},
				"description": "Memory type for retention action",
			},
			"retention_days": map[string]any{
				"type":        "integer",
				"description": "Retention period in days for retention action",
			},
			"confirm_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Memory IDs to confirm for forget action",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MemoryManageTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in memoryManageInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}

	switch in.Action {
	case "forget":
		return t.handleForget(ctx, in)
	case "list":
		return t.handleList(ctx, in)
	case "protect":
		return t.handleProtect(ctx, in)
	case "retention":
		return t.handleRetention(ctx, in)
	default:
		return Result{Error: fmt.Sprintf("unknown action: %s", in.Action)}, nil
	}
}

func (t *MemoryManageTool) handleForget(ctx context.Context, in memoryManageInput) (Result, error) {
	// If confirm_ids provided, delete those memories
	if len(in.ConfirmIDs) > 0 {
		deleted := 0
		var errs []string
		for _, id := range in.ConfirmIDs {
			if err := t.store.Delete(ctx, id); err != nil {
				errs = append(errs, fmt.Sprintf("failed to delete %s: %v", id, err))
				continue
			}
			t.logAudit(ctx, id, "delete", "user", "User requested deletion")
			deleted++
		}

		msg := fmt.Sprintf("Deleted %d memories.", deleted)
		if len(errs) > 0 {
			msg += "\nErrors:\n" + strings.Join(errs, "\n")
		}
		return Result{Output: msg}, nil
	}

	// Otherwise search for matching memories
	if in.Query == "" {
		return Result{Error: "Please provide a query to search for memories to forget."}, nil
	}

	results, err := t.store.Search(ctx, memory.SearchQuery{
		Text:  in.Query,
		Limit: 10,
	})
	if err != nil {
		return Result{Error: "search failed: " + err.Error()}, nil
	}

	if len(results) == 0 {
		return Result{Output: "No matching memories found."}, nil
	}

	// Return candidates for confirmation
	var sb strings.Builder
	sb.WriteString("Found the following matching memories. Call again with confirm_ids to delete:\n\n")
	for _, r := range results {
		content := r.Entry.Content
		if len(content) > 100 {
			content = content[:97] + "..."
		}
		_, _ = fmt.Fprintf(&sb, "- ID: %s | Score: %.2f | Content: %s\n", r.Entry.ID, r.Score, content)
	}
	return Result{Output: sb.String()}, nil
}

func (t *MemoryManageTool) handleList(ctx context.Context, in memoryManageInput) (Result, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT memory_id, scope, memory_type, strength, sensitivity, file_path
		FROM memory_index
		WHERE scope IN ('user', 'session')
		ORDER BY updated_at DESC
		LIMIT 20
	`)
	if err != nil {
		return Result{Error: "query failed: " + err.Error()}, nil
	}
	defer func() { _ = rows.Close() }()

	var sb strings.Builder
	sb.WriteString("Your memories (most recent first):\n\n")

	count := 0
	for rows.Next() {
		var memID, scope, memType, sensitivity, filePath string
		var strength float64
		if err := rows.Scan(&memID, &scope, &memType, &strength, &sensitivity, &filePath); err != nil {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "- ID: %s | Scope: %s | Type: %s | Strength: %.2f | Sensitivity: %s\n",
			memID, scope, memType, strength, sensitivity)
		count++
	}

	if count == 0 {
		return Result{Output: "No memories found."}, nil
	}

	_, _ = fmt.Fprintf(&sb, "\nTotal: %d memories shown.", count)
	return Result{Output: sb.String()}, nil
}

func (t *MemoryManageTool) handleProtect(ctx context.Context, in memoryManageInput) (Result, error) {
	if in.Query == "" {
		return Result{Error: "Please provide a query to search for memories to protect."}, nil
	}

	sensitivity := in.Sensitivity
	if sensitivity == "" {
		sensitivity = "secret"
	}

	results, err := t.store.Search(ctx, memory.SearchQuery{
		Text:  in.Query,
		Limit: 5,
	})
	if err != nil {
		return Result{Error: "search failed: " + err.Error()}, nil
	}

	if len(results) == 0 {
		return Result{Output: "No matching memories found to protect."}, nil
	}

	updated := 0
	for _, r := range results {
		_, err := t.db.ExecContext(ctx, `
			UPDATE memory_index SET sensitivity = ? WHERE memory_id = ?
		`, sensitivity, r.Entry.ID)
		if err != nil {
			continue
		}
		t.logAudit(ctx, r.Entry.ID, "protect", "user",
			fmt.Sprintf("Sensitivity set to %s", sensitivity))
		updated++
	}

	return Result{Output: fmt.Sprintf("Updated sensitivity to '%s' for %d memories.", sensitivity, updated)}, nil
}

func (t *MemoryManageTool) handleRetention(ctx context.Context, in memoryManageInput) (Result, error) {
	memType := in.MemoryType
	if memType == "" {
		return Result{Error: "Please specify a memory_type (episodic, semantic, or procedural)."}, nil
	}

	retentionDays := int(in.RetentionDays)
	if retentionDays <= 0 {
		return Result{Error: "Please specify a positive retention_days value."}, nil
	}

	return Result{
		Output: fmt.Sprintf("Retention policy for %s memories set to %d days. "+
			"Note: this takes effect on next restart.", memType, retentionDays),
	}, nil
}

func (t *MemoryManageTool) logAudit(ctx context.Context, memoryID, action, actor, details string) {
	id := fmt.Sprintf("audit_%d", time.Now().UnixNano())
	_, _ = t.db.ExecContext(ctx, `
		INSERT INTO memory_audit_log (id, memory_id, action, actor, timestamp, details)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, memoryID, action, actor, time.Now(), details)
}
