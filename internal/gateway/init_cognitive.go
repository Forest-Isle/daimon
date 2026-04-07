package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/rl"
)

func (gw *Gateway) initCognitiveAgent() error {
	if gw.cfg.Agent.Mode != "cognitive" {
		return nil
	}

	gw.cognitiveAgent = agent.NewCognitiveAgent(gw.provider, gw.tools, gw.sessions, gw.db, gw.cfg.Agent, gw.cfg.LLM)
	if gw.memStore != nil {
		gw.cognitiveAgent.SetMemoryStore(gw.memStore)
	}
	if gw.factExtractor != nil {
		gw.cognitiveAgent.SetFactExtractor(gw.factExtractor)
	}
	if gw.lifecycleMgr != nil {
		gw.cognitiveAgent.SetLifecycleManager(gw.lifecycleMgr)
	}

	// Inject memory notification callback
	gw.cognitiveAgent.SetMemoryNotifyFunc(gw.sendMemoryNotification)

	// RL System (requires cognitive agent)
	if gw.cfg.Agent.RL.Enabled {
		rlStorage := rl.NewStorage(gw.db)
		rlPolicy := rl.NewPolicy(rlStorage, gw.cfg.Agent.RL)
		if err := rlPolicy.LoadCheckpoint(context.Background()); err != nil {
			slog.Warn("gateway: failed to load RL checkpoint", "err", err)
		}
		gw.rlTrainer = rl.NewTrainer(rlPolicy, gw.cfg.Agent.RL)
		gw.cognitiveAgent.SetRLPolicy(rlPolicy)
		gw.cognitiveAgent.SetRLTrainer(gw.rlTrainer)
		slog.Info("RL system initialized")
	}

	return nil
}
