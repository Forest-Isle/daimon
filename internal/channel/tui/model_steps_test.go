package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stepsOf(m *Model) []*workflowStep {
	var out []*workflowStep
	for i := range m.messages {
		if m.messages[i].role == "step" {
			out = append(out, m.messages[i].step)
		}
	}
	return out
}

func TestToolActivityBuildsAndUpdatesStep(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(toolActivityMsg{callID: "a1", tool: "grep", arg: "foo", done: false})
	steps := stepsOf(&m)
	require.Len(t, steps, 1)
	assert.Equal(t, "grep", steps[0].tool)
	assert.Equal(t, "foo", steps[0].arg)
	assert.False(t, steps[0].done)
	assert.True(t, m.hasSteps())

	m.Update(toolActivityMsg{
		callID: "a1", done: true, ok: true,
		resultSummary: "3 lines", output: "a\nb\nc", duration: 400 * time.Millisecond,
	})
	steps = stepsOf(&m)
	require.Len(t, steps, 1)
	assert.True(t, steps[0].done)
	assert.True(t, steps[0].ok)
	assert.Equal(t, "3 lines", steps[0].resultSummary)
	assert.Equal(t, 400*time.Millisecond, steps[0].duration)
}

func TestClearResetsStepIndex(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(toolActivityMsg{callID: "a1", tool: "grep", arg: "foo", done: false})
	m.Update(sessionResetMsg{})
	assert.Empty(t, m.stepIndex)
	assert.False(t, m.hasSteps())
}
