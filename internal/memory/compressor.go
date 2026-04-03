package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MaxToolOutputChars = 500
	DefaultTokenLimit  = 100000
	ReserveTokens      = 20000
)

// IncrementalCompressor implements ReMe-style three-layer compression
type IncrementalCompressor struct {
	storageDir string
	completer  Completer
	tokenLimit int
}

func NewIncrementalCompressor(storageDir string, completer Completer) *IncrementalCompressor {
	_ = os.MkdirAll(filepath.Join(storageDir, "tool_results"), 0755)
	_ = os.MkdirAll(filepath.Join(storageDir, "sessions"), 0755)

	return &IncrementalCompressor{
		storageDir: storageDir,
		completer:  completer,
		tokenLimit: DefaultTokenLimit,
	}
}

// CompressToolResult truncates long tool outputs and saves to file
func (c *IncrementalCompressor) CompressToolResult(result string) string {
	if len(result) <= MaxToolOutputChars {
		return result
	}

	// Save full output
	id := uuid.New().String()
	path := filepath.Join(c.storageDir, "tool_results", id+".txt")
	_ = os.WriteFile(path, []byte(result), 0644)

	// Return truncated + reference
	return fmt.Sprintf(
		"[输出过长，已截断]\n前%d字符:\n%s\n...\n\n完整输出: %s",
		MaxToolOutputChars,
		result[:MaxToolOutputChars],
		path,
	)
}

// GenerateDailySummary creates incremental summary for a session
func (c *IncrementalCompressor) GenerateDailySummary(ctx context.Context, messages []map[string]string, date time.Time) (string, error) {
	if c.completer == nil {
		return "", fmt.Errorf("completer not available")
	}

	// Load existing summary
	summaryPath := filepath.Join(c.storageDir, "sessions", date.Format("2006-01-02")+".summary.md")
	existingSummary, _ := os.ReadFile(summaryPath)

	// Format new messages
	var formatted strings.Builder
	for _, msg := range messages {
		formatted.WriteString(fmt.Sprintf("%s: %s\n", msg["role"], msg["content"]))
	}

	prompt := fmt.Sprintf(`现有摘要:
%s

今日新增对话:
%s

请更新摘要，保留关键信息:
- 用户目标
- 重要决策
- 待办事项
- 关键上下文

输出Markdown格式。`, string(existingSummary), formatted.String())

	summary, err := c.completer.Complete(ctx, "你是记忆摘要助手", prompt)
	if err != nil {
		return "", err
	}

	// Save summary
	_ = os.WriteFile(summaryPath, []byte(summary), 0644)

	return summary, nil
}

// ShouldCompress checks if context exceeds threshold
func (c *IncrementalCompressor) ShouldCompress(messages []map[string]string) bool {
	tokens := c.countTokens(messages)
	return tokens > (c.tokenLimit - ReserveTokens)
}

// countTokens estimates token count (1 token ≈ 4 chars)
func (c *IncrementalCompressor) countTokens(messages []map[string]string) int {
	total := 0
	for _, msg := range messages {
		total += len(msg["content"]) / 4
	}
	return total
}
