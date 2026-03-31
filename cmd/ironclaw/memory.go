package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/spf13/cobra"
)

// newMemoryCmd builds the `ironclaw memory` subcommand group.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage IronClaw memory storage",
	}
	cmd.AddCommand(
		newMemoryReindexCmd(),
	)
	return cmd
}

func newMemoryReindexCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild memory index from files",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := store.Open(cfg.Store.Path)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			home, _ := os.UserHomeDir()
			memoryDir := filepath.Join(home, ".IronClaw", "memory")

			fileStore, err := memory.NewFileMemoryStore(memoryDir, db.DB, &memory.NoopEmbedding{}, memory.MemoryConfig{})
			if err != nil {
				return err
			}

			fmt.Println("Rebuilding index...")
			if err := fileStore.RebuildIndex(ctx); err != nil {
				return err
			}

			fmt.Println("Index rebuilt successfully")
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "config file")

	return cmd
}
