package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/economy"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
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

			st := economy.NewStore(db.DB)
			rows, err := st.ByModelSince(ctx, cutoff)
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

			// By activity class — where the spend goes (chat vs each autonomous
			// trigger). Each model sub-row is priced at its own rate then folded per
			// class, so a class that spans models still gets a correct dollar figure.
			classRows, err := st.ByClassModelSince(ctx, cutoff)
			if err != nil {
				return fmt.Errorf("aggregate costs by class: %w", err)
			}
			folded := foldClassCosts(classRows, prices)
			fmt.Printf("\nBy activity class — %s\n\n", periodLabel)
			cw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			_, _ = fmt.Fprintln(cw, "CLASS\tEPISODES\tINPUT\tOUTPUT\tCACHE_R\tCACHE_W\tCOST\t")
			classUnpriced := false
			for _, c := range folded {
				class := c.class
				if class == "" {
					class = "(unclassified)"
				}
				costCol := fmt.Sprintf("$%.4f", c.usd)
				if c.anyUnpriced {
					costCol = "—"
					classUnpriced = true
				}
				_, _ = fmt.Fprintf(cw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t\n",
					class, c.totals.Episodes, c.totals.InputTokens, c.totals.OutputTokens, c.totals.CacheReadTokens, c.totals.CacheCreationTokens, costCol)
			}
			_ = cw.Flush()
			if classUnpriced {
				fmt.Println("\nNote: a class with any unpriced model shows cost as — (incomplete);" +
					" price every model in economy.prices for a full per-class figure.")
			}

			// ROI by activity class — what the spend bought (blueprint §4.11). The
			// value proxy is the clean-outcome rate: episodes that closed cleanly with
			// no tool failure and every governed action verified (the J11+J12 signals).
			// A class burning tokens but mostly degrading is poor value; CLEAN/$ ranks
			// classes by clean outcomes delivered per dollar. Read-only: it joins the
			// cost ledger to each episode's outcome quality in the world journal.
			episodeCosts, err := st.EpisodeClassCostSince(ctx, cutoff)
			if err != nil {
				return fmt.Errorf("episode costs for ROI: %w", err)
			}
			if len(episodeCosts) > 0 {
				ids := make([]string, 0, len(episodeCosts))
				for _, e := range episodeCosts {
					if e.EpisodeID != "" {
						ids = append(ids, e.EpisodeID)
					}
				}
				quality, err := world.NewStore(db.DB).OutcomeQualityForEpisodes(ctx, ids)
				if err != nil {
					return fmt.Errorf("outcome quality for ROI: %w", err)
				}
				roi := foldROI(folded, episodeCosts, quality)
				fmt.Printf("\nROI by activity class — %s\n\n", periodLabel)
				rw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
				_, _ = fmt.Fprintln(rw, "CLASS\tEPISODES\tCLEAN\tCLEAN%\tCOST\tCLEAN/$\t")
				for _, r := range roi {
					class := r.class
					if class == "" {
						class = "(unclassified)"
					}
					cleanPct := "—"
					if r.episodes > 0 {
						cleanPct = fmt.Sprintf("%.0f%%", 100*float64(r.clean)/float64(r.episodes))
					}
					costCol, perDollar := "—", "—"
					if r.priced {
						costCol = fmt.Sprintf("$%.4f", r.usd)
						if r.usd > 0 {
							perDollar = fmt.Sprintf("%.2f", float64(r.clean)/r.usd)
						}
					}
					_, _ = fmt.Fprintf(rw, "%s\t%d\t%d\t%s\t%s\t%s\t\n",
						class, r.episodes, r.clean, cleanPct, costCol, perDollar)
				}
				_ = rw.Flush()
				fmt.Println("\nNote: value proxy = clean-outcome rate (no tool failure, all governed" +
					" actions verified). CLEAN/$ = clean episodes per dollar; — when the class" +
					" has an unpriced model or zero cost.")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "configs/daimon.yaml", "config file")
	cmd.Flags().DurationVar(&since, "since", 30*24*time.Hour, "report window (e.g. 720h, 168h); 0 = all time")
	return cmd
}

// classCostRow is one activity class's folded aggregate for the by-class table:
// summed tokens plus the dollar cost of all its model sub-rows. anyUnpriced is
// set when at least one of the class's models has no configured price, so its
// dollar figure would be incomplete (reported as "—" rather than understated).
type classCostRow struct {
	class       string
	totals      economy.Totals
	usd         float64
	anyUnpriced bool
}

// foldClassCosts groups per-(class,model) rows by class, summing tokens and
// pricing each model sub-row at its own rate. The result is ordered by output
// tokens descending, then class ascending — deterministic regardless of the map
// iteration order.
func foldClassCosts(rows []economy.ClassModelTotals, prices economy.Prices) []classCostRow {
	byClass := map[string]*classCostRow{}
	order := []string{}
	for _, r := range rows {
		c, ok := byClass[r.Class]
		if !ok {
			c = &classCostRow{class: r.Class}
			byClass[r.Class] = c
			order = append(order, r.Class)
		}
		c.totals.Episodes += r.Episodes
		c.totals.InputTokens += r.InputTokens
		c.totals.OutputTokens += r.OutputTokens
		c.totals.CacheReadTokens += r.CacheReadTokens
		c.totals.CacheCreationTokens += r.CacheCreationTokens
		if usd, priced := prices.CostUSD(r.Model, r.Totals); priced {
			c.usd += usd
		} else {
			c.anyUnpriced = true
		}
	}
	out := make([]classCostRow, 0, len(order))
	for _, cls := range order {
		out = append(out, *byClass[cls])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].totals.OutputTokens != out[j].totals.OutputTokens {
			return out[i].totals.OutputTokens > out[j].totals.OutputTokens
		}
		return out[i].class < out[j].class
	})
	return out
}

// roiClassRow is one activity class's ROI line: how many of its episodes closed
// cleanly, against the dollar cost of the class. priced is false when the class has
// any unpriced model (its cost — and so CLEAN/$ — is incomplete).
type roiClassRow struct {
	class    string
	episodes int
	clean    int
	usd      float64
	priced   bool
}

// foldROI overlays per-episode outcome quality onto the already-folded per-class
// cost rows. The folded rows are the spine: they carry the authoritative per-class
// episode count and dollar cost (and order — output desc, class asc), so foldROI
// keeps that order and only adds the clean-outcome count, derived by bucketing each
// episode's class against its quality. An episode with no recorded quality (no
// outcome row) counts toward episodes but not clean. Pure: no I/O.
func foldROI(folded []classCostRow, episodeCosts []economy.EpisodeClassCost, quality map[string]world.OutcomeQuality) []roiClassRow {
	cleanByClass := map[string]int{}
	for _, e := range episodeCosts {
		// Presence matters: OutcomeClean is the zero value, so an episode with no
		// recorded quality (absent from the map) must NOT be counted as clean.
		if q, ok := quality[e.EpisodeID]; ok && q == world.OutcomeClean {
			cleanByClass[e.Class]++
		}
	}
	out := make([]roiClassRow, 0, len(folded))
	for _, c := range folded {
		out = append(out, roiClassRow{
			class:    c.class,
			episodes: c.totals.Episodes,
			clean:    cleanByClass[c.class],
			usd:      c.usd,
			priced:   !c.anyUnpriced,
		})
	}
	return out
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
