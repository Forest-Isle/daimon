package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newHoldsCmd() *cobra.Command {
	var configPath string
	var devMode bool

	cmd := &cobra.Command{
		Use:   "holds",
		Short: "List or recall pending compensable action holds",
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to config file (auto-discovered if empty)")
	cmd.PersistentFlags().BoolVar(&devMode, "dev", false, "use configs/daimon.yaml in dev mode")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List pending holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHoldsList(cmd.Context(), configPath, devMode)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "recall <id>",
		Short: "Recall a pending hold",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHoldRecall(cmd.Context(), configPath, devMode, strings.TrimSpace(args[0]))
		},
	})
	return cmd
}

func runHoldsList(ctx context.Context, configPath string, devMode bool) error {
	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	holds, err := st.ListPendingHolds(ctx)
	if err != nil {
		return fmt.Errorf("list pending holds: %w", err)
	}
	if len(holds) == 0 {
		fmt.Println("No pending holds.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tTOOL\tEXECUTE_AT")
	for _, h := range holds {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", h.ID, h.ToolName, h.ExecuteAt)
	}
	return w.Flush()
}

func runHoldRecall(ctx context.Context, configPath string, devMode bool, id string) error {
	if id == "" {
		return errors.New("hold id is required")
	}
	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := st.RecallHold(ctx, id); err != nil {
		return fmt.Errorf("recall hold: %w", err)
	}
	fmt.Println("Recalled.")
	return nil
}
