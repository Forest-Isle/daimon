package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/gateway"
	"github.com/spf13/cobra"
)

var (
	cfgPath string
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:   "ironclaw",
		Short: "IronClaw — Local-first AI Agent Runtime",
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the IronClaw agent runtime",
		RunE:  runStart,
	}
	startCmd.Flags().StringVarP(&cfgPath, "config", "c", "configs/ironclaw.yaml", "path to config file")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ironclaw %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}

	root.AddCommand(startCmd, versionCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runStart(cmd *cobra.Command, args []string) error {
	// Setup logging
	setupLogging("info")

	slog.Info("loading config", "path", cfgPath)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	setupLogging(cfg.Log.Level)

	gw, err := gateway.New(cfg)
	if err != nil {
		return fmt.Errorf("init gateway: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := gw.Start(ctx); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	slog.Info("ironclaw is running, press Ctrl+C to stop")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down...")
	cancel()
	return gw.Stop(context.Background())
}

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
