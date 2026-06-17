package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/vcs"
	"github.com/spf13/cobra"
)

func newAttentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attention",
		Short: "Inspect and revert attention rules history",
	}
	cmd.AddCommand(newAttentionHistoryCmd(), newAttentionRevertCmd())
	return cmd
}

func newAttentionHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history [file]",
		Short: "Show attention rules git history",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := "rules.yaml"
			if len(args) > 0 {
				file = args[0]
			}
			return runAttentionHistory(cmd, file)
		},
	}
}

func newAttentionRevertCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revert <file>",
		Short: "Revert an attention rules file to its previous version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttentionRevert(cmd, args[0])
		},
	}
}

func runAttentionHistory(cmd *cobra.Command, file string) error {
	attentionDir := filepath.Join(appdir.BaseDir(), "attention")
	if err := vcs.EnsureRepo(cmd.Context(), attentionDir); err != nil {
		return fmt.Errorf("ensure attention rules repo: %w", err)
	}
	commits, err := vcs.Log(cmd.Context(), attentionDir, file, 20)
	if err != nil {
		return fmt.Errorf("attention rules history: %w", err)
	}
	if len(commits) == 0 {
		fmt.Println("No attention rules history.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SHA\tDATE\tSUBJECT")
	for _, commit := range commits {
		sha := commit.SHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", sha, commit.Date, commit.Subject)
	}
	return w.Flush()
}

func runAttentionRevert(cmd *cobra.Command, file string) error {
	attentionDir := filepath.Join(appdir.BaseDir(), "attention")
	fmt.Printf("Revert attention rules file %q to its previous version? [y/N] ", file)
	var answer string
	_, _ = fmt.Scanln(&answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Aborted.")
		return nil
	}
	if err := vcs.EnsureRepo(cmd.Context(), attentionDir); err != nil {
		return fmt.Errorf("ensure attention rules repo: %w", err)
	}
	if err := vcs.RevertFileToPrevious(cmd.Context(), attentionDir, file); err != nil {
		return fmt.Errorf("revert attention rules file %q: %w", file, err)
	}
	fmt.Printf("Reverted %s to previous version.\n", file)
	return nil
}
