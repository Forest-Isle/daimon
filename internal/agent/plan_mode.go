package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Forest-Isle/IronClaw/internal/channel"
)

const PlanGenerationPrompt = `You are generating an execution plan for a coding agent before any write tool is used.

You must:
1. Analyze the user's goal.
2. Break the work into discrete, ordered steps.
3. For each step, identify the tool to use and whether it is write-capable.
4. Return JSON only with no markdown fences unless explicitly unavoidable.

Output JSON using exactly this shape:
{
  "goal": "string",
  "steps": [
    {
      "description": "string",
      "tool_name": "string",
      "reason": "string",
      "is_write": true
    }
  ],
  "tools_needed": ["tool_a", "tool_b"]
}`

type PlanMode struct {
	mu            sync.Mutex
	activePlan    *Plan
	planProvider  Provider
	approvalFunc  ApprovalFunc
	autoApprove   bool
	approvedTools map[string]bool
}

type Plan struct {
	ID          string
	Goal        string
	Steps       []PlanStep
	ToolsNeeded []string
	Approved    bool
	CreatedAt   time.Time
}

type PlanStep struct {
	Description string
	ToolName    string
	Reason      string
	IsWrite     bool
}

type planModeResponse struct {
	Goal        string                 `json:"goal"`
	Steps       []planModeResponseStep `json:"steps"`
	ToolsNeeded []string               `json:"tools_needed"`
}

type planModeResponseStep struct {
	Description string `json:"description"`
	ToolName    string `json:"tool_name"`
	Reason      string `json:"reason"`
	IsWrite     bool   `json:"is_write"`
}

var planModeJSONBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
var planModeJSONObjectRe = regexp.MustCompile(`(?s)\{.*\}`)

func NewPlanMode(planProvider Provider, approvalFunc ApprovalFunc, autoApprove bool) *PlanMode {
	return &PlanMode{
		planProvider:  planProvider,
		approvalFunc:  approvalFunc,
		autoApprove:   autoApprove,
		approvedTools: make(map[string]bool),
	}
}

