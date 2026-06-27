package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/retrievaleval"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
	"github.com/spf13/cobra"
)

func newMemoryBenchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bench",
		Short: "Run a self-contained retrieval benchmark",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			path := filepath.Join(os.TempDir(), fmt.Sprintf("daimon-bench-%d.db", os.Getpid()))
			defer removeSQLiteFiles(path)

			db, err := store.Open(path)
			if err != nil {
				return fmt.Errorf("open bench database: %w", err)
			}
			defer func() { _ = db.Close() }()

			ws := world.NewStore(db.DB)
			qs, err := retrievaleval.SeedCorpus(ctx, ws)
			if err != nil {
				return fmt.Errorf("seed corpus: %w", err)
			}

			report, err := retrievaleval.Run(ctx, ws.Retrieve, qs, 13)
			if err != nil {
				return fmt.Errorf("run retrieval benchmark: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			_, _ = fmt.Fprintln(w, "SYSTEM\tRECALL\tPRECISION\tF1\tMRR\tTOKENS/Q\tSTORE\t")
			_, _ = fmt.Fprintf(w, "lexical\t%.3f\t%.3f\t%.3f\t%.3f\t%.1f\t%d\t\n",
				report.Recall, report.Precision, report.F1, report.MRR, report.TokensPerQuery, report.StoreSize)
			if err := w.Flush(); err != nil {
				return fmt.Errorf("flush benchmark output: %w", err)
			}
			return nil
		},
	}
}

func removeSQLiteFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
