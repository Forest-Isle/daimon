package memory

import (
	"context"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
	"gopkg.in/yaml.v3"
)

type ForgettingCurveManager struct {
	accessLog *AccessLog
	db        *store.DB
}

func NewForgettingCurveManager(db *store.DB) *ForgettingCurveManager {
	return &ForgettingCurveManager{
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

	// Type-dependent stability multiplier
	typeMultiplier := 24.0 // semantic default
	memType := ""
	if t, ok := fact.Metadata["type"]; ok {
		memType = t
	}
	switch memType {
	case "episodic":
		typeMultiplier = 12.0
	case "procedural":
		typeMultiplier = 48.0
	}
	stability := baseImportance * typeMultiplier

	// Time decay: R(t) = e^(-t/S)
	elapsedHours := time.Since(fact.CreatedAt).Hours()
	retention := math.Exp(-elapsedHours / stability)

	// Access bonus from last_accessed_at in frontmatter
	if fc.accessLog == nil {
		return retention
	}

	accessCount, lastAccess, err := fc.accessLog.GetStats(ctx, fact.ID)
	if err != nil {
		return retention
	}

	// Type-dependent access factor
	accessFactor := 0.1
	if memType == "procedural" {
		accessFactor = 0.12
	}
	accessBonus := 1.0 + accessFactor*float64(accessCount)

	// Recent access boost
	if !lastAccess.IsZero() && time.Since(lastAccess).Hours() < 24 {
		accessBonus *= 1.5
	}

	return retention * accessBonus
}

// ComputeStrengthFromFile calculates strength reading last_accessed_at from file frontmatter
func (fc *ForgettingCurveManager) ComputeStrengthFromFile(mf *MemoryFile) float64 {
	baseImportance := 1.0
	if imp, ok := mf.Metadata["importance"]; ok {
		if v, err := strconv.ParseFloat(imp, 64); err == nil {
			baseImportance = v
		}
	}
	// Also check the direct Importance field
	if mf.Importance > 0 {
		baseImportance = float64(mf.Importance)
	}

	// Type-dependent stability multiplier
	typeMultiplier := 24.0
	switch mf.Type {
	case "episodic":
		typeMultiplier = 12.0
	case "procedural":
		typeMultiplier = 48.0
	}
	stability := baseImportance * typeMultiplier

	elapsedHours := time.Since(mf.CreatedAt).Hours()
	retention := math.Exp(-elapsedHours / stability)

	if mf.LastAccessed != nil {
		hoursSinceAccess := time.Since(*mf.LastAccessed).Hours()
		if hoursSinceAccess < 24 {
			retention *= 1.5
		}
	}

	return retention
}

// FadeWeakMemories archives memories with low strength from the memory_index table.
// It queries memory_index for weak entries and moves the corresponding files to archived/.
func (fc *ForgettingCurveManager) FadeWeakMemories(ctx context.Context, baseDir string) error {
	rows, err := fc.db.QueryContext(ctx, `
		SELECT memory_id, file_path, strength
		FROM memory_index
		WHERE scope IN ('session', 'user') AND strength < 0.3
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	faded := 0
	for rows.Next() {
		var id, filePath string
		var strength float64
		if err := rows.Scan(&id, &filePath, &strength); err != nil {
			slog.Warn("forgetting_curve: scan error", "err", err)
			continue
		}

		// Move file to archived/
		archivedPath := filepath.Join(baseDir, "archived", filepath.Base(filePath))
		if err := os.Rename(filePath, archivedPath); err != nil {
			slog.Warn("forgetting_curve: failed to archive file", "id", id, "err", err)
			continue
		}

		// Update memory_index
		_, err := fc.db.ExecContext(ctx, `
			UPDATE memory_index SET scope = 'archived', file_path = ? WHERE memory_id = ?
		`, archivedPath, id)
		if err != nil {
			slog.Warn("forgetting_curve: failed to update index", "id", id, "err", err)
		}

		faded++
	}

	slog.Info("forgetting_curve: faded weak memories", "count", faded)
	return nil
}

// FadeWeakMemoriesFromFiles scans memory files and moves weak ones to archived/
func (fc *ForgettingCurveManager) FadeWeakMemoriesFromFiles(ctx context.Context, baseDir string) error {
	faded := 0
	scopes := []string{"user", "session"}

	for _, scope := range scopes {
		scopeDir := filepath.Join(baseDir, scope)
		files, err := filepath.Glob(filepath.Join(scopeDir, "*.md"))
		if err != nil {
			continue
		}

		for _, filePath := range files {
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			parts := strings.SplitN(string(data), "---\n", 3)
			if len(parts) < 3 {
				continue
			}

			var mf MemoryFile
			if err := yaml.Unmarshal([]byte(parts[1]), &mf); err != nil {
				continue
			}

			strength := fc.ComputeStrengthFromFile(&mf)
			if strength < 0.3 {
				archivedPath := filepath.Join(baseDir, "archived", filepath.Base(filePath))
				if err := os.Rename(filePath, archivedPath); err == nil {
					faded++
				}
			}
		}
	}

	slog.Info("forgetting_curve: faded weak memory files", "count", faded)
	return nil
}

// FadeByRetentionPolicy archives memories that exceed their type-specific retention period.
// Retention durations of 0 mean no time-based archival for that type (forgetting curve only).
func (fc *ForgettingCurveManager) FadeByRetentionPolicy(ctx context.Context, baseDir string, cfg MemoryConfig) error {
	// Build a map of memory_type → retention duration (skip zero durations)
	retentionPolicies := map[string]time.Duration{}
	if cfg.RetentionEpisodic > 0 {
		retentionPolicies["episodic"] = cfg.RetentionEpisodic
	}
	if cfg.RetentionSemantic > 0 {
		retentionPolicies["semantic"] = cfg.RetentionSemantic
	}
	if cfg.RetentionProcedural > 0 {
		retentionPolicies["procedural"] = cfg.RetentionProcedural
	}

	if len(retentionPolicies) == 0 {
		return nil // No retention policies configured
	}

	archived := 0
	now := time.Now()

	for memType, retention := range retentionPolicies {
		cutoff := now.Add(-retention)

		rows, err := fc.db.QueryContext(ctx, `
			SELECT memory_id, file_path
			FROM memory_index
			WHERE memory_type = ? AND scope IN ('session', 'user') AND created_at < ?
		`, memType, cutoff)
		if err != nil {
			slog.Warn("forgetting_curve: retention query failed", "type", memType, "err", err)
			continue
		}

		for rows.Next() {
			var id, filePath string
			if err := rows.Scan(&id, &filePath); err != nil {
				continue
			}

			archivedPath := filepath.Join(baseDir, "archived", filepath.Base(filePath))
			if err := os.Rename(filePath, archivedPath); err != nil {
				slog.Warn("forgetting_curve: retention archive failed", "id", id, "err", err)
				continue
			}

			// Update memory_index
			_, err := fc.db.ExecContext(ctx, `
				UPDATE memory_index SET scope = 'archived', file_path = ? WHERE memory_id = ?
			`, archivedPath, id)
			if err != nil {
				slog.Warn("forgetting_curve: retention index update failed", "id", id, "err", err)
			}
			archived++
		}
		_ = rows.Close()
	}

	if archived > 0 {
		slog.Info("forgetting_curve: archived by retention policy", "count", archived)
	}
	return nil
}