func (pm *PlanMode) GeneratePlan(ctx context.Context, goal, planContext string) (*Plan, error) {
	if pm.planProvider == nil {
		return nil, fmt.Errorf("plan mode: nil plan provider")
	}

	userPrompt := fmt.Sprintf("Goal:\n%s\n\nContext:\n%s\n", strings.TrimSpace(goal), strings.TrimSpace(planContext))
	resp, err := pm.planProvider.Complete(ctx, CompletionRequest{
		System:    PlanGenerationPrompt,
		Messages:  []CompletionMessage{{Role: "user", Content: userPrompt}},
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("plan mode: generate plan: %w", err)
	}

	parsed, err := parsePlanModeResponse(resp.Text)
	if err != nil {
		return nil, fmt.Errorf("plan mode: parse plan: %w", err)
	}

	plan := &Plan{
		ID:          uuid.NewString(),
		Goal:        parsed.Goal,
		ToolsNeeded: dedupeStrings(parsed.ToolsNeeded),
		CreatedAt:   time.Now(),
	}
	if plan.Goal == "" {
		plan.Goal = strings.TrimSpace(goal)
	}
	for _, step := range parsed.Steps {
		plan.Steps = append(plan.Steps, PlanStep{
			Description: step.Description,
			ToolName:    step.ToolName,
			Reason:      step.Reason,
			IsWrite:     step.IsWrite,
		})
		if step.ToolName != "" && !containsString(plan.ToolsNeeded, step.ToolName) {
			plan.ToolsNeeded = append(plan.ToolsNeeded, step.ToolName)
		}
	}

	pm.mu.Lock()
	pm.activePlan = plan
	pm.approvedTools = make(map[string]bool)
	pm.mu.Unlock()

	slog.Info("plan mode: generated plan", "plan_id", plan.ID, "goal", plan.Goal, "steps", len(plan.Steps), "tools", len(plan.ToolsNeeded))
	return plan, nil
}

func (pm *PlanMode) RequestApproval(ctx context.Context, plan *Plan, ch channel.Channel, target channel.MessageTarget) (bool, error) {
	if plan == nil {
		return false, fmt.Errorf("plan mode: nil plan")
	}

	planText := formatPlanForApproval(plan)
	approved := false

	switch {
	case pm.autoApprove:
		approved = true
	case ch != nil:
		if sender, ok := ch.(channel.ApprovalSender); ok {
			var err error
			approved, err = sender.SendApprovalRequest(ctx, target, "plan_mode", planText)
			if err != nil {
				return false, err
			}
		} else if pm.approvalFunc != nil {
			var err error
			approved, err = pm.approvalFunc(ctx, ch, target, "plan_mode", planText)
			if err != nil {
				return false, err
			}
		} else {
			approved = true
		}
	default:
		approved = true
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.activePlan == nil || pm.activePlan.ID != plan.ID {
		return false, fmt.Errorf("plan mode: plan %s is no longer active", plan.ID)
	}

	pm.activePlan.Approved = approved
	pm.approvedTools = make(map[string]bool)
	if approved {
		for _, toolName := range plan.ToolsNeeded {
			if toolName == "" {
				continue
			}
			pm.approvedTools[toolName] = true
		}
	}

	slog.Info("plan mode: approval decided", "plan_id", plan.ID, "approved", approved)
	return approved, nil
}

func (pm *PlanMode) CheckTool(toolName string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.checkToolLocked(toolName)
}

func (pm *PlanMode) InterceptTool(ctx context.Context, toolName string, input []byte) (bool, string, error) {
	_ = ctx

	if !isPlanModeWriteTool(toolName, input) {
		return true, "", nil
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.autoApprove {
		return true, "", nil
	}
	if pm.activePlan == nil {
		return false, "Plan Mode requires a proposed plan before using write tools. Generate a plan and request approval first.", nil
	}
	if !pm.activePlan.Approved {
		return false, "A plan exists but is not approved yet. Request user approval before executing write tools.", nil
	}
	if pm.checkToolLocked(toolName) {
		return true, "", nil
	}
	return false, fmt.Sprintf("Tool %q is not approved under the current plan. Replan and request approval again.", toolName), nil
}

func (pm *PlanMode) CompletePlan(planID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.activePlan == nil {
		return
	}
	if planID != "" && pm.activePlan.ID != planID {
		return
	}

	slog.Info("plan mode: completing plan", "plan_id", pm.activePlan.ID)
	pm.activePlan = nil
	pm.approvedTools = make(map[string]bool)
}

func (pm *PlanMode) checkToolLocked(toolName string) bool {
	if pm.autoApprove {
		return true
	}
	if pm.activePlan == nil || !pm.activePlan.Approved {
		return false
	}
	return pm.approvedTools[toolName]
}

func parsePlanModeResponse(raw string) (*planModeResponse, error) {
	text := strings.TrimSpace(raw)
	var parsed planModeResponse

	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return &parsed, nil
	}
	if m := planModeJSONBlockRe.FindStringSubmatch(text); len(m) == 2 {
		if err := json.Unmarshal([]byte(m[1]), &parsed); err == nil {
			return &parsed, nil
		}
	}
	if m := planModeJSONObjectRe.FindString(text); m != "" {
		if err := json.Unmarshal([]byte(m), &parsed); err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid plan json")
}

func formatPlanForApproval(plan *Plan) string {
	var b strings.Builder
	b.WriteString("Plan proposal\n")
	b.WriteString("Goal: ")
	b.WriteString(plan.Goal)
	b.WriteString("\n")
	for i, step := range plan.Steps {
		writeMode := "read"
		if step.IsWrite {
			writeMode = "write"
		}
		b.WriteString(fmt.Sprintf("%d. [%s] %s", i+1, writeMode, step.Description))
		if step.ToolName != "" {
			b.WriteString(" (tool: ")
			b.WriteString(step.ToolName)
			b.WriteString(")")
		}
		if step.Reason != "" {
			b.WriteString(" - ")
			b.WriteString(step.Reason)
		}
		b.WriteString("\n")
	}
	if len(plan.ToolsNeeded) > 0 {
		b.WriteString("Tools: ")
		b.WriteString(strings.Join(plan.ToolsNeeded, ", "))
	}
	return strings.TrimSpace(b.String())
}

func isPlanModeWriteTool(toolName string, input []byte) bool {
	switch toolName {
	case "file_write", "file_edit", "worktree_create", "worktree_merge":
		return true
	case "bash":
		return bashLikelyWrites(input)
	default:
		return false
	}
}

func bashLikelyWrites(input []byte) bool {
	if len(input) == 0 {
		return false
	}

	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}

	cmd, _ := payload["cmd"].(string)
	if cmd == "" {
		if alt, ok := payload["command"].(string); ok {
			cmd = alt
		}
	}
	cmd = strings.ToLower(cmd)
	if cmd == "" {
		return false
	}

	writeHints := []string{
		">", ">>", "tee ", "touch ", "mkdir ", "rm ", "mv ", "cp ",
		"sed -i", "perl -pi", "truncate ", "install ", "cat <<", "apply_patch",
		"git commit", "git merge", "git cherry-pick", "git rebase", "git worktree add",
	}
	for _, hint := range writeHints {
		if strings.Contains(cmd, hint) {
			return true
		}
	}
	return false
}

func dedupeStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
