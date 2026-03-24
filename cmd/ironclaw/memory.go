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
		newMemoryExportCmd(),
		newMemoryVerifyCmd(),
		newMemoryStatsCmd(),
	)
	return cmd
}

func newMemoryExportCmd() *cobra.Command {
	var outputDir string
	var configPath string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export memory from SQLite to file-based storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Load config
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Determine output directory
			if outputDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home dir: %w", err)
				}
				outputDir = filepath.Join(home, ".IronClaw", "memory")
			}

			fmt.Printf("Exporting memory from SQLite to %s\n", outputDir)

			// Open database
			db, err := store.Open(cfg.Store.Path)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer db.Close()

			// Create embeddings DB
			memCfg := memory.MemoryConfig{
				FactExtraction:      cfg.Memory.FactExtraction,
				SimilarityThreshold: cfg.Memory.SimilarityThreshold,
				BM25Weight:          cfg.Memory.BM25Weight,
				VectorWeight:        cfg.Memory.VectorWeight,
				EnableVSS:           cfg.Memory.EnableVSS,
				VectorDimension:     1536,
				EnableSearchCache:   cfg.Memory.EnableSearchCache,
				SearchCacheSize:     cfg.Memory.SearchCacheSize,
				SearchCacheTTL:      cfg.Memory.SearchCacheTTL,
			}

			embeddingsDB := memory.NewEmbeddingsDB(db, memCfg)

			// Create embedder (noop for migration)
			embedder := &memory.NoopEmbedding{}

			// Create file store
			fileStore := memory.NewFileStore(outputDir, embeddingsDB, embedder, memCfg)

			// Create migrator
			migrator := memory.NewMigrator(db, fileStore, embeddingsDB, embedder)

			// Perform migration
			fmt.Println("Starting migration...")

			stats, err := migrator.Migrate(ctx)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}

			// Print results
			fmt.Printf("\n✓ Migration completed in %s\n\n", stats.Duration)
			fmt.Printf("Total facts:       %d\n", stats.TotalFacts)
			fmt.Printf("  Session facts:   %d\n", stats.SessionFacts)
			fmt.Printf("  User facts:      %d\n", stats.UserFacts)
			fmt.Printf("  Global facts:    %d\n", stats.GlobalFacts)
			fmt.Printf("Files created:     %d\n", stats.FilesCreated)
			fmt.Printf("Embeddings copied: %d\n", stats.EmbeddingsCopied)

			if len(stats.Errors) > 0 {
				fmt.Printf("\n⚠ Errors encountered: %d\n", len(stats.Errors))
				for i, err := range stats.Errors {
					if i < 5 { // Show first 5 errors
						fmt.Printf("  - %s\n", err)
					}
				}
				if len(stats.Errors) > 5 {
					fmt.Printf("  ... and %d more\n", len(stats.Errors)-5)
				}
			}

			fmt.Printf("\nMemory exported to: %s\n", outputDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: ~/.IronClaw/memory)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "path to config file")

	return cmd
}

func newMemoryVerifyCmd() *cobra.Command {
	var outputDir string
	var configPath string

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify integrity of migrated memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Load config
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Determine output directory
			if outputDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home dir: %w", err)
				}
				outputDir = filepath.Join(home, ".IronClaw", "memory")
			}

			fmt.Printf("Verifying memory integrity...\n")

			// Open database
			db, err := store.Open(cfg.Store.Path)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer db.Close()

			// Create embeddings DB
			memCfg := memory.MemoryConfig{
				VectorDimension: 1536,
			}
			embeddingsDB := memory.NewEmbeddingsDB(db, memCfg)

			// Create file store
			embedder := &memory.NoopEmbedding{}
			fileStore := memory.NewFileStore(outputDir, embeddingsDB, embedder, memCfg)

			// Create migrator
			migrator := memory.NewMigrator(db, fileStore, embeddingsDB, embedder)

			// Verify
			if err := migrator.Verify(ctx); err != nil {
				fmt.Printf("✗ Verification failed: %v\n", err)
				return err
			}

			fmt.Printf("✓ Verification passed\n")
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: ~/.IronClaw/memory)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "path to config file")

	return cmd
}

func newMemoryStatsCmd() *cobra.Command {
	var outputDir string
	var configPath string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show memory storage statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Load config
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Determine output directory
			if outputDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home dir: %w", err)
				}
				outputDir = filepath.Join(home, ".IronClaw", "memory")
			}

			// Open database
			db, err := store.Open(cfg.Store.Path)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer db.Close()

			// Create embeddings DB
			memCfg := memory.MemoryConfig{
				VectorDimension: 1536,
			}
			embeddingsDB := memory.NewEmbeddingsDB(db, memCfg)

			// Create file store
			embedder := &memory.NoopEmbedding{}
			fileStore := memory.NewFileStore(outputDir, embeddingsDB, embedder, memCfg)

			// Create migrator
			migrator := memory.NewMigrator(db, fileStore, embeddingsDB, embedder)

			// Get stats
			stats, err := migrator.GetStats(ctx)
			if err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}

			// Print stats
			fmt.Println("Memory Storage Statistics")
			fmt.Println("=========================")
			fmt.Printf("SQLite facts:      %d\n", stats["sqlite_facts"])
			fmt.Printf("File-based facts:  %d\n", stats["file_facts"])
			fmt.Printf("Storage directory: %s\n", stats["storage_dir"])
			fmt.Printf("Storage size:      %.2f MB\n", stats["storage_size_mb"])

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: ~/.IronClaw/memory)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/ironclaw.yaml", "path to config file")

	return cmd
}
