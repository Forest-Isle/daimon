package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/spf13/cobra"
)

type undoSpecSummary struct {
	Op        string `json:"op"`
	Path      string `json:"path"`
	Existed   bool   `json:"existed"`
	Truncated bool   `json:"truncated,omitempty"`
}

func newUndoCmd() *cobra.Command {
	var configPath string
	var devMode bool

	cmd := &cobra.Command{
		Use:   "undo [receipt-id|list]",
		Short: "List or execute recorded reversible action undo entries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "list" {
				return runUndoList(cmd.Context(), configPath, devMode)
			}
			return runUndo(cmd.Context(), configPath, devMode, strings.TrimSpace(args[0]))
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config file (auto-discovered if empty)")
	cmd.Flags().BoolVar(&devMode, "dev", false, "use configs/daimon.yaml in dev mode")
	return cmd
}

func runUndoList(ctx context.Context, configPath string, devMode bool) error {
	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	entries, err := st.ListUndoable(ctx, 50)
	if err != nil {
		return fmt.Errorf("list undoable actions: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("No undoable actions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "RECEIPT_ID\tTOOL\tCREATED\tPATH")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			entry.ReceiptID, entry.ToolName, entry.CreatedAt, decodeUndoSpec(entry.UndoSpec).Path)
	}
	return w.Flush()
}

func runUndo(ctx context.Context, configPath string, devMode bool, id string) error {
	if id == "" {
		return errors.New("receipt id is required")
	}
	st, closeDB, err := openActionStore(configPath, devMode)
	if err != nil {
		return err
	}
	defer closeDB()

	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	entry, err := st.GetUndo(ctx, id)
	if errors.Is(err, action.ErrUndoNotFound) {
		return fmt.Errorf("undo receipt %q not found", id)
	}
	if err != nil {
		return fmt.Errorf("get undo: %w", err)
	}

	snap := decodeUndoSpec(entry.UndoSpec)
	fmt.Printf("Receipt: %s\n", entry.ReceiptID)
	fmt.Printf("Tool: %s\n", entry.ToolName)
	fmt.Printf("Path: %s\n", snap.Path)
	switch {
	case snap.Truncated:
		fmt.Println("Action: NOT reversible (content not captured)")
	case !snap.Existed:
		fmt.Println("Action: will DELETE created file")
	default:
		fmt.Println("Action: will RESTORE previous content")
	}
	fmt.Print("Undo this action? [y/N] ")
	var answer string
	_, _ = fmt.Scanln(&answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Aborted.")
		return nil
	}

	// The undo executor fences file paths to the caller's current project
	// directory, matching the file tools' workdir boundary.
	if err := st.Undo(ctx, root, id); errors.Is(err, action.ErrUndoAlreadyDone) {
		return fmt.Errorf("undo receipt %q is already undone", id)
	} else if err != nil {
		return fmt.Errorf("undo action: %w", err)
	}
	fmt.Println("Undone.")
	return nil
}

func openActionStore(configPath string, devMode bool) (*action.Store, func(), error) {
	resolvedPath, err := config.FindConfigPath(configPath, devMode)
	if err != nil {
		return nil, nil, fmt.Errorf("find config: %w", err)
	}
	cfg, err := config.Load(resolvedPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	db, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	return action.NewStore(db.DB), func() { _ = db.Close() }, nil
}

func decodeUndoSpec(spec string) undoSpecSummary {
	var snap undoSpecSummary
	_ = json.Unmarshal([]byte(spec), &snap)
	return snap
}
