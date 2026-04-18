package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// Planner implements the PLAN phase: single LLM call → structured TaskPlan.
type Planner struct {
	provider Provider
	tools    *tool.Registry
	cfg      config.CognitiveConfig
	llmModel string
	rlPolicy RLPolicy // optional RL policy
}

// NewPlanner creates a new Planner.
func NewPlanner(provider Provider, tools *tool.Registry, cfg config.CognitiveConfig, llmModel string) *Planner {
	model := cfg.PlanModel
	if model == "" {
		model = llmModel
	}
	return &Planner{
		provider: provider,
		tools:    tools,
		cfg:      cfg,
		llmModel: model,
	}
}

// SetRLPolicy injects an optional RL policy.
func (p *Planner) SetRLPolicy(policy RLPolicy) {
	p.rlPolicy = policy
}

// Run executes the PLAN phase. Makes one LLM call (no Tools parameter — planning only).
func (p *Planner) Run(ctx context.Context, state *CognitiveState) (*TaskPlan, error) {
	userMsg := buildPlanUserMessage(state, p.tools)
	maxTokens := p.cfg.PlanMaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	// Build system prompt, appending persistent rules if available
	system := PlanSystemPrompt
	if state.PersistentRules != "" {
		system += "\n\nADDITIONAL RULES (must follow):\n" + state.PersistentRules
	}

	// Allow evolution ModelRouter to override model per-request.
	model := p.llmModel
	if state.ModelOverride != "" {
		model = state.ModelOverride
	}
	if state.MaxTokensOverride > 0 {
		maxTokens = state.MaxTokensOverride
	}

	req := CompletionRequest{
		Model:     model,
		System:    system,
		Messages:  []CompletionMessage{{Role: "user", Content: userMsg}},
		Tools:     nil, // PLAN phase must not execute tools
		MaxTokens: maxTokens,
	}

	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("plan llm call: %w", err)
	}

	plan, err := parsePlanResponse(resp.Text)
	if err != nil {
		slog.Warn("plan: parse failed, falling back to direct reply", "err", err, "text_len", len(resp.Text))
		return &TaskPlan{
			Summary:           "Direct reply (plan parse failed)",
			DirectReply:       resp.Text,
			OverallConfidence: 0.5,
		}, nil
	}

	// Validate tool names exist
	for _, st := range plan.SubTasks {
		if st.ToolName != "" {
			if _, err := p.tools.Get(st.ToolName); err != nil {
				slog.Warn("plan: unknown tool in subtask, clearing", "task", st.ID, "tool", st.ToolName)
				st.ToolName = ""
				st.ToolInput = ""
			}
		}
	}

	// Validate DAG — detect cycles
	if err := validateDAG(plan.SubTasks); err != nil {
		slog.Warn("plan: DAG validation failed, using direct reply", "err", err)
		return &TaskPlan{
			Summary:           "Direct reply (plan had cyclic dependencies)",
			DirectReply:       "I was unable to build a valid execution plan. " + state.UserMessage,
			OverallConfidence: 0.3,
		}, nil
	}

	slog.Info("plan complete",
		"summary", plan.Summary,
		"subtasks", len(plan.SubTasks),
		"confidence", plan.OverallConfidence,
		"direct_reply", plan.DirectReply != "",
	)

	return plan, nil
}

