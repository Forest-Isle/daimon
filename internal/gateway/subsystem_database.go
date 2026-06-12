package gateway

import (
	"context"
	ierrors "github.com/Forest-Isle/daimon/internal/errors"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"log/slog"
)

type DatabaseSubsystem struct {
	DB       *store.DB
	Sessions *session.Manager
}

func (ds *DatabaseSubsystem) Name() string                  { return "database" }
func (ds *DatabaseSubsystem) Start(_ context.Context) error { return nil }
func (ds *DatabaseSubsystem) Stop(_ context.Context) error {
	if ds.DB != nil {
		_ = ds.DB.Close()
	}
	return nil
}

func InitDatabase(dbPath string) (*DatabaseSubsystem, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, ierrors.Wrap(err, ierrors.KindUnavailable, "failed to open database")
	}
	slog.Info("database opened", "path", dbPath)
	return &DatabaseSubsystem{DB: db, Sessions: session.NewManager(db)}, nil
}
