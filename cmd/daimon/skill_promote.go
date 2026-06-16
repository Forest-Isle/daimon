package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/skill"
	"github.com/spf13/cobra"
)

func newSkillDraftsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drafts",
		Short: "List staged distilled skill drafts awaiting promotion",
		RunE: func(cmd *cobra.Command, args []string) error {
			drafts, err := skill.ListDrafts(appdir.SkillsStagingDir())
			if err != nil {
				return err
			}
			if len(drafts) == 0 {
				fmt.Println("No distilled drafts staged.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "SLUG\tNAME\tVERSION\tEPISODES\tSTATUS\tDESCRIPTION")
			for _, d := range drafts {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
					d.Slug, d.Name, d.Version, d.Episodes, d.Status, truncate(d.Description, 50))
			}
			return w.Flush()
		},
	}
}

func newSkillPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <slug>",
		Short: "Promote a staged distilled skill draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			if err := skill.ValidSlug(slug); err != nil {
				return err
			}

			path := filepath.Join(appdir.SkillsStagingDir(), slug, "SKILL.md")
			draft, err := skill.ValidateDraft(path)
			if err != nil {
				return err
			}
			body, err := draft.Content()
			if err != nil {
				return err
			}

			fmt.Printf("Name: %s\n", draft.Name)
			fmt.Printf("Description: %s\n", draft.Description)
			fmt.Printf("Version: %s\n", draft.Version)
			fmt.Printf("SourceCandidate: %s\n", draft.Metadata.SourceCandidate)
			fmt.Printf("Episodes: %d\n", len(draft.Metadata.SourceEpisodes))
			fmt.Printf("Preview:\n%s\n\n", truncate(strings.TrimSpace(body), 200))
			fmt.Println("§706: Promoting makes this an ACTIVE prompt-reference skill. Its actions remain governed; first execution is NOT auto-held.")
			fmt.Printf("Promote skill %q? [y/N] ", slug)
			var answer string
			_, _ = fmt.Scanln(&answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}

			target, err := skill.PromoteDraft(appdir.SkillsStagingDir(), appdir.SkillsDir(), slug)
			if err != nil {
				return err
			}
			fmt.Printf("Promoted to %s.\n", target)
			return nil
		},
	}
}

func newSkillDemoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demote <slug>",
		Short: "Un-promote a distilled skill, returning it to the staging drafts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			fmt.Printf("Un-promote (return to drafts) distilled skill %q? [y/N] ", slug)
			var answer string
			_, _ = fmt.Scanln(&answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}

			if err := skill.DemoteSkill(appdir.SkillsDir(), appdir.SkillsStagingDir(), slug); err != nil {
				return err
			}
			fmt.Printf("Demoted %q (returned to drafts).\n", slug)
			return nil
		},
	}
}
