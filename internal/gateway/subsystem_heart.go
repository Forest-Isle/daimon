package gateway

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

// HeartSubsystem owns the autonomous event path: the event heart, the attention
// router that triages each event, and the feedback store for routing
// corrections. It is built only when agent.heart_enabled is true; otherwise the
// binary behaves exactly as before (no heart, chat path unchanged).
type HeartSubsystem struct {
	enabled          bool
	chatThroughHeart bool
	store            *heart.Store
	heart            *heart.Heart
	chain            *attention.Chain
	feedback         *attention.FeedbackStore
}

// RecordChatEvent records an inbound chat message in the unified event stream
// for audit and idempotent dedup, returning inserted=false when the message (by
// its channel-native id) was already seen — a redelivery the caller should skip.
// It does NOT dispatch the turn: chat is handled synchronously by the caller
// (the agent's HandleMessage), so the heart's role here is the durable,
// deduplicated record, not the execution. A nil/disabled subsystem records
// nothing and reports inserted=true so the caller always proceeds.
func (hs *HeartSubsystem) RecordChatEvent(ctx context.Context, msg channel.InboundMessage) (bool, error) {
	if hs == nil || !hs.enabled || !hs.chatThroughHeart || hs.heart == nil {
		return true, nil
	}
	ev := &heart.Event{
		Source:   msg.Channel,
		Kind:     "message",
		Payload:  msg.Text,
		DedupKey: msg.MessageID, // unique within source; "" disables dedup for this msg
	}
	return hs.heart.Record(ctx, ev)
}

func (hs *HeartSubsystem) Name() string { return "heart" }

// Start launches the heart's run loop (unrouted-backlog recovery + sources) in
// the background. It returns immediately; the loop exits when ctx is cancelled.
func (hs *HeartSubsystem) Start(ctx context.Context) error {
	if !hs.enabled || hs.heart == nil {
		return nil
	}
	go func() {
		if err := hs.heart.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("heart: run loop stopped", "err", err)
		}
	}()
	slog.Info("heart started")
	return nil
}

// Stop is a no-op: the run loop and all sources stop when the start ctx is
// cancelled at shutdown.
func (hs *HeartSubsystem) Stop(_ context.Context) error { return nil }

// InitHeart builds the heart subsystem's routing pieces (store, attention chain,
// feedback store). The heart itself — with its dispatch handler — and any event
// sources are attached by the gateway once the agent exists.
func InitHeart(cfg *config.Config, db *store.DB, provider agent.Provider, worldStore *world.Store) *HeartSubsystem {
	hs := &HeartSubsystem{
		enabled:          cfg.Agent.HeartEnabled,
		chatThroughHeart: cfg.Agent.Heart.ChatThroughHeart,
	}
	hs.store = heart.NewStore(db.DB)

	rulesRouter := attention.NewRulesRouter(loadAttentionRules())

	var model attention.ModelRouter
	if cfg.Agent.Heart.ModelRouter {
		if haiku := cfg.LLM.Models.Haiku; haiku != "" && provider != nil {
			ctxFn := func(ctx context.Context) string {
				if worldStore == nil {
					return ""
				}
				digest, err := worldStore.CommitmentsDigest(ctx, "")
				if err != nil {
					return ""
				}
				return digest
			}
			model = attention.NewLLMModelRouter(provider, haiku, ctxFn)
		} else {
			slog.Warn("heart: model_router enabled but llm.models.haiku is empty; using rules-only routing")
		}
	}
	hs.chain = attention.NewChain(rulesRouter, model)
	hs.chain.SetHighRiskKinds(highRiskKinds(cfg))
	hs.feedback = attention.NewFeedbackStore(db.DB)
	return hs
}

// highRiskKinds merges the safe default always-wake whitelist with any
// user-configured additions. Defaults are never removed: a config can only widen
// the safety net, never shrink it.
func highRiskKinds(cfg *config.Config) []string {
	kinds := attention.DefaultHighRiskKinds()
	return append(kinds, cfg.Agent.Heart.HighRiskKinds...)
}

// loadAttentionRules reads ~/.daimon/attention/rules.yaml (a top-level YAML list
// of rules) if present. A missing file yields no rules — every event falls
// through to the Cognize default. A malformed file is logged and ignored rather
// than failing startup or, worse, silently swallowing events.
func loadAttentionRules() []attention.Rule {
	path := filepath.Join(appdir.BaseDir(), "attention", "rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("heart: read attention rules failed", "path", path, "err", err)
		}
		return nil
	}
	var rules []attention.Rule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		slog.Warn("heart: parse attention rules failed", "path", path, "err", err)
		return nil
	}
	slog.Info("heart: loaded attention rules", "count", len(rules))
	return rules
}