// buildPlanUserMessage fills in the PlanUserPromptTemplate.
func buildPlanUserMessage(state *CognitiveState, tools *tool.Registry) string {
	// Tools section
	var toolsSB strings.Builder
	for _, t := range tools.All() {
		schemaBytes, _ := json.Marshal(t.InputSchema())
		_, _ = fmt.Fprintf(&toolsSB, "- %s: %s\n  Schema: %s\n",
			t.Name(), t.Description(), string(schemaBytes))
	}

	// Memories section
	var memSB strings.Builder
	if len(state.RelevantMemories) == 0 {
		memSB.WriteString("(none)")
	} else {
		for _, m := range state.RelevantMemories {
			memSB.WriteString("- ")
			memSB.WriteString(m.Entry.Content)
			memSB.WriteString("\n")
		}
	}

	// History section (last 10 messages)
	var histSB strings.Builder
	history := state.RecentHistory
	if len(history) > 10 {
		history = history[len(history)-10:]
	}
	if len(history) == 0 {
		histSB.WriteString("(none)")
	} else {
		for _, msg := range history {
			role := msg.Role
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			_, _ = fmt.Fprintf(&histSB, "[%s]: %s\n", role, content)
		}
	}

	msg := PlanUserPromptTemplate
	msg = strings.ReplaceAll(msg, "{{USER_REQUEST}}", state.UserMessage)
	msg = strings.ReplaceAll(msg, "{{TOOLS}}", toolsSB.String())
	msg = strings.ReplaceAll(msg, "{{MEMORIES}}", memSB.String())
	msg = strings.ReplaceAll(msg, "{{HISTORY}}", histSB.String())

	// Knowledge section
	var knowledgeSB strings.Builder
	if len(state.KnowledgeContext) == 0 {
		knowledgeSB.WriteString("(none)")
	} else {
		for i, k := range state.KnowledgeContext {
			_, _ = fmt.Fprintf(&knowledgeSB, "[%d] %s\n\n", i+1, k)
		}
	}
	msg = strings.ReplaceAll(msg, "{{KNOWLEDGE}}", knowledgeSB.String())

	// Graph section
	var graphSB strings.Builder
	if len(state.GraphContext) == 0 {
		graphSB.WriteString("(none)")
	} else {
		for _, rel := range state.GraphContext {
			graphSB.WriteString("- " + rel + "\n")
		}
	}
	msg = strings.ReplaceAll(msg, "{{GRAPH}}", graphSB.String())

	// Project context section
	projectCtx := "(none)"
	if state.ProjectCtx != nil && state.ProjectCtx.RawContent != "" {
		projectCtx = state.ProjectCtx.RawContent
	}
	msg = strings.ReplaceAll(msg, "{{PROJECT_CONTEXT}}", projectCtx)

	// Preferences section (from evolution PreferenceLearner)
	msg = strings.ReplaceAll(msg, "{{PREFERENCES}}", state.Preferences)

	// Strategy hints section (from evolution StrategyOptimizer)
	msg = strings.ReplaceAll(msg, "{{STRATEGY}}", state.StrategyHints)

	// Append available skills if any
	if state.Skills != "" {
		msg += "\n\n" + state.Skills
	}

	// Append available agents if any
	if state.Agents != "" {
		msg += "\n\n" + state.Agents
	}

	return msg
}

var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
var jsonObjectRe = regexp.MustCompile(`(?s)\{.*\}`)

// parsePlanResponse tries three fallbacks to extract JSON from LLM output.
func parsePlanResponse(text string) (*TaskPlan, error) {
	raw := strings.TrimSpace(text)

	var pj planJSON

	// Attempt 1: direct parse
	if err := json.Unmarshal([]byte(raw), &pj); err == nil {
		return planJSONToTaskPlan(pj), nil
	}

	// Attempt 2: extract ```json ... ``` block
	if m := jsonBlockRe.FindStringSubmatch(raw); len(m) == 2 {
		if err := json.Unmarshal([]byte(m[1]), &pj); err == nil {
			return planJSONToTaskPlan(pj), nil
		}
	}

	// Attempt 3: extract first {...} block
	if m := jsonObjectRe.FindString(raw); m != "" {
		if err := json.Unmarshal([]byte(m), &pj); err == nil {
			return planJSONToTaskPlan(pj), nil
		}
	}

	return nil, fmt.Errorf("no valid JSON found in plan response")
}

func planJSONToTaskPlan(pj planJSON) *TaskPlan {
	plan := &TaskPlan{
		Summary:           pj.Summary,
		OverallConfidence: pj.OverallConfidence,
		DirectReply:       pj.DirectReply,
	}
	for i := range pj.SubTasks {
		stj := pj.SubTasks[i]
		plan.SubTasks = append(plan.SubTasks, &SubTask{
			ID:          stj.ID,
			Description: stj.Description,
			ToolName:    stj.ToolName,
			ToolInput:   stj.ToolInput,
			DependsOn:   stj.DependsOn,
			Confidence:  stj.Confidence,
			Status:      SubTaskPending,
		})
	}
	return plan
}

// validateDAG checks for cycles using topological sort (Kahn's algorithm).
func validateDAG(tasks []*SubTask) error {
	index := make(map[string]int, len(tasks))
	for i, t := range tasks {
		index[t.ID] = i
	}

	inDegree := make([]int, len(tasks))
	adj := make([][]int, len(tasks))

	for i, t := range tasks {
		for _, dep := range t.DependsOn {
			j, ok := index[dep]
			if !ok {
				return fmt.Errorf("subtask %s depends on unknown id %s", t.ID, dep)
			}
			adj[j] = append(adj[j], i)
			inDegree[i]++
		}
	}

	queue := make([]int, 0, len(tasks))
	for i, d := range inDegree {
		if d == 0 {
			queue = append(queue, i)
		}
	}

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited != len(tasks) {
		return fmt.Errorf("cyclic dependency detected in task plan")
	}
	return nil
}
