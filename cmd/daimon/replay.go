package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/replay"
	"github.com/spf13/cobra"
)

// newReplayCmd builds the `daimon replay` command: it reads the recorded replay
// journals and prints an offline health report over the sessions they captured.
// It never re-runs anything or contacts a provider.
func newReplayCmd() *cobra.Command {
	var replaysDir string
	var sessionID string

	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Analyze recorded agent replay journals (offline)",
		Long: "Read the JSONL replay journals under the replays directory and print " +
			"an offline health report (exchanges, tool failures, salvaged episodes, " +
			"abnormal stops) over the recorded sessions.",
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

			rep := replay.Analyze(sessions, skipped)
			printReplayReport(dir, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&replaysDir, "replays", "", "replay journals directory (default: ~/.daimon/replays)")
	cmd.Flags().StringVar(&sessionID, "session", "", "limit the report to a single session id")
	return cmd
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
