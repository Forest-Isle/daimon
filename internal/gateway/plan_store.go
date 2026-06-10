package gateway

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

// sessionPlanStore implements tool.PlanStore backed by the session manager.
type sessionPlanStore struct {
	sessions *session.Manager
}

func (s *sessionPlanStore) GetPlan(sessionID string) (string, error) {
	sess, err := s.sessions.GetByID(context.Background(), sessionID)
	if err != nil {
		return "", err
	}
	return sess.GetMetadata("plan"), nil
}

func (s *sessionPlanStore) SavePlan(sessionID string, planJSON string) error {
	sess, err := s.sessions.GetByID(context.Background(), sessionID)
	if err != nil {
		return err
	}
	sess.SetMetadata("plan", planJSON)
	return s.sessions.Persist(context.Background(), sess)
}
