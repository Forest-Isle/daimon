package episode

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/world"
)

const constitutionSummary = `You are Daimon, a local-first cognitive agent. Stay grounded in the user's durable world model, use tools when they materially improve the result, preserve commitments and decisions as world writes, and end every episode by declaring the structured outcome. Be concise, honest about uncertainty, and ask only when blocked.`

func composePrompt(ctx context.Context, ep State, ws *world.Store, id *world.Identity) (system string, messages []agent.CompletionMessage) {
	return composePromptWithTools(ctx, ep, ws, id, nil)
}

func composePromptWithTools(ctx context.Context, ep State, ws *world.Store, id *world.Identity, tools *tool.Registry) (system string, messages []agent.CompletionMessage) {
	identityDigest := "Not yet configured."
	if id != nil {
		if digest := strings.TrimSpace(id.Digest()); digest != "" {
			identityDigest = digest
		}
	}

	commitments := "None."
	if ws != nil {
		if digest, err := ws.CommitmentsDigest(ctx, ""); err == nil && strings.TrimSpace(digest) != "" {
			commitments = digest
		}
	}

	var sections []string
	sections = append(sections, "## Personality and Daimon Constitution\n"+constitutionSummary)
	sections = append(sections, "## Identity Digest\n"+identityDigest)
	sections = append(sections, "## Active Commitments\n"+commitments)
	sections = append(sections, "## Available Tools\n"+toolsDigest(tools))
	sections = append(sections, "## Mandatory Episode Close Tool\n"+episodeCloseToolDigest())
	sections = append(sections, fmt.Sprintf("## Goal Instruction\nYour goal: %s. You MUST call `episode_close` with a complete Outcome before finishing. Until you call it, the system will treat your work as incomplete.", goalText(ep)))

	content := triggerPayload(ep)
	if ep.Goal != "" {
		content = "## Goal\n" + ep.Goal + "\n\n" + content
	}
	return strings.Join(sections, "\n\n"), []agent.CompletionMessage{{Role: "user", Content: content}}
}

func goalText(ep State) string {
	if strings.TrimSpace(ep.Goal) == "" {
		return "respond to the trigger event"
	}
	return strings.TrimSpace(ep.Goal)
}

func triggerPayload(ep State) string {
	trigger := strings.TrimSpace(ep.Trigger)
	if trigger == "" {
		return "Trigger event: unspecified."
	}
	return trigger
}

func toolsDigest(registry *tool.Registry) string {
	if registry == nil {
		return "No registered tools."
	}
	tools := registry.All()
	if len(tools) == 0 {
		return "No registered tools."
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name() < tools[j].Name() })
	lines := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Name() == episodeCloseToolName {
			continue
		}
		lines = append(lines, "- "+t.Name()+": "+strings.TrimSpace(t.Description()))
	}
	if len(lines) == 0 {
		return "No registered tools."
	}
	return strings.Join(lines, "\n")
}

func episodeCloseToolDigest() string {
	return "- episode_close: mandatory structured exit with status, summary, world_writes, receipts, follow_ups, and open_question."
}
