package gateway

import (
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"

	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"
)

func (gw *Gateway) initDatabase() error {
	db, err := store.Open(gw.Config().Store.Path)
	if err != nil {
		return ierrors.Wrap(err, ierrors.KindUnavailable, "failed to open database")
	}
	gw.db = db
	gw.sessions = session.NewManager(db)
	return nil
}
