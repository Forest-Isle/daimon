package agent

import (
	"context"
	"sort"
	"strings"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/hook"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/session"
)

// PromptLayerScope describes how long a prompt layer should remain valid.
type PromptLayerScope string

const (
	PromptScopeStatic    PromptLayerScope = "static"
	PromptScopeSession   PromptLayerScope = "session"
	PromptScopeTurn      PromptLayerScope = "turn"
	PromptScopeIteration PromptLayerScope = "iteration"
	PromptScopeEphemeral PromptLayerScope = "ephemeral"
)

// PromptLayer is a named prompt fragment with explicit ordering and lifecycle.
type PromptLayer struct {
	Key      string
	Scope    PromptLayerScope
	Priority int
	Content  string
}

const (
	promptPriorityPersonality = 10
	promptPrioritySystem      = 20
	promptPriorityRules       = 30
	promptPriorityBoundary    = 40
	promptPriorityMemory      = 60
	promptPrioritySkills      = 70
	promptPriorityAgents      = 80
	promptPriorityHooks       = 120
)

// PromptFrame is the per-turn prompt assembly unit. Static/session/turn layers
// are prepared once and rendered before each model call.
type PromptFrame struct {
	UserText string
	Layers   []PromptLayer
}

func (a *Agent) preparePromptFrame(ctx context.Context, sess *session.Session, msg channel.InboundMessage) *PromptFrame {
	frame := a.buildPromptFrame(ctx, msg.Text)

	if a.deps.Security.HookMgr != nil && a.deps.Security.HookMgr.HasOnUserMessageHandlers() {
		msgResult, _ := a.deps.Security.HookMgr.FireOnUserMessage(ctx, hook.OnUserMessageEvent{
			Channel: msg.Channel, ChannelID: msg.ChannelID, UserID: msg.UserID, Text: msg.Text,
		})
		if len(msgResult.InjectedContext) > 0 {
			frame.AddLayer(PromptLayer{
				Key:      "turn.environment_context",
				Scope:    PromptScopeTurn,
				Priority: promptPriorityHooks,
				Content:  "## Environment Context\n" + strings.Join(msgResult.InjectedContext, "\n"),
			})
		}
	}

	return frame
}

func (a *Agent) buildPromptFrame(ctx context.Context, userText string) *PromptFrame {
	frame := &PromptFrame{UserText: userText}

	if a.deps.Core.Cfg.Personality != "" {
		frame.AddLayer(PromptLayer{
			Key:      "static.personality",
			Scope:    PromptScopeStatic,
			Priority: promptPriorityPersonality,
			Content:  "## Personality\n" + a.deps.Core.Cfg.Personality,
		})
	}
	frame.AddLayer(PromptLayer{
		Key:      "static.system",
		Scope:    PromptScopeStatic,
		Priority: promptPrioritySystem,
		Content:  a.deps.Core.Cfg.SystemPrompt,
	})

	if a.deps.Core.Cfg.PersistentRules != "" {
		frame.AddLayer(PromptLayer{
			Key:      "static.rules",
			Scope:    PromptScopeStatic,
			Priority: promptPriorityRules,
			Content:  "## Rules\n" + a.deps.Core.Cfg.PersistentRules,
		})
	}

	frame.AddLayer(PromptLayer{
		Key:      "static.dynamic_boundary",
		Scope:    PromptScopeStatic,
		Priority: promptPriorityBoundary,
		Content:  dynamicContextMarker,
	})

	if section := a.buildMemoryPromptSection(ctx, userText); section != "" {
		frame.AddLayer(PromptLayer{
			Key:      "session.memories",
			Scope:    PromptScopeSession,
			Priority: promptPriorityMemory,
			Content:  section,
		})
	}

	if a.deps.MultiAgent.SkillMgr != nil {
		if section := a.deps.MultiAgent.SkillMgr.BuildPromptSection(userText); section != "" {
			frame.AddLayer(PromptLayer{
				Key:      "session.skills",
				Scope:    PromptScopeSession,
				Priority: promptPrioritySkills,
				Content:  section,
			})
		}
	}

	if a.deps.MultiAgent.AgentMgr != nil {
		if section := a.deps.MultiAgent.AgentMgr.BuildPromptSection(); section != "" {
			frame.AddLayer(PromptLayer{
				Key:      "session.agents",
				Scope:    PromptScopeSession,
				Priority: promptPriorityAgents,
				Content:  section,
			})
		}
	}

	return frame
}

