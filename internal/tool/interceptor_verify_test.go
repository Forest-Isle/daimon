package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyInterceptor_FileWriteAddsVerificationMetadata(t *testing.T) {
	repo := initVerifyTestRepo(t)
	path := filepath.Join(repo, "test.txt")
	writeVerifyRepoFile(t, repo, "test.txt", "before\n")
	runVerifyGit(t, repo, "add", "test.txt")
	runVerifyGit(t, repo, "commit", "-m", "init")

	vi := NewVerifyInterceptor(repo)
	vi.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	call := &ToolCall{
		ToolName: "file_write",
		Input:    `{"path":"test.txt","content":"after\n"}`,
	}
	next := func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return &ToolResult{Output: "wrote file", Metadata: map[string]string{"success": "true"}}, nil
	}

	result, err := vi.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	verify := decodeVerifyMetadata(t, result.Metadata["verify"])
	if !verify.FileReadable {
		t.Fatalf("expected file_readable true, got %#v", verify)
	}
	if verify.FileSizeBytes == 0 {
		t.Fatalf("expected non-zero file size, got %#v", verify)
	}
	if !strings.Contains(verify.DiffSummary, "test.txt") {
		t.Fatalf("expected diff summary for test.txt, got %q", verify.DiffSummary)
	}
	if _, ok := result.Metadata["verify_warnings"]; ok {
		t.Fatalf("expected no verify warnings, got %s", result.Metadata["verify_warnings"])
	}
}

func TestVerifyInterceptor_ReadOnlyToolPassesThrough(t *testing.T) {
	vi := NewVerifyInterceptor(t.TempDir())
	expected := &ToolResult{Output: "ok", Metadata: map[string]string{"success": "true"}}

	result, err := vi.Intercept(context.Background(), &ToolCall{ToolName: "file_read", Input: `{"path":"x"}`}, func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		return expected, nil
	})
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	if result != expected {
		t.Fatal("expected result to pass through unchanged")
	}
	if _, ok := result.Metadata["verify"]; ok {
		t.Fatalf("did not expect verify metadata, got %#v", result.Metadata)
	}
}

func TestVerifyInterceptor_FailedToolPassesThrough(t *testing.T) {
	vi := NewVerifyInterceptor(t.TempDir())
	expected := &ToolResult{Output: "failed", Error: "boom", Metadata: map[string]string{"success": "false"}}

	result, err := vi.Intercept(context.Background(), &ToolCall{ToolName: "file_write", Input: `{"path":"x","content":"y"}`}, func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		return expected, nil
	})
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	if result != expected {
		t.Fatal("expected failed result to pass through unchanged")
	}
	if _, ok := result.Metadata["verify"]; ok {
		t.Fatalf("did not expect verify metadata, got %#v", result.Metadata)
	}
}

func TestVerifyInterceptor_BashWriteCommandVerifies(t *testing.T) {
	repo := initVerifyTestRepo(t)
	runVerifyGit(t, repo, "commit", "--allow-empty", "-m", "init")

	vi := NewVerifyInterceptor(repo)
	call := &ToolCall{
		ToolName: "bash",
		Input:    `{"command":"echo hello > bash.txt"}`,
	}
	next := func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		if err := os.WriteFile(filepath.Join(repo, "bash.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return &ToolResult{Output: `{"status":"ok"}`, Metadata: map[string]string{"status": "ok"}}, nil
	}

	result, err := vi.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	verify := decodeVerifyMetadata(t, result.Metadata["verify"])
	if !verify.FileReadable {
		t.Fatalf("expected file_readable true, got %#v", verify)
	}
	if !strings.Contains(verify.DiffSummary, "bash.txt") {
		t.Fatalf("expected diff summary for bash.txt, got %q", verify.DiffSummary)
	}
}

func TestVerifyInterceptor_BashReadOnlyCommandSkipsVerification(t *testing.T) {
	vi := NewVerifyInterceptor(t.TempDir())
	expected := &ToolResult{Output: `{"status":"ok"}`, Metadata: map[string]string{"status": "ok"}}

	result, err := vi.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: `{"command":"pwd"}`}, func(ctx context.Context, c *ToolCall) (*ToolResult, error) {
		return expected, nil
	})
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	if result != expected {
		t.Fatal("expected result to pass through unchanged")
	}
	if _, ok := result.Metadata["verify"]; ok {
		t.Fatalf("did not expect verify metadata, got %#v", result.Metadata)
	}
}

func initVerifyTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runVerifyGit(t, repo, "init")
	runVerifyGit(t, repo, "config", "user.name", "Verify Test")
	runVerifyGit(t, repo, "config", "user.email", "verify@example.com")
	return repo
}

func runVerifyGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func writeVerifyRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeVerifyMetadata(t *testing.T, raw string) verifyMetadata {
	t.Helper()
	if raw == "" {
		t.Fatal("expected verify metadata")
	}
	var verify verifyMetadata
	if err := json.Unmarshal([]byte(raw), &verify); err != nil {
		t.Fatalf("unmarshal verify metadata: %v", err)
	}
	return verify
}
