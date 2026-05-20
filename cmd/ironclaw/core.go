package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/core"
	"github.com/Forest-Isle/IronClaw/internal/core/boot"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
	"github.com/spf13/cobra"
)

// newCoreCmd builds the `ironclaw core` subcommand group, the entry-point
// to the new clean agentic runtime defined in internal/core.
func newCoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "core",
		Short: "Clean agentic runtime (new architecture)",
		Long: `The "core" subcommand runs the rebuilt agentic loop in
internal/core, bypassing the legacy Gateway. Useful for benchmarking,
single-shot completions, and as a cleaner mental model of the system.`,
	}
	cmd.AddCommand(newCoreRunCmd())
	cmd.AddCommand(newCoreToolsCmd())
	return cmd
}

func newCoreRunCmd() *cobra.Command {
	var (
		cfgFile  string
		model    string
		system   string
		verbose  bool
		jsonEvts bool
		maxTurns int
		memory   string
	)
	cmd := &cobra.Command{
		Use:   "run [prompt]",
		Short: "Run a single prompt through the core agent loop",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := userdir.Apply(cfg); err != nil {
				return fmt.Errorf("apply userdir: %w", err)
			}

			prompt := joinArgs(args)

			var sink core.EventSink = core.NullSink
			if verbose || jsonEvts {
				sink = consoleSink(jsonEvts)
			}

			opts := boot.Options{
				Cfg:        cfg,
				Sink:       sink,
				Model:      model,
				System:     system,
				MaxTurns:   maxTurns,
				MemoryPath: memory,
			}

			out, stop, err := boot.Run(context.Background(), opts, prompt)
			if err != nil {
				return err
			}
			fmt.Println(out)
			if verbose {
				fmt.Fprintf(os.Stderr, "\n[stop=%s]\n", stop)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&cfgFile, "config", "c", "configs/ironclaw.yaml", "config file")
	cmd.Flags().StringVar(&model, "model", "", "override llm.model")
	cmd.Flags().StringVar(&system, "system", "", "override agent.system_prompt")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "stream events to stderr")
	cmd.Flags().BoolVar(&jsonEvts, "json", false, "stream events as JSON to stderr")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 0, "override max iteration count (0 = default)")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "persist conversation to NDJSON file")
	return cmd
}

func newCoreToolsCmd() *cobra.Command {
	var cfgFile string
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List tools registered in the core agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := userdir.Apply(cfg); err != nil {
				return fmt.Errorf("apply userdir: %w", err)
			}
			ag, err := boot.New(boot.Options{Cfg: cfg})
			if err != nil {
				return err
			}
			_ = ag
			// The core registry isn't directly exposed by the agent; rebuild
			// it from the same config to enumerate.
			tools := boot.ListToolSchemas(cfg)
			for _, t := range tools {
				fmt.Printf("%-22s  %s\n", t.Name, t.Description)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&cfgFile, "config", "c", "configs/ironclaw.yaml", "config file")
	return cmd
}

// joinArgs concatenates positional args with single spaces.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

// consoleSink builds a sink that writes events to stderr. When asJSON is
// true, each event is rendered as a single line of JSON.
func consoleSink(asJSON bool) core.EventSink {
	if asJSON {
		enc := json.NewEncoder(os.Stderr)
		return core.EventSinkFunc(func(e core.Event) {
			_ = enc.Encode(e)
		})
	}
	return core.EventSinkFunc(func(e core.Event) {
		fmt.Fprintf(os.Stderr, "[%s] turn=%d\n", e.Kind, e.Turn)
	})
}
