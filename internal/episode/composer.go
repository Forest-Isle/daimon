package episode

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/world"
)

const constitutionSummary = `You are Daimon, a local-first cognitive agent. Stay grounded in the user's durable world model, use tools when they materially improve the result, preserve commitments and decisions as world writes, and end every episode by declaring the structured outcome. Be concise, honest about uncertainty, and ask only when blocked.`

// valueDigester supplies the high-confidence values digest — the durable, sourced
// user values that permit autonomous action — for injection into the system
// prompt. The episode runner wires the values store; a nil digester omits the
// section, so the kernel is behaviorally unchanged when values are not present.
type valueDigester interface {
	Digest() string
}

// composeSystem builds the episode system prompt by freshly assembling the
// durable world context (identity, commitments, values) with the runtime-provided
// context (persona, rules, memories) and the mandatory exit instruction. The
// message list (transcript) is supplied separately by the caller.
func composeSystem(ctx context.Context, req agent.CognitiveRequest, ws *world.Store, id *world.Identity, vd valueDigester) string {
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

	if vd != nil {
		if vals := strings.TrimSpace(vd.Digest()); vals != "" {
			sections = append(sections, "## Values\n"+vals)
		}
	}

	if mem := relevantMemories(ctx, ws, req); mem != "" {
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

// relevantMemories assembles the "Relevant Memories" section from the world
// model — journal entries and commitments retrieved for the episode goal. This
// is the strangler switch from the legacy memory package: world.Retrieve is the
// primary source; req.Memories (legacy injection) is used only as a fallback
// while the memory package is still present. Once the replay harness confirms
// assembly quality, the legacy path and req.Memories can be removed.
func relevantMemories(ctx context.Context, ws *world.Store, req agent.CognitiveRequest) string {
	if ws != nil {
		if hits, err := ws.Retrieve(ctx, world.Query{Text: req.Goal, Limit: 6}); err == nil && len(hits) > 0 {
			var b strings.Builder
			for _, h := range hits {
				label := h.Kind
				if label == "" {
					label = h.Source
				}
				fmt.Fprintf(&b, "- [%s] %s\n", label, strings.TrimSpace(h.Title))
			}
			return strings.TrimRight(b.String(), "\n")
		}
	}
	return strings.TrimSpace(req.Memories)
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
