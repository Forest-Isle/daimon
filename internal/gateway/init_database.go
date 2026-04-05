package gateway

import (
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

func (gw *Gateway) initDatabase() error {
	db, err := store.Open(gw.cfg.Store.Path)
	if err != nil {
		return err
	}
	gw.db = db
	gw.sessions = session.NewManager(db)
	return nil
}
