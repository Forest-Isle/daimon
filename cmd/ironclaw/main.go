package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/Forest-Isle/IronClaw/internal/channel/telegram"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/gateway"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
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

	root.AddCommand(startCmd, versionCmd, newTUICmd(), newSkillCmd(), newMemoryCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// skillsDir returns the path to ~/.IronClaw/skills/.
func skillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".IronClaw", "skills")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}
	return dir, nil
}

// resolveWorkdir returns the workdir for clawhub CLI.
// If dir is empty, defaults to ~/.IronClaw.
func resolveWorkdir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".IronClaw"), nil
}

// newSkillCmd builds the `ironclaw skill` subcommand group.
func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage IronClaw skills",
	}
	cmd.AddCommand(
		newSkillListCmd(),
		newSkillSearchCmd(),
		newSkillInstallCmd(),
		newSkillUpdateCmd(),
		newSkillRemoveCmd(),
	)
	return cmd
}

func newSkillListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := skillsDir()
			if err != nil {
				return err
			}

			mgr := skill.New()
			if err := mgr.LoadBuiltin(); err != nil {
				return fmt.Errorf("load builtin skills: %w", err)
			}
			if err := mgr.LoadDir(dir); err != nil {
				return fmt.Errorf("load skills: %w", err)
			}

			skills := mgr.All()
			if len(skills) == 0 {
				fmt.Println("No skills installed. Use `ironclaw skill install <slug>` to install from ClawHub.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tVERSION\tAUTHOR\tTAGS\tDESCRIPTION")
			for _, s := range skills {
				tags := strings.Join(s.Tags, ", ")
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					s.Name, s.Version, s.Author, tags, truncate(s.Description, 50))
			}
			return w.Flush()
		},
	}
}

func newSkillSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search ClawHub for skills",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			c := exec.Command("clawhub", "search", query,
				"--limit", fmt.Sprintf("%d", limit), "--no-input")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("search failed (requires clawhub CLI: npm install -g clawhub): %w", err)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "max results")
	return cmd
}

func newSkillInstallCmd() *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:   "install <slug>",
		Short: "Install a skill from ClawHub",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			c := exec.Command("clawhub", "install", args[0],
				"--workdir", dir, "--no-input")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("install failed (requires clawhub CLI: npm install -g clawhub): %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "skills working directory (default: ~/.IronClaw)")
	return cmd
}

func newSkillUpdateCmd() *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:   "update [slug]",
		Short: "Update installed skills from ClawHub",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveWorkdir(workdir)
			if err != nil {
				return err
			}
			cmdArgs := []string{"update"}
			if len(args) > 0 {
				cmdArgs = append(cmdArgs, args[0])
			} else {
				cmdArgs = append(cmdArgs, "--all")
			}
			cmdArgs = append(cmdArgs, "--workdir", dir, "--no-input")
			c := exec.Command("clawhub", cmdArgs...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("update failed (requires clawhub CLI: npm install -g clawhub): %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "skills working directory (default: ~/.IronClaw)")
	return cmd
}

func newSkillRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := skillsDir()
			if err != nil {
				return err
			}

			name := args[0]
			skillPath := filepath.Join(dir, name)

			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				return fmt.Errorf("skill %q not found in %s", name, dir)
			}

			fmt.Printf("Remove skill %q from %s? [y/N] ", name, dir)
			var answer string
			_, _ = fmt.Scanln(&answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}

			if err := os.RemoveAll(skillPath); err != nil {
				return fmt.Errorf("remove skill: %w", err)
			}
			fmt.Printf("Skill %q removed.\n", name)
			return nil
		},
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
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

	if err := userdir.Apply(cfg); err != nil {
		return fmt.Errorf("load user config: %w", err)
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return fmt.Errorf("init gateway: %w", err)
	}

	// Create and register Telegram channel
	tg, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUserIDs)
	if err != nil {
		return fmt.Errorf("init telegram: %w", err)
	}
	if cfg.Agent.Cognitive.ApprovalTimeoutSeconds > 0 {
		tg.SetApprovalTimeout(cfg.Agent.Cognitive.ApprovalTimeoutSeconds)
	}
	gw.AddChannel(tg)

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
