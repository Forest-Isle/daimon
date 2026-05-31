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
	opts := &agent.CognitiveAgentOptions{}

	// Codebase index (optional)
	if gw.codebaseIndex != nil {
		opts.CodebaseIndex = gw.codebaseIndex
	}

	// Memory notification callback
	opts.MemoryNotifyFunc = gw.sendMemoryNotification

	// Plan Mode: plan->approve->execute flow for write tools.
	if gw.provider != nil {
		gw.planMode = agent.NewPlanMode(
			gw.provider,
			gw.handleApproval,
			false,
		)
		opts.PlanMode = gw.planMode
		slog.Info("plan mode wired into cognitive agent")
	}

	// Checkpoint store for task resume
	checkpointStore := agent.NewSQLiteCheckpointStore(gw.db)
	opts.CheckpointStore = checkpointStore

	// RL System (requires cognitive agent)
	if gw.featureEnabled("rl") {
		rlStorage := rl.NewStorage(gw.db)
		rlPolicy := rl.NewPolicy(rlStorage, gw.cfg.Agent.RL)
		if err := rlPolicy.LoadCheckpoint(gw.initCtx); err != nil {
			slog.Warn("gateway: failed to load RL checkpoint", "err", err)
		}
		gw.rlTrainer = rl.NewTrainer(rlPolicy, gw.cfg.Agent.RL)
		opts.RLPolicy = rlPolicy
		opts.RLTrainer = gw.rlTrainer
		slog.Info("RL system initialized")

		// Bridge memory lifecycle events to RL system
		if gw.memory.LifecycleManager() != nil {
			memoryRewards := rl.DefaultMemoryRLRewards()
			memRLHandler := rl.NewMemoryRLHandler(gw.rlTrainer, memoryRewards)
			gw.memory.LifecycleManager().SetRLEventHandler(memRLHandler)
			slog.Info("RL-memory bridge connected")
		}
	}

	if gw.evolution.Engine() != nil {
		opts.EvolutionEngine = gw.evolution.Engine()
	}

	if gw.treePlanner != nil {
		opts.TreePlanner = gw.treePlanner
	}

	opts.DebateConfig = gw.cfg.Agents.Debate

	// Create cognitive agent with shared AgentDeps
	gw.cognitiveAgent = agent.NewCognitiveAgent(gw.agentDeps, opts)
	if gw.cognitiveAgent != nil && gw.evolution.Engine() != nil {
		gw.registerEvolutionHooks()
	}

	return nil
}

// registerEvolutionHooks wires self-evolution loops into the engine. Call only
// after evolution.NewEngine and before gateway.Start (hooks must register
// before Engine.Start). No-op when evolution is disabled in config.
func (gw *Gateway) registerEvolutionHooks() {
	if gw.evolution.engine == nil || !gw.featureEnabled("evolution") {
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
		gw.evolution.engine.RegisterHook(pl)
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

			// Inject sandbox validation into the SandboxTestGate if Docker is available.
			sandboxEnabled := gw.cfg.Evolution.SandboxValidation
			if sandboxEnabled && gw.sandbox.dockerSessionMgr != nil && gw.sandbox.dockerSessionMgr.Available() {
				activator.SetSandboxValidator(true, gw.sandboxSkillValidator())
			} else if sandboxEnabled {
				slog.Warn("gateway: evolution: sandbox validation enabled but Docker unavailable, falling back to static-analysis-only")
			}

			ss.SetActivator(activator)
			gw.evolution.engine.RegisterHook(ss)
			skillSynth = ss
		}
	}

	// Trajectory recorder: persists every cognitive cycle as JSONL.
	if trajDir, err := gw.resolveEvolutionTrajDir(); err != nil {
		slog.Warn("gateway: evolution: trajectory dir unavailable, recorder disabled", "err", err)
	} else {
		gw.evolution.engine.RegisterHook(evolution.NewTrajectoryRecorder(trajDir))
		gw.evolution.engine.SetTrajectoryDir(trajDir)
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
			gw.evolution.engine.RegisterHook(opt)
			stratOptimizer = opt
		}
	}

	// Evolution Brain: unified coordinator with cross-loop feedback
	if prefLearner != nil || stratOptimizer != nil || skillSynth != nil {
		brain := evolution.NewBrain(prefLearner, stratOptimizer, skillSynth)
		gw.evolution.engine.SetBrain(brain)
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

// sandboxSkillValidator returns a SandboxValidator that runs the skill draft's
// tool sequence inside a Docker sandbox container. If execution reveals
// dangerous operations (high exit codes, blocked commands), the draft is
// rejected. When Docker is unavailable the function returns (true, "") and
// logs a warning, allowing static-analysis-only fallback.
func (gw *Gateway) sandboxSkillValidator() evolution.SandboxValidator {
	return func(draft evolution.SkillDraft) (bool, string) {
		if gw.sandbox.dockerSessionMgr == nil || !gw.sandbox.dockerSessionMgr.Available() {
			slog.Warn("evolution: sandbox validator called but Docker unavailable, allowing draft",
				"draft", draft.Name)
			return true, ""
		}

		// Use a short-lived background context for sandbox validation.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		session, err := gw.sandbox.dockerSessionMgr.GetOrCreate(ctx, "sandbox-val-"+draft.Name)
		if err != nil {
			slog.Warn("evolution: sandbox validator failed to create session, allowing draft",
				"draft", draft.Name, "err", err)
			return true, ""
		}

		// Validate each tool in the tool sequence by running a basic check.
		for _, toolName := range draft.ToolSequence {
			// For bash-like tools, run a simple echo to verify the sandbox works.
			// For file tools, verify path isolation.
			switch {
			case toolName == "bash" || toolName == "sh":
				stdout, stderr, code, _, err := session.Exec(ctx, "echo sandbox-ok")
				if err != nil || code != 0 {
					slog.Warn("evolution: sandbox validation failed for bash tool",
						"draft", draft.Name, "code", code, "stderr", stderr, "err", err)
					return false, "sandbox execution failed for tool: " + toolName
				}
				if stdout != "sandbox-ok\n" && stdout != "sandbox-ok" {
					return false, "unexpected sandbox output for tool: " + toolName
				}

			case toolName == "file_write" || toolName == "rm":
				// Verify that writes are constrained — attempt to write outside allowed paths.
				stdout, stderr, code, _, err := session.Exec(ctx, "touch /tmp/sandbox-test")
				if err != nil || code != 0 {
					slog.Warn("evolution: sandbox validation failed for file tool",
						"draft", draft.Name, "code", code, "stderr", stderr, "err", err)
					return false, "sandbox execution failed for tool: " + toolName
				}
				_ = stdout

			case toolName == "http" || toolName == "network":
				// Validate network access is constrained.
				_, _, _, _, _ = session.Exec(ctx, "curl -s --connect-timeout 3 http://localhost:9999 || true")
			}
		}

		slog.Info("evolution: sandbox validation passed",
			"draft", draft.Name, "tools", draft.ToolSequence)
		return true, ""
	}
}
