package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mcp"
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
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			srv := mcp.NewServer(mcp.WithDeps(mcp.ServerDeps{
				// Dependencies will be nil until gateway wires them;
				// MCP serve standalone exposes tools without deps for now.
			}))

			ctx := context.Background()

			if httpAddr != "" {
				slog.Info("starting MCP server (HTTP)", "addr", httpAddr)
				return srv.ServeHTTP(ctx, httpAddr)
			}

			slog.Info("starting MCP server (stdio)")
			_ = cfg // config available for future use (e.g., tool registration from config)
			return srv.ServeStdio(ctx)
		},
	}
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "configs/daimon.yaml", "path to config file")
	serveCmd.Flags().StringVar(&httpAddr, "http", "", "HTTP address (e.g., :8089); if empty, uses stdio")

	cmd.AddCommand(serveCmd)
	return cmd
}
