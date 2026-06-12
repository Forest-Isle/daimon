package episode

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/world"
)

const constitutionSummary = `You are Daimon, a local-first cognitive agent. Stay grounded in the user's durable world model, use tools when they materially improve the result, preserve commitments and decisions as world writes, and end every episode by declaring the structured outcome. Be concise, honest about uncertainty, and ask only when blocked.`

// composeSystem builds the episode system prompt by freshly assembling the
// durable world context (identity, commitments) with the runtime-provided
// context (persona, rules, memories) and the mandatory exit instruction. The
// message list (transcript) is supplied separately by the caller.
func composeSystem(ctx context.Context, req agent.CognitiveRequest, ws *world.Store, id *world.Identity) string {
	var sections []string
	sections = append(sections, "## Personality and Daimon Constitution\n"+constitutionSummary)

	if persona := strings.TrimSpace(req.Persona); persona != "" {
		sections = append(sections, "## Persona\n"+persona)
	}
	if rules := strings.TrimSpace(req.Rules); rules != "" {
		sections = append(sections, "## Rules\n"+rules)
	}

	sections = append(sections, "## Identity Digest\n"+identityDigest(id))
	sections = append(sections, "## Active Commitments\n"+commitmentsDigest(ctx, ws))

	if mem := strings.TrimSpace(req.Memories); mem != "" {
		sections = append(sections, "## Relevant Memories\n"+mem)
	}

	sections = append(sections, fmt.Sprintf(
		"## Goal\n%s\n\nYou MUST call `episode_close` with a complete Outcome before finishing. Until you call it, the system treats your work as incomplete. Persist anything worth remembering via the world_writes field or the world tools.",
		goalText(req.Goal)))

	return strings.Join(sections, "\n\n")
}

func identityDigest(id *world.Identity) string {
	if id != nil {
		if digest := strings.TrimSpace(id.Digest()); digest != "" {
			return digest
		}
	}
	return "Not yet configured."
}

func commitmentsDigest(ctx context.Context, ws *world.Store) string {
	if ws != nil {
		if digest, err := ws.CommitmentsDigest(ctx, ""); err == nil && strings.TrimSpace(digest) != "" {
			return digest
		}
	}
	return "None."
}

func goalText(goal string) string {
	if strings.TrimSpace(goal) == "" {
		return "Respond to the trigger event."
	}
	return strings.TrimSpace(goal)
}
