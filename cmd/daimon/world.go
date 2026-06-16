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

func newWorldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "world",
		Short: "Inspect and revert identity history",
	}
	cmd.AddCommand(newWorldHistoryCmd(), newWorldRevertCmd())
	return cmd
}

func newWorldHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history [file]",
		Short: "Show identity git history",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ""
			if len(args) > 0 {
				file = args[0]
			}
			return runWorldHistory(cmd, file)
		},
	}
}

func newWorldRevertCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revert <file>",
		Short: "Revert an identity file to its previous version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorldRevert(cmd, args[0])
		},
	}
}

func runWorldHistory(cmd *cobra.Command, file string) error {
	identityDir := filepath.Join(appdir.BaseDir(), "world", "identity")
	if err := vcs.EnsureRepo(cmd.Context(), identityDir); err != nil {
		return fmt.Errorf("ensure identity repo: %w", err)
	}
	commits, err := vcs.Log(cmd.Context(), identityDir, file, 20)
	if err != nil {
		return fmt.Errorf("world history: %w", err)
	}
	if len(commits) == 0 {
		fmt.Println("No identity history.")
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

func runWorldRevert(cmd *cobra.Command, file string) error {
	identityDir := filepath.Join(appdir.BaseDir(), "world", "identity")
	fmt.Printf("Revert identity file %q to its previous version? [y/N] ", file)
	var answer string
	_, _ = fmt.Scanln(&answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Aborted.")
		return nil
	}
	if err := vcs.EnsureRepo(cmd.Context(), identityDir); err != nil {
		return fmt.Errorf("ensure identity repo: %w", err)
	}
	if err := vcs.RevertFileToPrevious(cmd.Context(), identityDir, file); err != nil {
		return fmt.Errorf("revert identity file %q: %w", file, err)
	}
	fmt.Printf("Reverted %s to previous version.\n", file)
	return nil
}
