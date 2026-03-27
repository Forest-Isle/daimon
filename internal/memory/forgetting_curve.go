package memory

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

type ForgettingCurveManager struct {
	store     *SQLiteStore
	accessLog *AccessLog
	db        *store.DB
}

func NewForgettingCurveManager(s *SQLiteStore, db *store.DB) *ForgettingCurveManager {
	return &ForgettingCurveManager{
		store:     s,
		accessLog: NewAccessLog(db),
		db:        db,
	}
}

// ComputeStrength calculates memory strength using forgetting curve
func (fc *ForgettingCurveManager) ComputeStrength(ctx context.Context, fact Entry) float64 {
	// Base importance from metadata
	baseImportance := 1.0
	if imp, ok := fact.Metadata["importance"]; ok {
		if v, err := strconv.ParseFloat(imp, 64); err == nil {
			baseImportance = v
		}
	}

	// Time decay: R(t) = e^(-t/S)
	elapsedHours := time.Since(fact.CreatedAt).Hours()
	stability := baseImportance * 24 // Important facts decay slower
	retention := math.Exp(-elapsedHours / stability)

	// Access bonus (skip if no access log)
	if fc.accessLog == nil {
		return retention
	}

	accessCount, lastAccess, err := fc.accessLog.GetStats(ctx, fact.ID)
	if err != nil {
		return retention
	}

	accessBonus := 1.0 + 0.1*float64(accessCount)

	// Recent access boost
	if !lastAccess.IsZero() && time.Since(lastAccess).Hours() < 24 {
		accessBonus *= 1.5
	}

	return retention * accessBonus
}

// FadeWeakMemories archives facts with low strength
func (fc *ForgettingCurveManager) FadeWeakMemories(ctx context.Context) error {
	rows, err := fc.db.QueryContext(ctx, `
		SELECT id, session_id, user_id, scope, content, embedding, version, expires_at, metadata, created_at, updated_at
		FROM memory_facts
		WHERE scope IN ('session', 'user')
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	faded := 0
	for rows.Next() {
		var fact Entry
		var metadataJSON string
		var embBytes []byte
		if err := rows.Scan(&fact.ID, &fact.SessionID, &fact.UserID, &fact.Scope, &fact.Content,
			&embBytes, &fact.Version, &fact.ExpiresAt, &metadataJSON,
			&fact.CreatedAt, &fact.UpdatedAt); err != nil {
			slog.Warn("forgetting_curve: scan error", "err", err)
			continue
		}

		strength := fc.ComputeStrength(ctx, fact)
		if strength < 0.3 {
			// Archive weak memory (use background context to avoid deadline)
			_, err := fc.db.Exec(`UPDATE memory_facts SET scope = 'archive' WHERE id = ?`, fact.ID)
			if err == nil {
				faded++
			} else {
				slog.Warn("forgetting_curve: update error", "id", fact.ID, "err", err)
			}
		}
	}

	slog.Info("forgetting_curve: faded weak memories", "count", faded)
	return nil
}
