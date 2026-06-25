package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mcp"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/skill"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	var (
		configPath string
		httpAddr   string
	)

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start Daimon as an MCP server (stdio or HTTP)",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			var memStore memory.Store
			var embedder memory.EmbeddingProvider = &memory.NoopEmbedding{}
			if cfg.Memory.OpenAIAPIKey != "" {
				embedder = memory.NewCachedEmbedder(memory.NewOpenAIEmbeddingWithURL(cfg.Memory.OpenAIAPIKey, cfg.Memory.EmbeddingModel, cfg.Memory.EmbeddingBaseURL))
			}
			memCfg := memory.MemoryConfig{
				FactExtraction:      cfg.Memory.FactExtraction,
				SimilarityThreshold: cfg.Memory.SimilarityThreshold,
				BM25Weight:          cfg.Memory.BM25Weight,
				VectorWeight:        cfg.Memory.VectorWeight,
				EmbeddingDimension:  cfg.Memory.VectorDimension,
			}
			storageDir := cfg.Memory.StorageDir
			if storageDir == "" {
				storageDir = filepath.Join(appdir.BaseDir(), "memory")
			} else if strings.HasPrefix(storageDir, "~/") {
				storageDir = filepath.Join(filepath.Dir(appdir.BaseDir()), storageDir[2:])
			}
			if fileStore, err := memory.NewFileMemoryStore(storageDir, db.DB, embedder, memCfg); err != nil {
				slog.Warn("mcp server: create memory store failed", "dir", storageDir, "err", err)
			} else {
				memStore = fileStore
			}

			skillMgr := skill.New()
			if err := skillMgr.LoadBuiltin(); err != nil {
				slog.Error("skill: load builtin skills failed", "err", err)
			}
			skillsDir := filepath.Join(appdir.BaseDir(), "skills")
			if err := skillMgr.LoadDir(skillsDir); err != nil {
				slog.Warn("skill: load skills dir failed", "dir", skillsDir, "err", err)
			}

			srv := mcp.NewServer()
			mcp.RegisterDefaultTools(srv, mcp.ServerDeps{MemoryStore: memStore, SkillMgr: skillMgr})

			ctx := context.Background()

			if httpAddr != "" {
				slog.Info("starting MCP server (HTTP)", "addr", httpAddr)
				return srv.ServeHTTP(ctx, httpAddr)
			}

			slog.Info("starting MCP server (stdio)")
			return srv.ServeStdio(ctx)
		},
	}
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config file")
	serveCmd.Flags().StringVar(&httpAddr, "http", "", "HTTP address (e.g., :8089); if empty, uses stdio")

	cmd.AddCommand(serveCmd)
	return cmd
}
