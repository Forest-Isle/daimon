package gateway

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/rl"
)

func (gw *Gateway) initCognitiveAgent() error {
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
	if gw.interceptorChain != nil {
		gw.cognitiveAgent.SetInterceptorChain(gw.interceptorChain)
	}

	// Inject memory notification callback
	gw.cognitiveAgent.SetMemoryNotifyFunc(gw.sendMemoryNotification)

	// Plan Mode: plan→approve→execute flow for write tools.
	// Enabled when cognitive mode is active and provider supports plan generation.
	if gw.provider != nil {
		gw.planMode = agent.NewPlanMode(
			gw.provider,
			gw.handleApproval,
			false, // safe default: require approval for write tools
		)
		gw.cognitiveAgent.SetPlanMode(gw.planMode)
		slog.Info("plan mode wired into cognitive agent")
	}

	// Checkpoint store for task resume
	checkpointStore := agent.NewSQLiteCheckpointStore(gw.db)
	gw.cognitiveAgent.SetCheckpointStore(checkpointStore)

	// RL System (requires cognitive agent)
	if gw.featureEnabled("rl") {
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
	if gw.replayRecorder != nil {
		gw.cognitiveAgent.SetReplayRecorder(gw.replayRecorder)
	}
	if gw.selfHealEngine != nil {
		gw.cognitiveAgent.SetSelfHealEngine(gw.selfHealEngine)
	}

	return nil
}

// registerEvolutionHooks wires self-evolution loops into the engine. Call only
// after evolution.NewEngine and before gateway.Start (hooks must register
// before Engine.Start). No-op when evolution is disabled in config.
func (gw *Gateway) registerEvolutionHooks() {
	if gw.evoEngine == nil || !gw.featureEnabled("evolution") {
		return
	}

	evo := gw.cfg.Evolution

	// Track loop references for Evolution Brain
	var prefLearner *evolution.PreferenceLearner
	var stratOptimizer *evolution.StrategyOptimizer
	var skillSynth *evolution.SkillSynthesizer

	if evo.Preference.Enabled {
		pl := evolution.NewPreferenceLearner(evo.Preference)
		if prefPath, err := gw.resolveEvolutionPreferencePath(evo.PreferenceFile); err == nil && prefPath != "" {
			if err := pl.LoadPreferences(prefPath); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					slog.Warn("gateway: evolution: failed to load preferences, starting fresh",
						"path", prefPath, "err", err)
				}
			}
		}
		gw.evoEngine.RegisterHook(pl)
		prefLearner = pl
	}

	synthCfg := evo.Synthesizer
	if synthCfg.Enabled {
		if p, err := gw.resolveEvolutionDraftsDir(synthCfg.DraftsDir); err != nil {
			slog.Warn("gateway: evolution: skill drafts path unavailable, synthesizer disabled",
				"err", err)
		} else {
			synthCfg.DraftsDir = p
			ss := evolution.NewSkillSynthesizer(synthCfg)
			if synthCfg.LLMEnabled && gw.provider != nil {
				model := synthCfg.LLMModel
				if model == "" {
					model = gw.cfg.LLM.Model
				}
				if proposer := newSkillDraftProposer(gw.provider, model); proposer != nil {
					ss.SetSkillProposer(proposer)
				} else if model == "" {
					slog.Warn("gateway: evolution: synthesizer llm_enabled but no llm.model, using heuristic-only drafts")
				}
			}
			// Wire SkillActivator for auto-promotion through safety gates
			activeDir := filepath.Join(p, "..", "active")
			activator := evolution.NewSkillActivator(p, activeDir)
			ss.SetActivator(activator)
			gw.evoEngine.RegisterHook(ss)
			skillSynth = ss
		}
	}

	// Trajectory recorder: persists every cognitive cycle as JSONL.
	if trajDir, err := gw.resolveEvolutionTrajDir(); err != nil {
		slog.Warn("gateway: evolution: trajectory dir unavailable, recorder disabled", "err", err)
	} else {
		gw.evoEngine.RegisterHook(evolution.NewTrajectoryRecorder(trajDir))
		gw.evoEngine.SetTrajectoryDir(trajDir)
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
			stratOptimizer = opt
		}
	}

	// Evolution Brain: unified coordinator with cross-loop feedback
	if prefLearner != nil || stratOptimizer != nil || skillSynth != nil {
		brain := evolution.NewBrain(prefLearner, stratOptimizer, skillSynth)
		gw.evoEngine.SetBrain(brain)
		slog.Info("evolution brain wired into engine",
			"preference", prefLearner != nil,
			"optimizer", stratOptimizer != nil,
			"synthesizer", skillSynth != nil,
		)
	}

	// Schedule trajectory cleanup (retain 30 days of detailed data)
	if trajDir, err := gw.resolveEvolutionTrajDir(); err == nil {
		go func() {
			removed, err := evolution.CleanupTrajectories(trajDir, 30*24*60*60*1e9) // 30 days
			if err != nil {
				slog.Warn("gateway: trajectory cleanup failed", "err", err)
			} else if removed > 0 {
				slog.Info("gateway: cleaned old trajectories", "removed", removed)
			}
		}()
	}

	// Schedule preference decay (run once at startup; future: periodic via ticker)
	if prefLearner != nil {
		go func() {
			decayed := prefLearner.DecayPreferences(time.Now(), 7*24*time.Hour) // 7-day half-life
			if decayed > 0 {
				slog.Info("gateway: decayed stale preferences", "removed", decayed)
			}
		}()
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

// resolveEvolutionPreferencePath turns a relative preference_file into an absolute path.
func (gw *Gateway) resolveEvolutionPreferencePath(prefFile string) (string, error) {
	if prefFile == "" {
		return "", nil
	}
	if filepath.IsAbs(prefFile) {
		return prefFile, nil
	}
	base, err := gw.ironclawHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "evolution", prefFile), nil
}

// resolveEvolutionTrajDir returns the absolute path to ~/.IronClaw/evolution/trajectories/.
func (gw *Gateway) resolveEvolutionTrajDir() (string, error) {
	base, err := gw.ironclawHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "evolution", "trajectories"), nil
}
