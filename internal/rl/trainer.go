package rl

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/config"
)

// Trainer coordinates RL training in the background.
type Trainer struct {
	policy     *Policy
	buffer     *ExperienceBuffer
	cfg        config.RLConfig
	episodeNum int
	mu         sync.Mutex
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewTrainer creates a new RL trainer.
func NewTrainer(policy *Policy, cfg config.RLConfig) *Trainer {
	bufferSize := cfg.DQN.BufferSize
	if bufferSize <= 0 {
		bufferSize = 10000
	}

	return &Trainer{
		policy: policy,
		buffer: NewExperienceBuffer(bufferSize),
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start begins the background training loop.
func (t *Trainer) Start(ctx context.Context) {
	if !t.policy.IsEnabled() {
		return
	}

	t.wg.Add(1)
	go t.trainLoop(ctx)
	slog.Info("rl trainer: started")
}

// Stop gracefully stops the trainer.
func (t *Trainer) Stop() {
	close(t.stopCh)
	t.wg.Wait()
	slog.Info("rl trainer: stopped")
}

// AddExperience adds an experience to the replay buffer.
func (t *Trainer) AddExperience(exp Experience) {
	t.buffer.Add(exp)
}

// RecordEpisode records a completed episode to storage.
func (t *Trainer) RecordEpisode(ctx context.Context, params EpisodeParams) error {
	t.mu.Lock()
	t.episodeNum++
	episodeNum := t.episodeNum
	t.mu.Unlock()

	// Create episode record
	episodeID, err := t.policy.GetStorage().CreateEpisode(ctx, params.SessionID, params.Goal, params.Complexity)
	if err != nil {
		return err
	}

	// Compute total reward
	rewardCfg := t.cfg.Reward
	rc := ComputeEpisodeReward(EpisodeRewardParams{
		Succeeded:      params.Succeeded,
		DurationMs:     params.DurationMs,
		MaxDurationMs:  params.MaxDurationMs,
		ReplanCount:    params.ReplanCount,
		SuccessCount:   params.SuccessCount,
		FailureCount:   params.FailureCount,
		DeniedCount:    params.DeniedCount,
		UserFeedback:   params.UserFeedback,
	}, rewardCfg)

	totalReward := rc.Total(rewardCfg)

	// Update episode record
	err = t.policy.GetStorage().UpdateEpisode(ctx, episodeID, totalReward, params.Succeeded, params.SubtaskCount, params.ReplanCount, params.DurationMs)
	if err != nil {
		return err
	}

	// Store reward components
	t.policy.GetStorage().AddReward(ctx, episodeID, "task_success", rc.TaskSuccess, rewardCfg.TaskSuccessWeight)
	t.policy.GetStorage().AddReward(ctx, episodeID, "efficiency", rc.Efficiency, rewardCfg.EfficiencyWeight)
	t.policy.GetStorage().AddReward(ctx, episodeID, "safety", rc.Safety, rewardCfg.SafetyWeight)
	t.policy.GetStorage().AddReward(ctx, episodeID, "user_satisfaction", rc.UserSatisfaction, rewardCfg.UserSatisfactionWeight)

	// Store trajectories
	for i, exp := range params.Experiences {
		stateBytes := exp.State.Encode()
		actionBytes := EncodeAction(exp.Level, exp.Action)
		var nextStateBytes []byte
		if exp.NextState != nil {
			nextStateBytes = exp.NextState.Encode()
		}
		t.policy.GetStorage().AddTrajectory(ctx, episodeID, i, exp.Level, stateBytes, actionBytes, exp.Reward, nextStateBytes, exp.Done)
	}

	slog.Info("rl trainer: episode recorded",
		"episode", episodeNum,
		"total_reward", totalReward,
		"succeeded", params.Succeeded,
		"subtasks", params.SubtaskCount,
	)

	return nil
}

// trainLoop runs periodic training updates.
func (t *Trainer) trainLoop(ctx context.Context) {
	defer t.wg.Done()

	updateFreq := t.cfg.UpdateFrequency
	if updateFreq <= 0 {
		updateFreq = 10
	}

	checkpointFreq := t.cfg.CheckpointFrequency
	if checkpointFreq <= 0 {
		checkpointFreq = 100
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.performUpdate(ctx, updateFreq, checkpointFreq)
		}
	}
}

// performUpdate performs a training update if enough episodes have accumulated.
func (t *Trainer) performUpdate(ctx context.Context, updateFreq, checkpointFreq int) {
	t.mu.Lock()
	episodeNum := t.episodeNum
	t.mu.Unlock()

	// Check if we should update
	if episodeNum%updateFreq != 0 {
		return
	}

	bufferSize := t.buffer.Size()
	if bufferSize < 32 {
		return // Not enough data yet
	}

	slog.Info("rl trainer: performing update", "episode", episodeNum, "buffer_size", bufferSize)

	// Update PPO
	if t.policy.GetPPO() != nil {
		ppoBatch := t.buffer.SampleByLevel(LevelPPO, 64)
		if len(ppoBatch) > 0 {
			loss := t.policy.GetPPO().Update(ppoBatch)
			slog.Debug("rl trainer: PPO updated", "loss", loss, "batch_size", len(ppoBatch))
		}
	}

	// Update DQN
	if t.policy.GetDQN() != nil {
		dqnBatch := t.buffer.SampleByLevel(LevelDQN, 64)
		if len(dqnBatch) > 0 {
			loss := t.policy.GetDQN().Update(dqnBatch)
			slog.Debug("rl trainer: DQN updated", "loss", loss, "batch_size", len(dqnBatch))
		}
	}

	// Save checkpoint periodically
	if episodeNum%checkpointFreq == 0 {
		err := t.policy.SaveCheckpoint(ctx, episodeNum)
		if err != nil {
			slog.Warn("rl trainer: checkpoint save failed", "err", err)
		}
	}
}

// GetEpisodeCount returns the current episode number.
func (t *Trainer) GetEpisodeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.episodeNum
}

// EpisodeParams holds parameters for recording an episode.
type EpisodeParams struct {
	SessionID    string
	Goal         string
	Complexity   string
	Succeeded    bool
	DurationMs   int64
	MaxDurationMs int64
	SubtaskCount int
	ReplanCount  int
	SuccessCount int
	FailureCount int
	DeniedCount  int
	UserFeedback float64
	Experiences  []Experience
}
