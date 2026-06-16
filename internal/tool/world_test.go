package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/vcs"
	"github.com/Forest-Isle/daimon/internal/world"
)

func openToolWorldTestDB(t *testing.T) (*store.DB, *world.Store, world.Identity) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "world-tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	identity := world.Identity{Dir: filepath.Join(t.TempDir(), "identity")}
	if err := identity.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	return db, world.NewStore(db.DB), identity
}

func TestWorldReadToolSectionsAndValidation(t *testing.T) {
	_, store, identity := openToolWorldTestDB(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(identity.Dir, "digest.md"), []byte("name: Daimon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCommitment(ctx, world.Commitment{
		ID:    "commit_due",
		Kind:  "project",
		Title: "Ship world tools",
		DueAt: "2030-01-02T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCommitment(ctx, world.Commitment{
		ID:    "commit_later",
		Kind:  "deadline",
		Title: "Later item",
		DueAt: "2030-02-02T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []world.JournalEntry{
		{ID: "journal_old", Kind: "fact", Summary: "old", OccurredAt: "2030-01-01T00:00:00Z"},
		{ID: "journal_new", Kind: "decision", Summary: "new", OccurredAt: "2030-01-03T00:00:00Z"},
	} {
		if err := store.AppendJournal(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewWorldReadTool(store, identity)
	tests := []struct {
		name        string
		input       string
		want        string
		wantAbsent  string
		wantError   string
		exactOutput bool
	}{
		{
			name:        "identity",
			input:       `{"section":"identity"}`,
			want:        "name: Daimon\n",
			exactOutput: true,
		},
		{
			name:       "commitments with due_within",
			input:      `{"section":"commitments","due_within":"2030-01-31T00:00:00Z"}`,
			want:       "project/Ship world tools/active/2030-01-02T00:00:00Z",
			wantAbsent: "Later item",
		},
		{
			name:       "default commitments",
			input:      `{}`,
			want:       "Ship world tools",
			wantAbsent: "No journal entries",
		},
		{
			name:       "journal limit",
			input:      `{"section":"journal","limit":1}`,
			want:       "2030-01-03T00:00:00Z [decision] new (journal_new)",
			wantAbsent: "journal_old",
		},
		{
			name:      "unknown section",
			input:     `{"section":"unknown"}`,
			wantError: "unknown section",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tool.Execute(ctx, []byte(tt.input))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if tt.wantError != "" {
				if !strings.Contains(got.Error, tt.wantError) {
					t.Fatalf("Error = %q, want contains %q", got.Error, tt.wantError)
				}
				return
			}
			if got.Error != "" {
				t.Fatalf("unexpected Error = %q", got.Error)
			}
			if tt.exactOutput {
				if got.Output != tt.want {
					t.Fatalf("Output = %q, want %q", got.Output, tt.want)
				}
			} else if !strings.Contains(got.Output, tt.want) {
				t.Fatalf("Output = %q, want contains %q", got.Output, tt.want)
			}
			if tt.wantAbsent != "" && strings.Contains(got.Output, tt.wantAbsent) {
				t.Fatalf("Output = %q, should not contain %q", got.Output, tt.wantAbsent)
			}
		})
	}
}

func TestCommitmentToolActionsAndValidation(t *testing.T) {
	_, store, _ := openToolWorldTestDB(t)
	ctx := context.Background()
	tool := NewCommitmentTool(store)

	create, err := tool.Execute(ctx, []byte(`{"action":"create","kind":"routine","title":"Daily review","body":"Check plan","due_at":"2030-01-02T00:00:00Z","horizon":"day"}`))
	if err != nil {
		t.Fatalf("create Execute() error = %v", err)
	}
	if create.Error != "" {
		t.Fatalf("create Error = %q", create.Error)
	}
	if !strings.Contains(create.Output, "commitment created: commitment_") {
		t.Fatalf("create Output = %q", create.Output)
	}
	created, err := store.ListCommitments(ctx, []string{"active"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("active commitments len = %d, want 1", len(created))
	}

	list, err := tool.Execute(ctx, []byte(`{"action":"list","states":["active"]}`))
	if err != nil {
		t.Fatalf("list Execute() error = %v", err)
	}
	if list.Error != "" || !strings.Contains(list.Output, "Daily review") {
		t.Fatalf("list result = %#v", list)
	}

	updateInput := `{"action":"update","id":"` + created[0].ID + `","state":"waiting","title":"Daily planning review"}`
	update, err := tool.Execute(ctx, []byte(updateInput))
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if update.Error != "" || !strings.Contains(update.Output, "commitment updated: "+created[0].ID) {
		t.Fatalf("update result = %#v", update)
	}
	waiting, err := store.ListCommitments(ctx, []string{"waiting"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Title != "Daily planning review" {
		t.Fatalf("waiting commitments = %#v", waiting)
	}

	tests := []struct {
		name      string
		input     string
		wantError string
	}{
		{"unknown action", `{"action":"delete"}`, "unknown action"},
		{"create missing kind", `{"action":"create","title":"x"}`, "kind is required"},
		{"create bad kind", `{"action":"create","kind":"task","title":"x"}`, "unknown kind"},
		{"create missing title", `{"action":"create","kind":"project"}`, "title is required"},
		{"update missing id", `{"action":"update","state":"done"}`, "id is required"},
		{"update bad state", `{"action":"update","id":"x","state":"blocked"}`, "unknown state"},
		{"list bad state", `{"action":"list","states":["blocked"]}`, "unknown state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tool.Execute(ctx, []byte(tt.input))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(got.Error, tt.wantError) {
				t.Fatalf("Error = %q, want contains %q", got.Error, tt.wantError)
			}
		})
	}
}

func TestWorldEditToolWriteAppendAndFence(t *testing.T) {
	_, _, identity := openToolWorldTestDB(t)
	ctx := context.Background()
	tool := NewWorldEditTool(identity)

	written, err := tool.Execute(ctx, []byte(`{"file":"preferences/coding.md","content":"one\n"}`))
	if err != nil {
		t.Fatalf("write Execute() error = %v", err)
	}
	if written.Error != "" {
		t.Fatalf("write Error = %q", written.Error)
	}
	target := filepath.Join(identity.Dir, "preferences", "coding.md")
	if got := readString(t, target); got != "one\n" {
		t.Fatalf("written content = %q, want one\\n", got)
	}

	appended, err := tool.Execute(ctx, []byte(`{"file":"preferences/coding.md","content":"two\n","append":true}`))
	if err != nil {
		t.Fatalf("append Execute() error = %v", err)
	}
	if appended.Error != "" {
		t.Fatalf("append Error = %q", appended.Error)
	}
	if got := readString(t, target); got != "one\ntwo\n" {
		t.Fatalf("appended content = %q, want one\\ntwo\\n", got)
	}

	tests := []struct {
		name      string
		file      string
		wantError string
	}{
		{"missing file", "", "file is required"},
		{"parent traversal", "../x", "escapes identity root"},
		{"absolute path", "/etc/x", "relative path"},
		{"cleaned traversal", "a/../../x", "escapes identity root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"file":"` + tt.file + `","content":"blocked"}`
			got, err := tool.Execute(ctx, []byte(input))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(got.Error, tt.wantError) {
				t.Fatalf("Error = %q, want contains %q", got.Error, tt.wantError)
			}
		})
	}

	missingContent, err := tool.Execute(ctx, []byte(`{"file":"profile.md"}`))
	if err != nil {
		t.Fatalf("missing content Execute() error = %v", err)
	}
	if !strings.Contains(missingContent.Error, "content is required") {
		t.Fatalf("missing content Error = %q, want content is required", missingContent.Error)
	}
}

func TestWorldEditToolCommitsIdentityChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	_, _, identity := openToolWorldTestDB(t)
	ctx := context.Background()
	tool := NewWorldEditTool(identity)

	for _, content := range []string{"one\n", "two\n"} {
		got, err := tool.Execute(ctx, []byte(`{"file":"profile.md","content":`+strconvQuote(content)+`}`))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got.Error != "" {
			t.Fatalf("Execute() Error = %q", got.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(identity.Dir, ".git")); err != nil {
		t.Fatalf(".git stat: %v", err)
	}
	commits, err := vcs.Log(ctx, identity.Dir, "profile.md", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) < 2 {
		t.Fatalf("profile commits len = %d, want at least 2", len(commits))
	}
}

func strconvQuote(s string) string {
	return strconv.Quote(s)
}

func TestWorldEditToolRejectsSymlinkEscape(t *testing.T) {
	_, _, identity := openToolWorldTestDB(t)
	ctx := context.Background()
	tool := NewWorldEditTool(identity)

	outside := t.TempDir()
	link := filepath.Join(identity.Dir, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got, err := tool.Execute(ctx, []byte(`{"file":"outside-link/evil.md","content":"blocked"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(got.Error, "symlink target") || !strings.Contains(got.Error, "escapes identity root") {
		t.Fatalf("Error = %q, want symlink escape error", got.Error)
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat errored unexpectedly: %v", err)
	}
}

func TestWorldToolCapabilities(t *testing.T) {
	_, store, identity := openToolWorldTestDB(t)

	readCaps := GetCapabilities(NewWorldReadTool(store, identity))
	if !readCaps.IsReadOnly || readCaps.IsDestructive || readCaps.ParallelSafety != ParallelSafe {
		t.Fatalf("world_read capabilities = %#v", readCaps)
	}

	commitmentCaps := GetCapabilities(NewCommitmentTool(store))
	if commitmentCaps.IsReadOnly || commitmentCaps.IsDestructive || commitmentCaps.ParallelSafety != ParallelNever {
		t.Fatalf("commitment capabilities = %#v", commitmentCaps)
	}

	editTool := NewWorldEditTool(identity)
	var _ PathScopedTool = editTool
	editCaps := GetCapabilities(editTool)
	if editCaps.IsReadOnly || editCaps.IsDestructive || editCaps.ParallelSafety != ParallelPathScoped {
		t.Fatalf("world_edit capabilities = %#v", editCaps)
	}
	paths, err := editTool.ExtractPaths([]byte(`{"file":"profile.md"}`))
	if err != nil {
		t.Fatalf("ExtractPaths() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(identity.Dir, "profile.md") {
		t.Fatalf("ExtractPaths() = %#v", paths)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
