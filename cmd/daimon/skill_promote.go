package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/appdir"
	"github.com/Forest-Isle/daimon/internal/skill"
	"github.com/spf13/cobra"
)

func validSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("empty slug")
	}
	if slug == "." || slug == ".." {
		return fmt.Errorf("invalid slug %q", slug)
	}
	if strings.Contains(slug, "/") || strings.ContainsRune(slug, os.PathSeparator) {
		return fmt.Errorf("invalid slug %q: must be a single directory name", slug)
	}
	return nil
}

func ensureNotSymlink(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symlink; refusing", p)
	}
	return nil
}

func ensureWithinRoot(root, p string) error {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	if r, e := filepath.EvalSymlinks(rootAbs); e == nil {
		rootAbs = r
	}
	pAbs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if r, e := filepath.EvalSymlinks(pAbs); e == nil {
		pAbs = r
	}
	rel, err := filepath.Rel(rootAbs, pAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%q escapes %s", p, root)
	}
	return nil
}

func validateDraft(skillMdPath string) (*skill.Skill, error) {
	s, err := skill.ParseSkill(skillMdPath)
	if err != nil {
		return nil, err
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return nil, fmt.Errorf("missing name")
	}
	if !s.Metadata.Distilled {
		return nil, fmt.Errorf("not a distilled draft (metadata.distilled != true)")
	}
	body, err := s.Content()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("empty body")
	}
	return s, nil
}

type draftInfo struct {
	Slug        string
	Name        string
	Version     string
	Description string
	Distilled   bool
	Episodes    int
	Status      string
}

func listDrafts(stagingDir string) ([]draftInfo, error) {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read staging dir: %w", err)
	}

	var drafts []draftInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := draftInfo{Slug: e.Name()}
		path := filepath.Join(stagingDir, e.Name(), "SKILL.md")
		s, err := skill.ParseSkill(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				info.Status = "missing SKILL.md"
			} else {
				info.Status = err.Error()
			}
			drafts = append(drafts, info)
			continue
		}
		info.Name = s.Name
		info.Version = s.Version
		info.Description = s.Description
		info.Distilled = s.Metadata.Distilled
		info.Episodes = len(s.Metadata.SourceEpisodes)
		if _, err := validateDraft(path); err != nil {
			info.Status = err.Error()
		} else {
			info.Status = "valid"
		}
		drafts = append(drafts, info)
	}

	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Slug < drafts[j].Slug
	})
	return drafts, nil
}

func promoteDraft(stagingDir, activeDir, slug string) (string, error) {
	if err := validSlug(slug); err != nil {
		return "", err
	}

	srcDir := filepath.Join(stagingDir, slug)
	src := filepath.Join(srcDir, "SKILL.md")
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("draft %q not found in %s", slug, stagingDir)
		}
		return "", fmt.Errorf("stat draft: %w", err)
	}

	draft, err := validateDraft(src)
	if err != nil {
		return "", err
	}

	mgr := skill.New()
	if err := mgr.LoadBuiltin(); err != nil {
		return "", fmt.Errorf("load builtin skills: %w", err)
	}
	if err := mgr.LoadDir(activeDir); err != nil {
		return "", fmt.Errorf("load active skills: %w", err)
	}
	for _, active := range mgr.All() {
		if strings.TrimSpace(active.Name) == draft.Name {
			return "", fmt.Errorf("a skill named %q is already active", draft.Name)
		}
	}

	target := filepath.Join(activeDir, slug)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("%q already promoted (target dir exists)", slug)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}
	if err := ensureNotSymlink(srcDir); err != nil {
		return "", err
	}
	if err := ensureNotSymlink(src); err != nil {
		return "", err
	}
	if err := ensureWithinRoot(stagingDir, src); err != nil {
		return "", err
	}
	if err := os.Rename(srcDir, target); err != nil {
		return "", fmt.Errorf("promote draft: %w", err)
	}
	return target, nil
}

func demoteSkill(activeDir, stagingDir, slug string) error {
	if err := validSlug(slug); err != nil {
		return err
	}

	dir := filepath.Join(activeDir, slug)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("skill %q not found in %s", slug, activeDir)
		}
		return fmt.Errorf("stat skill: %w", err)
	}
	s, err := skill.ParseSkill(path)
	if err != nil {
		return err
	}
	if !s.Metadata.Distilled {
		return fmt.Errorf("%q is not a distilled skill; use `daimon skill remove`", slug)
	}
	if err := ensureNotSymlink(dir); err != nil {
		return err
	}
	if err := ensureNotSymlink(path); err != nil {
		return err
	}
	if err := ensureWithinRoot(activeDir, path); err != nil {
		return err
	}
	target := filepath.Join(stagingDir, slug)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("draft %q already staged", slug)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	if err := os.Rename(dir, target); err != nil {
		return fmt.Errorf("demote skill: %w", err)
	}
	return nil
}

func newSkillDraftsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drafts",
		Short: "List staged distilled skill drafts awaiting promotion",
		RunE: func(cmd *cobra.Command, args []string) error {
			drafts, err := listDrafts(appdir.SkillsStagingDir())
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
			if err := validSlug(slug); err != nil {
				return err
			}

			path := filepath.Join(appdir.SkillsStagingDir(), slug, "SKILL.md")
			draft, err := validateDraft(path)
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

			target, err := promoteDraft(appdir.SkillsStagingDir(), appdir.SkillsDir(), slug)
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

			if err := demoteSkill(appdir.SkillsDir(), appdir.SkillsStagingDir(), slug); err != nil {
				return err
			}
			fmt.Printf("Demoted %q (returned to drafts).\n", slug)
			return nil
		},
	}
}
