package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tuichannel "github.com/Forest-Isle/IronClaw/internal/channel/tui"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/gateway"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
	"github.com/spf13/cobra"
)

func newTUICmd() *cobra.Command {
	var tuiCfgPath string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Start IronClaw with an interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(tuiCfgPath)
		},
	}
	cmd.Flags().StringVarP(&tuiCfgPath, "config", "c", "configs/ironclaw.yaml", "path to config file")
	return cmd
}

func runTUI(configPath string) error {
	// Redirect slog to a file so it doesn't interfere with Bubble Tea's raw mode.
	logPath := tuiLogPath()
	if err := setupTUILogging(logPath); err != nil {
		return fmt.Errorf("setup TUI logging: %w", err)
	}

	slog.Info("loading config", "path", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := userdir.Apply(cfg); err != nil {
		return fmt.Errorf("load user config: %w", err)
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return fmt.Errorf("init gateway: %w", err)
	}

	// Create TUI adapter
	tuiAdapter := tuichannel.New(cfg.Agent.Mode, version)
	if cfg.TUI.AutoApprove {
		tuiAdapter.SetAutoApprove(true)
	}
	if cfg.Agent.Cognitive.ApprovalTimeoutSeconds > 0 {
		tuiAdapter.SetApprovalTimeout(
			time.Duration(cfg.Agent.Cognitive.ApprovalTimeoutSeconds) * time.Second,
		)
	}
	gw.AddChannel(tuiAdapter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := gw.Start(ctx); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	// Handle signals for graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		cancel()
		gw.Stop(context.Background()) //nolint:errcheck
	}()

	// Run Bubble Tea — blocks until user quits
	if err := tuiAdapter.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	cancel()
	return gw.Stop(context.Background())
}

// tuiLogPath returns ~/.ironclaw/tui.log.
func tuiLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "tui.log"
	}
	dir := filepath.Join(home, ".IronClaw")
	os.MkdirAll(dir, 0755) //nolint:errcheck
	return filepath.Join(dir, "tui.log")
}

// setupTUILogging redirects slog output to a file.
func setupTUILogging(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return nil
}
