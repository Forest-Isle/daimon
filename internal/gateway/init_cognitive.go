package gateway

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
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

	// Wire hook manager and permission engine (security: parity with simple runtime)
	if gw.hookMgr != nil {
		gw.cognitiveAgent.SetHookManager(gw.hookMgr)
	}
	if gw.permEngine != nil {
		gw.cognitiveAgent.SetPermissionEngine(gw.permEngine)
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

		// Bridge memory lifecycle events to RL system
		if gw.lifecycleMgr != nil {
			memoryRewards := rl.DefaultMemoryRLRewards()
			memRLHandler := rl.NewMemoryRLHandler(gw.rlTrainer, memoryRewards)
			gw.lifecycleMgr.SetRLEventHandler(memRLHandler)
			slog.Info("RL-memory bridge connected")
		}
	}

	if gw.cognitiveAgent != nil && gw.evoEngine != nil {
		gw.cognitiveAgent.SetEvolutionEngine(gw.evoEngine)
		gw.registerEvolutionHooks()
	}

	return nil
}

// registerEvolutionHooks wires self-evolution loops into the engine. Call only
// after evolution.NewEngine and before gateway.Start (hooks must register
// before Engine.Start). No-op when evolution is disabled in config.
func (gw *Gateway) registerEvolutionHooks() {
	if gw.evoEngine == nil || !gw.cfg.Evolution.Enabled {
		return
	}

	evo := gw.cfg.Evolution

	if evo.Preference.Enabled {
		gw.evoEngine.RegisterHook(evolution.NewPreferenceLearner(evo.Preference))
	}

	synthCfg := evo.Synthesizer
	if synthCfg.Enabled {
		if p, err := gw.resolveEvolutionDraftsDir(synthCfg.DraftsDir); err != nil {
			slog.Warn("gateway: evolution: skill drafts path unavailable, synthesizer disabled",
				"err", err)
		} else {
			synthCfg.DraftsDir = p
			gw.evoEngine.RegisterHook(evolution.NewSkillSynthesizer(synthCfg))
		}
	}

	// Trajectory recorder: persists every cognitive cycle as JSONL.
	if trajDir, err := gw.resolveEvolutionTrajDir(); err != nil {
		slog.Warn("gateway: evolution: trajectory dir unavailable, recorder disabled", "err", err)
	} else {
		gw.evoEngine.RegisterHook(evolution.NewTrajectoryRecorder(trajDir))
	}

	optCfg := evo.Optimizer
	if optCfg.Enabled {
		strategyPath, err := gw.resolveEvolutionStrategyPath(optCfg.StrategyFile)
		if err != nil {
			slog.Warn("gateway: evolution: strategy file path unavailable, optimizer disabled",
				"err", err)
		} else {
			optCfg.StrategyFile = strategyPath
			opt := evolution.NewStrategyOptimizer(optCfg)
			if strategyPath != "" {
				if err := opt.LoadStrategy(strategyPath); err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						slog.Warn("gateway: evolution: failed to load strategy file, using defaults",
							"path", strategyPath, "err", err)
					}
				}
			}
			gw.evoEngine.RegisterHook(opt)
		}
	}
}

func (gw *Gateway) ironclawHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".IronClaw"), nil
}

// resolveEvolutionDraftsDir turns a relative drafts_dir (under ~/.IronClaw/skills/) into an absolute path.
func (gw *Gateway) resolveEvolutionDraftsDir(draftsDir string) (string, error) {
	if draftsDir == "" {
		return "", errors.New("drafts_dir is empty")
	}
	if filepath.IsAbs(draftsDir) {
		return draftsDir, nil
	}
	base, err := gw.ironclawHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "skills", draftsDir), nil
}

// resolveEvolutionStrategyPath turns a relative strategy_file (under ~/.IronClaw/evolution/) into an absolute path.
func (gw *Gateway) resolveEvolutionStrategyPath(strategyFile string) (string, error) {
	if strategyFile == "" {
		return "", nil
	}
	if filepath.IsAbs(strategyFile) {
		return strategyFile, nil
	}
	base, err := gw.ironclawHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "evolution", strategyFile), nil
}

// resolveEvolutionTrajDir returns the absolute path to ~/.IronClaw/evolution/trajectories/.
func (gw *Gateway) resolveEvolutionTrajDir() (string, error) {
	base, err := gw.ironclawHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "evolution", "trajectories"), nil
}
