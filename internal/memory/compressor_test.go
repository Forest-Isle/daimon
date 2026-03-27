package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompressToolResult(t *testing.T) {
	tmpDir := t.TempDir()
	compressor := NewIncrementalCompressor(tmpDir, nil)

	// Short output - no compression
	short := "short output"
	result := compressor.CompressToolResult(short)
	if result != short {
		t.Errorf("short output should not be compressed")
	}

	// Long output - should compress
	long := strings.Repeat("x", 1000)
	result = compressor.CompressToolResult(long)

	if !strings.Contains(result, "[输出过长，已截断]") {
		t.Errorf("long output should be compressed")
	}

	if !strings.Contains(result, "tool_results/") {
		t.Errorf("compressed output should reference file")
	}

	// Verify file was created
	files, _ := filepath.Glob(filepath.Join(tmpDir, "tool_results", "*.txt"))
	if len(files) != 1 {
		t.Errorf("expected 1 tool result file, got %d", len(files))
	}

	// Verify full content saved
	content, _ := os.ReadFile(files[0])
	if string(content) != long {
		t.Errorf("full content not saved correctly")
	}
}

func TestShouldCompress(t *testing.T) {
	compressor := NewIncrementalCompressor(t.TempDir(), nil)

	// Small context
	small := []map[string]string{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}
	if compressor.ShouldCompress(small) {
		t.Errorf("small context should not trigger compression")
	}

	// Large context
	large := []map[string]string{
		{"role": "user", "content": strings.Repeat("x", 400000)},
	}
	if !compressor.ShouldCompress(large) {
		t.Errorf("large context should trigger compression")
	}
}

type mockCompleter struct{}

func (m *mockCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	return "## 摘要\n用户询问了测试问题", nil
}

func TestGenerateDailySummary(t *testing.T) {
	tmpDir := t.TempDir()
	compressor := NewIncrementalCompressor(tmpDir, &mockCompleter{})

	messages := []map[string]string{
		{"role": "user", "content": "测试问题"},
		{"role": "assistant", "content": "测试回答"},
	}

	summary, err := compressor.GenerateDailySummary(context.Background(), messages, time.Now())
	if err != nil {
		t.Fatalf("GenerateDailySummary failed: %v", err)
	}

	if !strings.Contains(summary, "摘要") {
		t.Errorf("summary should contain expected content")
	}

	// Verify file created
	summaryPath := filepath.Join(tmpDir, "sessions", time.Now().Format("2006-01-02")+".summary.md")
	if _, err := os.Stat(summaryPath); os.IsNotExist(err) {
		t.Errorf("summary file not created")
	}
}
