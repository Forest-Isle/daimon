package agent

import (
	"context"
	"os"
	"testing"

	"github.com/Forest-Isle/daimon/internal/tool"
)

func TestAgentToolContextSetsDefaultWorkDir(t *testing.T) {
	ctx := agentToolContext(context.Background(), "sess_1")
	if got := tool.SessionIDFromContext(ctx); got != "sess_1" {
		t.Fatalf("session id = %q, want sess_1", got)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := tool.WorkDirFromContext(ctx); got != cwd {
		t.Fatalf("workdir = %q, want %q", got, cwd)
	}
}

func TestAgentToolContextPreservesExistingWorkDir(t *testing.T) {
	ctx := tool.WithWorkDir(context.Background(), "/tmp/custom-workdir")
	ctx = agentToolContext(ctx, "sess_2")
	if got := tool.WorkDirFromContext(ctx); got != "/tmp/custom-workdir" {
		t.Fatalf("workdir = %q, want existing workdir", got)
	}
}
