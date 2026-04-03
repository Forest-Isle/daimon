package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSidechainRecorder_Record(t *testing.T) {
	recorder := NewSidechainRecorder("agent-1", "parent-1", "chain-1", nil)

	if err := recorder.RecordMessage("user", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := recorder.RecordMessage("assistant", "hi there"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := recorder.RecordToolCall("bash", `{"command":"ls"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := recorder.RecordToolResult("bash", "file1.go\nfile2.go", "success"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := recorder.RecordStatus("completed", "agent finished"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := recorder.Entries()
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	if entries[0].Type != "message" {
		t.Errorf("expected type 'message', got %q", entries[0].Type)
	}
	if entries[0].Metadata["role"] != "user" {
		t.Errorf("expected role 'user', got %q", entries[0].Metadata["role"])
	}
	if entries[0].AgentID != "agent-1" {
		t.Errorf("expected agent ID 'agent-1', got %q", entries[0].AgentID)
	}
	if entries[0].Metadata["chain_id"] != "chain-1" {
		t.Errorf("expected chain_id 'chain-1', got %q", entries[0].Metadata["chain_id"])
	}

	if entries[2].Type != "tool_call" {
		t.Errorf("expected type 'tool_call', got %q", entries[2].Type)
	}
	if entries[2].Metadata["tool"] != "bash" {
		t.Errorf("expected tool 'bash', got %q", entries[2].Metadata["tool"])
	}

	if entries[4].Type != "status" {
		t.Errorf("expected type 'status', got %q", entries[4].Type)
	}
}

func TestFileSidechainStore_AppendAndGetByAgent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileSidechainStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	recorder := NewSidechainRecorder("agent-a", "parent-x", "chain-x", store)
	if err := recorder.RecordMessage("user", "hello"); err != nil {
		t.Fatalf("record message: %v", err)
	}
	if err := recorder.RecordMessage("assistant", "world"); err != nil {
		t.Fatalf("record message: %v", err)
	}

	// Read back
	entries, err := store.GetByAgent("agent-a")
	if err != nil {
		t.Fatalf("get by agent: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", entries[0].Content)
	}
	if entries[1].Content != "world" {
		t.Errorf("expected 'world', got %q", entries[1].Content)
	}

	// Verify files exist on disk
	agentDir := filepath.Join(dir, "agent-a")
	files, _ := os.ReadDir(agentDir)
	if len(files) != 2 {
		t.Errorf("expected 2 files on disk, got %d", len(files))
	}
}

func TestFileSidechainStore_GetByChain(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileSidechainStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Two agents in same chain
	r1 := NewSidechainRecorder("agent-1", "", "chain-abc", store)
	r2 := NewSidechainRecorder("agent-2", "agent-1", "chain-abc", store)

	if err := r1.RecordMessage("user", "msg from agent-1"); err != nil {
		t.Fatalf("record message: %v", err)
	}
	if err := r2.RecordMessage("user", "msg from agent-2"); err != nil {
		t.Fatalf("record message: %v", err)
	}

	entries, err := store.GetByChain("chain-abc")
	if err != nil {
		t.Fatalf("get by chain: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in chain, got %d", len(entries))
	}
}

func TestFileSidechainStore_GetByAgent_Empty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileSidechainStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	entries, err := store.GetByAgent("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestRecoverFromSidechain(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileSidechainStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	r := NewSidechainRecorder("recover-agent", "parent", "chain", store)
	if err := r.RecordMessage("user", "question"); err != nil {
		t.Fatalf("record message: %v", err)
	}
	if err := r.RecordToolCall("bash", "ls"); err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	r.RecordToolResult("bash", "output", "success")
	r.RecordMessage("assistant", "answer")

	entries, err := RecoverFromSidechain(store, "recover-agent")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 recovered entries, got %d", len(entries))
	}
	if entries[0].Type != "message" || entries[0].Content != "question" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[3].Type != "message" || entries[3].Content != "answer" {
		t.Errorf("unexpected last entry: %+v", entries[3])
	}
}
