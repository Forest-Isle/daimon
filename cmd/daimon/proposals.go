package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

// newProposalsCmd builds the `daimon proposals` command: it lists the pending
// anticipatory proposals the sleep cycle has queued (DAIMON_BLUEPRINT.md §4.9).
// Read-only inspection of the queue; delivery and accept/dismiss UX land later.
func newProposalsCmd() *cobra.Command {
	var configPath string
	var devMode bool

	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List pending anticipatory proposals",
		Long: "Show the proposals the sleep cycle has queued from upcoming " +
			"commitments — concrete next actions you will likely need but have " +
			"not yet asked for. Read-only.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

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

			pending, err := proposals.NewStore(db.DB).ListPending(ctx, time.Now().Unix())
			if err != nil {
				return fmt.Errorf("list pending proposals: %w", err)
			}
			if len(pending) == 0 {
				fmt.Println("No pending proposals.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "URGENCY\tTITLE\tACTION PLAN")
			for _, p := range pending {
				_, _ = fmt.Fprintf(w, "%d\t%s\t%s\n", p.Urgency, p.Title, p.ActionPlan)
			}
			_ = w.Flush()
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "config file")
	return cmd
}
