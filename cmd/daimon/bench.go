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

			lexicalParaphrase, semanticParaphrase, err := runSemanticBench(ctx)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(os.Stdout)
			sw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			_, _ = fmt.Fprintln(sw, "SYSTEM\tRECALL\tPRECISION\tF1\tMRR\tTOKENS/Q\tSTORE\t")
			_, _ = fmt.Fprintf(sw, "lexical\t%.3f\t%.3f\t%.3f\t%.3f\t%.1f\t%d\t\n",
				lexicalParaphrase.Recall, lexicalParaphrase.Precision, lexicalParaphrase.F1,
				lexicalParaphrase.MRR, lexicalParaphrase.TokensPerQuery, lexicalParaphrase.StoreSize)
			_, _ = fmt.Fprintf(sw, "+semantic\t%.3f\t%.3f\t%.3f\t%.3f\t%.1f\t%d\t\n",
				semanticParaphrase.Recall, semanticParaphrase.Precision, semanticParaphrase.F1,
				semanticParaphrase.MRR, semanticParaphrase.TokensPerQuery, semanticParaphrase.StoreSize)
			if err := sw.Flush(); err != nil {
				return fmt.Errorf("flush semantic benchmark output: %w", err)
			}
			return nil
		},
	}
}

func runSemanticBench(ctx context.Context) (retrievaleval.SystemReport, retrievaleval.SystemReport, error) {
	lexicalStore, lexicalCleanup, err := newParaphraseBenchStore("lexical")
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, err
	}
	defer lexicalCleanup()
	lexicalQueries, err := retrievaleval.SeedParaphraseCorpus(ctx, lexicalStore)
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, fmt.Errorf("seed lexical paraphrase corpus: %w", err)
	}
	lexicalReport, err := retrievaleval.Run(ctx, lexicalStore.Retrieve, lexicalQueries, 6)
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, fmt.Errorf("run lexical paraphrase benchmark: %w", err)
	}

	semanticStore, semanticCleanup, err := newParaphraseBenchStore("semantic")
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, err
	}
	defer semanticCleanup()
	semanticQueries, err := retrievaleval.SeedParaphraseCorpus(ctx, semanticStore)
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, fmt.Errorf("seed semantic paraphrase corpus: %w", err)
	}
	embedder := retrievaleval.ConceptEmbedder{}
	if err := embedBenchJournal(ctx, semanticStore, embedder); err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, err
	}
	semanticStore.SetEmbedder(embedder)
	semanticReport, err := retrievaleval.Run(ctx, semanticStore.Retrieve, semanticQueries, 6)
	if err != nil {
		return retrievaleval.SystemReport{}, retrievaleval.SystemReport{}, fmt.Errorf("run semantic paraphrase benchmark: %w", err)
	}
	return lexicalReport, semanticReport, nil
}

func newParaphraseBenchStore(suffix string) (*world.Store, func(), error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("daimon-bench-%s-%d.db", suffix, os.Getpid()))
	removeSQLiteFiles(path)
	db, err := store.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s paraphrase database: %w", suffix, err)
	}
	cleanup := func() {
		_ = db.Close()
		removeSQLiteFiles(path)
	}
	return world.NewStore(db.DB), cleanup, nil
}

func embedBenchJournal(ctx context.Context, ws *world.Store, embedder retrievaleval.ConceptEmbedder) error {
	entries, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		return fmt.Errorf("list semantic bench journal: %w", err)
	}
	for _, entry := range entries {
		vec, err := embedder.Embed(ctx, entry.Summary+" "+entry.Detail)
		if err != nil {
			return fmt.Errorf("embed semantic bench journal %s: %w", entry.ID, err)
		}
		if err := ws.SetJournalEmbedding(ctx, entry.ID, vec); err != nil {
			return fmt.Errorf("set semantic bench journal embedding %s: %w", entry.ID, err)
		}
	}
	return nil
}

func removeSQLiteFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
