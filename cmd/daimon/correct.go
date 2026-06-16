package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/replay"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

func newCorrectCmd() *cobra.Command {
	var note string
	var configPath string
	var devMode bool
	var replaysDir string

	cmd := &cobra.Command{
		Use:   "correct <session-id>",
		Short: "Mark a replay session as user-corrected for regression gating",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			sessionID := strings.TrimSpace(args[0])

			resolvedPath, err := config.FindConfigPath(configPath, devMode)
			if err != nil {
				return fmt.Errorf("find config: %w", err)
			}
			cfg, err := config.Load(resolvedPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			db, err := store.Open(cfg.Store.Path)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			dir := replaysDir
			if dir == "" {
				dir = filepath.Join(appdir.BaseDir(), "replays")
			}
			if sessions, _, err := replay.LoadDir(dir); err != nil {
				fmt.Printf("warning: could not validate session against replay dir %s: %v\n", dir, err)
			} else if !hasReplaySession(sessions, sessionID) {
				fmt.Printf("warning: session %s not found in recorded replays under %s\n", sessionID, dir)
			}

			if err := replay.NewCorrectionStore(db.DB).Mark(ctx, sessionID, note, time.Now().Unix()); err != nil {
				return fmt.Errorf("mark correction: %w", err)
			}
			fmt.Printf("Marked corrected session %s", sessionID)
			if note != "" {
				fmt.Printf(" note=%q", note)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "correction note")
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config file (auto-discovered if empty)")
	cmd.Flags().BoolVar(&devMode, "dev", false, "use configs/daimon.yaml in dev mode")
	cmd.Flags().StringVar(&replaysDir, "replays", "", "replay journals directory (default: ~/.daimon/replays)")
	return cmd
}

func hasReplaySession(sessions []replay.Session, id string) bool {
	for _, s := range sessions {
		if s.SessionID == id {
			return true
		}
	}
	return false
}
