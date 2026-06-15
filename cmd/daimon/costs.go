package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/economy"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

// newCostsCmd builds the `daimon costs` command: a read-only report of what the
// agent spent over a period (DAIMON_BLUEPRINT.md §4.11). It aggregates the
// per-episode token ledger by model and converts to dollars using the model
// prices in config; models with no configured price are reported in tokens only.
func newCostsCmd() *cobra.Command {
	var configPath string
	var devMode bool
	var since time.Duration

	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Report what the agent spent (token usage + cost)",
		Long: "Aggregate the per-episode cost ledger by model over a period and " +
			"convert to dollars using the model prices in config (economy.prices). " +
			"Models without a configured price are shown in tokens only. Read-only.",
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

			// since <= 0 ⇒ all time; otherwise the cutoff is now - since.
			var cutoff int64
			periodLabel := "all time"
			if since > 0 {
				cutoff = time.Now().Add(-since).Unix()
				periodLabel = "last " + since.String()
			}

			rows, err := economy.NewStore(db.DB).ByModelSince(ctx, cutoff)
			if err != nil {
				return fmt.Errorf("aggregate costs: %w", err)
			}
			if len(rows) == 0 {
				fmt.Printf("No cost recorded (%s).\n", periodLabel)
				return nil
			}

			prices := pricesFromConfig(cfg.Economy)

			fmt.Printf("Cost report — %s\n\n", periodLabel)
			// Trailing tab on every row terminates the COST cell so tabwriter aligns
			// it (the final, newline-terminated cell is otherwise left unpadded).
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			_, _ = fmt.Fprintln(w, "MODEL\tEPISODES\tINPUT\tOUTPUT\tCACHE_R\tCACHE_W\tCOST\t")

			var grand economy.Totals
			var grandUSD float64
			anyUnpriced := false
			for _, r := range rows {
				grand.Episodes += r.Episodes
				grand.InputTokens += r.InputTokens
				grand.OutputTokens += r.OutputTokens
				grand.CacheReadTokens += r.CacheReadTokens
				grand.CacheCreationTokens += r.CacheCreationTokens

				costCol := "—"
				if usd, priced := prices.CostUSD(r.Model, r.Totals); priced {
					grandUSD += usd
					costCol = fmt.Sprintf("$%.4f", usd)
				} else {
					anyUnpriced = true
				}
				model := r.Model
				if model == "" {
					model = "(unknown)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t\n",
					model, r.Episodes, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheCreationTokens, costCol)
			}
			_, _ = fmt.Fprintf(w, "TOTAL\t%d\t%d\t%d\t%d\t%d\t$%.4f\t\n",
				grand.Episodes, grand.InputTokens, grand.OutputTokens, grand.CacheReadTokens, grand.CacheCreationTokens, grandUSD)
			_ = w.Flush()

			if anyUnpriced {
				fmt.Println("\nNote: some models have no configured price (cost shown as —);" +
					" the TOTAL cost covers priced models only. Set economy.prices in config to price them.")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/daimon.yaml", "config file")
	cmd.Flags().DurationVar(&since, "since", 30*24*time.Hour, "report window (e.g. 720h, 168h); 0 = all time")
	return cmd
}

// pricesFromConfig converts the operator's configured model prices into the
// economy package's pricing table.
func pricesFromConfig(cfg config.EconomyConfig) economy.Prices {
	prices := economy.Prices{}
	for model, mp := range cfg.Prices {
		prices[model] = economy.Price{
			InputPerMTok:         mp.InputPerMTok,
			OutputPerMTok:        mp.OutputPerMTok,
			CacheReadPerMTok:     mp.CacheReadPerMTok,
			CacheCreationPerMTok: mp.CacheCreationPerMTok,
		}
	}
	return prices
}
