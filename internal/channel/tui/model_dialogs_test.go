package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLocalCommandIncludesTUIOnlyCommands(t *testing.T) {
	assert.True(t, isLocalCommand("/mouse"))
	assert.True(t, isLocalCommand("/m"))
	assert.True(t, isLocalCommand("/status"))
	assert.True(t, isLocalCommand("/stats"))
	assert.True(t, isLocalCommand("/model"))
	assert.False(t, isLocalCommand("/model claude-sonnet-4-20250514"))
}

func TestHandleLocalCommandStatus(t *testing.T) {
	m := NewModel("test-version", "local", "/tmp/daimon")

	handled, cmd := m.handleLocalCommand("/status")
	require.True(t, handled)
	require.Nil(t, cmd)
	require.NotEmpty(t, m.messages)
	got := m.messages[len(m.messages)-1].content
	assert.Contains(t, got, "Status")
	assert.Contains(t, got, "Version: test-version")
	assert.Contains(t, got, "Working directory: /tmp/daimon")
}

func TestFormatConversationExport(t *testing.T) {
	ts := time.Date(2026, 6, 11, 12, 30, 0, 0, time.UTC)
	got := formatConversationExport([]chatMessage{{
		role:      "user",
		content:   "hello",
		timestamp: ts,
	}})

	assert.Contains(t, got, "Daimon Conversation Export")
	assert.Contains(t, got, "[2026-06-11 12:30:00] USER")
	assert.Contains(t, got, "hello")
}

func TestWriteConversationExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "conversation.txt")

	got, err := writeConversationExport(path, "content")
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(path), got)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteConversationExportDefaultFilename(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWd))
	})

	got, err := writeConversationExport("  ", "empty")
	require.NoError(t, err)
	assert.Equal(t, "conversation.txt", got)
	assert.False(t, strings.Contains(got, ".."))
}
