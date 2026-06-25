package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

// newMemoryCmd builds the `daimon memory` subcommand group.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage Daimon memory storage",
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

			resolvedPath, err := config.FindConfigPath(configPath, false)
			if err != nil {
				return err
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

			memoryDir := filepath.Join(appdir.BaseDir(), "memory")

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

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "config file")

	return cmd
}
