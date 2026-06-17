package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/spf13/cobra"
)

func newTrustCmd() *cobra.Command {
	var configPath string
	var devMode bool

	cmd := &cobra.Command{
		Use:   "trust",
		Short: "List autonomy/trust levels or revoke autonomy via a correction",
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to config file (auto-discovered if empty)")
	cmd.PersistentFlags().BoolVar(&devMode, "dev", false, "use configs/daimon.yaml in dev mode")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List autonomy/trust levels",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrustList(cmd.Context(), configPath, devMode)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "correct <class> <context-key>",
		Short: "Revoke autonomy via a correction",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrustCorrect(cmd.Context(), configPath, devMode, args[0], args[1])
		},
	})
	return cmd
}

func runTrustList(ctx context.Context, configPath string, devMode bool) error {
	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	entries, err := st.ListTrust(ctx)
	if err != nil {
		return fmt.Errorf("list trust ledger: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("No trust ledger entries.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "CLASS\tCONTEXT\tLEVEL\tATTEMPTS\tVERIFIED\tCORRECTED")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\n",
			entry.ActionClass, entry.ContextKey, action.Level(entry.Level).String(), entry.Attempts, entry.VerifiedOK, entry.Corrected)
	}
	return w.Flush()
}

func runTrustCorrect(ctx context.Context, configPath string, devMode bool, classArg string, contextArg string) error {
	class, err := action.ParseClass(strings.TrimSpace(classArg))
	if err != nil {
		return err
	}
	contextKey := strings.TrimSpace(contextArg)
	if contextKey == "" {
		return errors.New("context key is required")
	}

	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := st.RecordCorrection(ctx, class, contextKey); err != nil {
		return fmt.Errorf("record correction: %w", err)
	}
	lvl, err := st.TrustLevel(ctx, class, contextKey)
	if err != nil {
		fmt.Printf("Corrected: %s actions for %q; future auto-promotion frozen.\n", class, contextKey)
		return nil
	}
	fmt.Printf("Corrected: %s actions for %q demoted to %s; future auto-promotion frozen.\n", class, contextKey, lvl)
	return nil
}
