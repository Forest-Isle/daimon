package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/replay"
	"github.com/spf13/cobra"
)

// newReplayCmd builds the `daimon replay` command: it reads the recorded replay
// journals and prints an offline health report over the sessions they captured.
// It never re-runs anything or contacts a provider.
func newReplayCmd() *cobra.Command {
	var replaysDir string
	var sessionID string
	var against string
	var judgeModel string
	var maxExchanges int

	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Analyze recorded agent replay journals (offline)",
		Long: "Read the JSONL replay journals under the replays directory and print " +
			"an offline health report (exchanges, tool failures, salvaged episodes, " +
			"abnormal stops) over the recorded sessions.\n\n" +
			"With --against <config>, re-run each recorded exchange through that " +
			"configuration's provider and have a judge compare the new responses to " +
			"the recorded baselines, printing a quality/cost/regression report. This " +
			"makes real provider calls (it costs tokens) but never executes tools or " +
			"writes the world — it is a dry-run re-scoring.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := replaysDir
			if dir == "" {
				dir = filepath.Join(appdir.BaseDir(), "replays")
			}

			sessions, skipped, err := replay.LoadDir(dir)
			if err != nil {
				return fmt.Errorf("load replays: %w", err)
			}
			if sessionID != "" {
				sessions = filterSessions(sessions, sessionID)
			}
			if len(sessions) == 0 {
				fmt.Printf("No recorded sessions found in %s\n", dir)
				return nil
			}

			if against != "" {
				return runReplayAgainst(cmd.Context(), dir, against, judgeModel, maxExchanges, sessions)
			}

			rep := replay.Analyze(sessions, skipped)
			printReplayReport(dir, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&replaysDir, "replays", "", "replay journals directory (default: ~/.daimon/replays)")
	cmd.Flags().StringVar(&sessionID, "session", "", "limit the report to a single session id")
	cmd.Flags().StringVar(&against, "against", "", "candidate config to re-run recorded exchanges against (offline re-scoring; costs tokens)")
	cmd.Flags().StringVar(&judgeModel, "judge-model", "", "model the judge uses (default: the candidate config's model)")
	cmd.Flags().IntVar(&maxExchanges, "max-exchanges", 20, "cap the number of exchanges re-scored (0 = no cap)")
	return cmd
}

// judgeProvider adapts an mind.Provider to replay.Judge: a single-user-message
// completion at the judge model. The judge is kept stable and cheap (a small
// model) so the verdict reflects the candidate's quality, not the judge's.
type judgeProvider struct {
	provider mind.Provider
	model    string
}

func (j judgeProvider) Complete(ctx context.Context, system, user string) (string, error) {
	resp, err := j.provider.Complete(ctx, mind.CompletionRequest{
		Model:     j.model,
		System:    system,
		Messages:  []mind.CompletionMessage{{Role: "user", Content: user}},
		MaxTokens: 512,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// runReplayAgainst re-scores the recorded sessions against a candidate config and
// prints the comparison report. The candidate provider drives the re-run; the
// judge (same provider, judge model) compares each new response to its baseline.
func runReplayAgainst(ctx context.Context, dir, configPath, judgeModel string, maxExchanges int, sessions []replay.Session) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load candidate config: %w", err)
	}
	provider := mind.NewProviderFromConfig(cfg.LLM)
	if judgeModel == "" {
		judgeModel = cfg.LLM.Model
	}

	rep, err := replay.Rescore(ctx, sessions, provider, judgeProvider{provider: provider, model: judgeModel},
		replay.RescoreOptions{Model: cfg.LLM.Model, MaxTokens: cfg.LLM.MaxTokens, MaxExchanges: maxExchanges}, nil)
	if err != nil {
		return fmt.Errorf("rescore: %w", err)
	}
	printRescoreReport(dir, configPath, cfg.LLM.Model, rep, provider)
	return nil
}

func printRescoreReport(dir, configPath, model string, rep replay.RescoreReport, provider mind.Provider) {
	fmt.Printf("Replay re-score — recorded=%s against=%s model=%s\n", dir, configPath, model)
	fmt.Printf("compared=%d avg_score=%d regressions=%d errors=%d skipped=%d capped=%t\n",
		rep.Compared, rep.AvgScore, rep.Regressions, rep.Errors, rep.Skipped, rep.Capped)
	// Cost: cumulative tokens spent by this run (candidate re-runs + judge calls
	// share one freshly-built provider, so its cumulative usage is the run total).
	if ts, ok := provider.(interface {
		GetTokenStats() (int64, int64)
	}); ok {
		in, out := ts.GetTokenStats()
		fmt.Printf("tokens_in=%d tokens_out=%d\n", in, out)
	}
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SESSION\tITER\tSCORE\tREGRESSION\tBASE_MS\tCAND_MS\tNOTE")
	for _, r := range rep.Results {
		note := r.Verdict.Note
		if r.Err != "" {
			note = "ERR: " + r.Err
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%t\t%d\t%d\t%s\n",
			r.SessionID, r.Iteration, r.Verdict.Score, r.Verdict.Regression, r.BaselineMs, r.CandidateMs, note)
	}
	_ = w.Flush()
}

func filterSessions(sessions []replay.Session, id string) []replay.Session {
	out := sessions[:0:0]
	for _, s := range sessions {
		if s.SessionID == id {
			out = append(out, s)
		}
	}
	return out
}

func printReplayReport(dir string, rep replay.Report) {
	fmt.Printf("Replay report — %s\n", dir)
	fmt.Printf("sessions=%d exchanges=%d tool_calls=%d tool_failures=%d salvaged=%d abnormal_stops=%d max_token_stops=%d",
		rep.Sessions, rep.Exchanges, rep.ToolCalls, rep.ToolFailures, rep.Salvaged, rep.AbnormalStops, rep.MaxTokenStops)
	if rep.SkippedLines > 0 {
		fmt.Printf(" skipped_lines=%d", rep.SkippedLines)
	}
	fmt.Println()
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SESSION\tEXCHANGES\tTOOLS\tTOOL_FAIL\tABNORMAL\tMAX_TOK\tSALVAGED")
	for _, m := range rep.PerSession {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%t\n",
			m.SessionID, m.Exchanges, m.ToolCalls, m.ToolFailures, m.AbnormalStops, m.MaxTokenStops, m.Salvaged)
	}
	_ = w.Flush()
}