func (a *Agent) buildMemoryPromptSection(ctx context.Context, userText string) string {
	if a.deps.Memory.Cortex != nil {
		results, err := a.deps.Memory.Cortex.Search(ctx, userText, memory.SearchOptions{Limit: 5})
		if err == nil && len(results) > 0 {
			if sections := a.deps.Memory.Cortex.BuildPromptSection(results); sections != nil {
				return strings.TrimSpace(sections.Combined)
			}
		}
		return ""
	}

	if a.deps.Memory.Store == nil {
		return ""
	}
	results, err := a.deps.Memory.Store.Search(ctx, memory.SearchQuery{
		Text:         userText,
		Limit:        5,
		ExcludeTypes: []string{"profile"},
	})
	if err != nil || len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## Relevant Memories\n")
	for _, res := range results {
		sb.WriteString("- ")
		sb.WriteString(res.Entry.Content)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func (a *Agent) renderPromptFrame(ctx context.Context, frame *PromptFrame, sess *session.Session) string {
	return a.renderPromptFrameForIteration(ctx, frame, sess, -1)
}

func (a *Agent) renderPromptFrameForIteration(ctx context.Context, frame *PromptFrame, sess *session.Session, iteration int) string {
	if frame == nil {
		frame = a.buildPromptFrame(ctx, "")
	}

	layers := frame.renderLayers(sess)
	rendered := renderPromptLayers(layers)
	a.publishPromptFrameRendered(sess, iteration, layers, rendered)
	return rendered
}

func (f *PromptFrame) renderLayers(sess *session.Session) []PromptLayer {
	layers := append([]PromptLayer(nil), f.Layers...)
	return layers
}

func (a *Agent) buildPromptBase(ctx context.Context, userText string) string {
	return renderPromptLayers(a.buildPromptFrame(ctx, userText).Layers)
}

func (f *PromptFrame) AddLayer(layer PromptLayer) {
	layer.Content = strings.TrimSpace(layer.Content)
	if layer.Content == "" {
		return
	}
	f.Layers = append(f.Layers, layer)
}

func renderPromptLayers(layers []PromptLayer) string {
	if len(layers) == 0 {
		return ""
	}
	ordered := orderedPromptLayers(layers)

	parts := make([]string, 0, len(ordered))
	for _, layer := range ordered {
		content := strings.TrimSpace(layer.Content)
		if content == "" {
			continue
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n")
}

func (a *Agent) publishPromptFrameRendered(sess *session.Session, iteration int, layers []PromptLayer, rendered string) {
	if a.eventBus == nil {
		return
	}
	scopeCounts := make(map[PromptLayerScope]int)
	ordered := orderedPromptLayers(layers)
	layerKeys := make([]string, 0, len(ordered))
	for _, layer := range ordered {
		if strings.TrimSpace(layer.Content) == "" {
			continue
		}
		scopeCounts[layer.Scope]++
		layerKeys = append(layerKeys, layer.Key)
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	a.eventBus.Publish(PromptFrameRendered{
		SessionID:       sessionID,
		Iteration:       iteration,
		LayerCount:      len(layerKeys),
		LayerKeys:       layerKeys,
		ScopeCounts:     scopeCounts,
		CharacterCount:  len(rendered),
		EstimatedTokens: estimatePromptTokens(rendered, a.deps.Core.Cfg.Compression.TokenEstimateRatio),
	})
}

func orderedPromptLayers(layers []PromptLayer) []PromptLayer {
	ordered := append([]PromptLayer(nil), layers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Priority < ordered[j].Priority
	})
	return ordered
}

func estimatePromptTokens(text string, ratio float64) int {
	if text == "" {
		return 0
	}
	if ratio <= 0 {
		ratio = 0.25
	}
	estimated := int(float64(len(text)) * ratio)
	if estimated <= 0 {
		return 1
	}
	return estimated
}
