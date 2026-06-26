package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewHeightFitsWithPanels(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Model)
	}{
		{
			name: "help panel",
			setup: func(m *Model) {
				m.showHelpPanel = true
			},
		},
		{
			name: "model panel",
			setup: func(m *Model) {
				m.SetModelRoles("", "", "", "")
				m.SetCurrentModel("claude-sonnet-4-6")
				m.showModelPanel = true
				m.modelSelectionIdx = 1
			},
		},
		{
			name: "suggestions",
			setup: func(m *Model) {
				m.textarea.SetValue("/")
				m.updateSuggestions()
			},
		},
		{
			name: "typing indicator",
			setup: func(m *Model) {
				m.waitingForResponse = true
				m.addMessage("user", "hello")
				m.refreshViewport()
			},
		},
	}
	sizes := []struct {
		width  int
		height int
	}{
		{width: 80, height: 24},
		{width: 60, height: 18},
	}

	for _, tt := range tests {
		for _, size := range sizes {
			t.Run(tt.name, func(t *testing.T) {
				m := NewModel("test-version", "local", "/very/long/path/for/an/daimon/project")
				_, cmd := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				require.Nil(t, cmd)

				tt.setup(&m)
				m.viewport.Height = m.viewportHeight()

				view := m.View()
				assert.LessOrEqual(t, lipgloss.Height(view), size.height)
				assertRenderedWidthWithin(t, view, size.width)
			})
		}
	}
}

func TestTwoColumnLineStaysWithinWidth(t *testing.T) {
	got := twoColumnLine("  ", "/schedule", "<list|add|remove|enable|disable|run> · Manage scheduled prompts", 32)

	assert.LessOrEqual(t, lipgloss.Width(got), 32)
	assert.True(t, strings.Contains(got, "…"))
}

func TestPanelTitleLineStaysWithinWidth(t *testing.T) {
	got := panelTitleLine("Commands", "128 commands available", 24, statsHeaderStyle)

	assert.LessOrEqual(t, lipgloss.Width(got), 24)
	assert.Contains(t, got, "Commands")
}

func TestVisibleRangeCentersSelection(t *testing.T) {
	start, end := visibleRange(20, 10, 5)

	assert.Equal(t, 8, start)
	assert.Equal(t, 13, end)
}

func TestStatusBarTruncatesLongSegments(t *testing.T) {
	m := NewModel("test-version", "local", "/tmp")
	m.width = 48
	m.activeTool = "very_long_tool_name"
	m.activeToolSummary = "reading an extremely long path that should not take the whole status bar"
	m.currentModel = "claude-sonnet-4-6-with-a-very-long-suffix"
	m.mouseEnabled = false

	assert.LessOrEqual(t, lipgloss.Width(m.renderStatusBar()), 48)
}

func TestRenderStepLineCollapsed(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.addMessage("user", "find the close logic")
	m.appendStep("a1", "grep", "implicitClose")
	if i, ok := m.stepIndex["a1"]; ok {
		s := m.messages[i].step
		s.done, s.ok, s.resultSummary, s.duration = true, true, "3 lines", 400*time.Millisecond
	}
	m.addMessage("agent", "It closes at episode.go:142")

	out := m.renderStaticChat()
	assert.Contains(t, out, "grep")
	assert.Contains(t, out, "implicitClose")
	assert.Contains(t, out, "3 lines")
	assert.Contains(t, out, "│") // round grouping guide
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 80)
	}
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "400ms", formatDuration(400*time.Millisecond))
	assert.Equal(t, "1.5s", formatDuration(1500*time.Millisecond))
}

func assertRenderedWidthWithin(t *testing.T, rendered string, maxWidth int) {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), maxWidth, "line exceeded terminal width: %q", line)
	}
}
