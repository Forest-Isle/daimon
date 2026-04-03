package rl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// Storage handles persistence of RL data to SQLite.
type Storage struct {
	db *store.DB
}

// NewStorage creates a new RL storage instance.
func NewStorage(db *store.DB) *Storage {
	return &Storage{db: db}
}

// CreateEpisode creates a new episode record.
func (s *Storage) CreateEpisode(ctx context.Context, sessionID, goal, complexity string) (string, error) {
	episodeID := uuid.New().String()
	query := `
		INSERT INTO rl_episodes (id, session_id, goal, complexity, created_at)
		VALUES (?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, episodeID, sessionID, goal, complexity, time.Now())
	if err != nil {
		return "", fmt.Errorf("create episode: %w", err)
	}
	return episodeID, nil
}

// UpdateEpisode updates episode metrics after completion.
func (s *Storage) UpdateEpisode(ctx context.Context, episodeID string, totalReward float64, succeeded bool, subtaskCount, replanCount int, durationMs int64) error {
	query := `
		UPDATE rl_episodes
		SET total_reward = ?, succeeded = ?, subtask_count = ?, replan_count = ?, duration_ms = ?
		WHERE id = ?
	`
	succeededInt := 0
	if succeeded {
		succeededInt = 1
	}
	_, err := s.db.ExecContext(ctx, query, totalReward, succeededInt, subtaskCount, replanCount, durationMs, episodeID)
	if err != nil {
		return fmt.Errorf("update episode: %w", err)
	}
	return nil
}

// AddTrajectory records a state-action-reward transition.
func (s *Storage) AddTrajectory(ctx context.Context, episodeID string, step int, level string, state, action []byte, reward float64, nextState []byte, done bool) error {
	trajID := uuid.New().String()
	doneInt := 0
	if done {
		doneInt = 1
	}
	query := `
		INSERT INTO rl_trajectories (id, episode_id, step, level, state, action, reward, next_state, done, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, trajID, episodeID, step, level, state, action, reward, nextState, doneInt, time.Now())
	if err != nil {
		return fmt.Errorf("add trajectory: %w", err)
	}
	return nil
}

// AddReward records a decomposed reward component.
func (s *Storage) AddReward(ctx context.Context, episodeID, rewardType string, value, weight float64) error {
	rewardID := uuid.New().String()
	query := `
		INSERT INTO rl_rewards (id, episode_id, reward_type, value, weight, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query, rewardID, episodeID, rewardType, value, weight, time.Now())
	if err != nil {
		return fmt.Errorf("add reward: %w", err)
	}
	return nil
}

// SaveCheckpoint saves a policy checkpoint.
func (s *Storage) SaveCheckpoint(ctx context.Context, policyName string, version int, stateDim, actionDim int, weights []byte, metrics map[string]float64) error {
	checkpointID := uuid.New().String()
	metricsJSON, _ := json.Marshal(metrics)
	query := `
		INSERT INTO rl_model_checkpoints (id, policy_name, version, state_dim, action_dim, weights, metrics, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(policy_name, version) DO UPDATE SET
			weights = excluded.weights,
			metrics = excluded.metrics,
			created_at = excluded.created_at
	`
	_, err := s.db.ExecContext(ctx, query, checkpointID, policyName, version, stateDim, actionDim, weights, string(metricsJSON), time.Now())
	if err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint loads the latest checkpoint for a policy.
func (s *Storage) LoadCheckpoint(ctx context.Context, policyName string) ([]byte, int, error) {
	query := `
		SELECT weights, version FROM rl_model_checkpoints
		WHERE policy_name = ?
		ORDER BY version DESC
		LIMIT 1
	`
	var weights []byte
	var version int
	err := s.db.QueryRowContext(ctx, query, policyName).Scan(&weights, &version)
	if err != nil {
		return nil, 0, fmt.Errorf("load checkpoint: %w", err)
	}
	return weights, version, nil
}

// GetBanditArm retrieves bandit arm statistics.
func (s *Storage) GetBanditArm(ctx context.Context, contextHash, armName string) (alpha, beta float64, pulls int, totalReward float64, err error) {
	query := `
		SELECT alpha, beta, pulls, total_reward FROM rl_bandit_arms
		WHERE context_hash = ? AND arm_name = ?
	`
	err = s.db.QueryRowContext(ctx, query, contextHash, armName).Scan(&alpha, &beta, &pulls, &totalReward)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return alpha, beta, pulls, totalReward, nil
}

// UpdateBanditArm updates bandit arm statistics after a pull.
func (s *Storage) UpdateBanditArm(ctx context.Context, contextHash, armName string, alpha, beta float64, pulls int, totalReward float64) error {
	armID := uuid.New().String()
	query := `
		INSERT INTO rl_bandit_arms (id, context_hash, arm_name, alpha, beta, pulls, total_reward, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(context_hash, arm_name) DO UPDATE SET
			alpha = excluded.alpha,
			beta = excluded.beta,
			pulls = excluded.pulls,
			total_reward = excluded.total_reward,
			updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query, armID, contextHash, armName, alpha, beta, pulls, totalReward, time.Now())
	if err != nil {
		return fmt.Errorf("update bandit arm: %w", err)
	}
	return nil
}

// GetRecentEpisodes retrieves the N most recent episodes.
func (s *Storage) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	query := `
		SELECT id, session_id, goal, complexity, total_reward, succeeded, subtask_count, replan_count, duration_ms, created_at
		FROM rl_episodes
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent episodes: %w", err)
	}
	defer rows.Close()

	var episodes []Episode
	for rows.Next() {
		var ep Episode
		var succeededInt int
		err := rows.Scan(&ep.ID, &ep.SessionID, &ep.Goal, &ep.Complexity, &ep.TotalReward, &succeededInt, &ep.SubtaskCount, &ep.ReplanCount, &ep.DurationMs, &ep.CreatedAt)
		if err != nil {
			return nil, err
		}
		ep.Succeeded = succeededInt == 1
		episodes = append(episodes, ep)
	}
	return episodes, nil
}

// Episode represents a training episode record.
type Episode struct {
	ID           string
	SessionID    string
	Goal         string
	Complexity   string
	TotalReward  float64
	Succeeded    bool
	SubtaskCount int
	ReplanCount  int
	DurationMs   int64
	CreatedAt    time.Time
}
