package tui

import (
	"github.com/Forest-Isle/IronClaw/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
)

// TUIEmitter implements agent.DashboardEmitter by forwarding events as
// Bubble Tea messages. Safe for concurrent use (tea.Program.Send is goroutine-safe).
type TUIEmitter struct {
	program *tea.Program
}

func NewTUIEmitter(p *tea.Program) *TUIEmitter {
	return &TUIEmitter{program: p}
}

func (e *TUIEmitter) EmitPhaseStart(_, phase string) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(phaseStartMsg{phase: phase})
}

func (e *TUIEmitter) EmitPhaseEnd(_, phase string, durationMs int64) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(phaseEndMsg{phase: phase, durationMs: durationMs})
}

func (e *TUIEmitter) EmitToolStart(_, toolName, input string) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(toolStartMsg{toolName: toolName, input: input})
}

func (e *TUIEmitter) EmitToolEnd(_, toolName string, succeeded bool, durationMs int64) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(toolEndMsg{toolName: toolName, succeeded: succeeded, durationMs: durationMs})
}

func (e *TUIEmitter) EmitSubAgentSpawn(_, _, _, _ string) {}

func (e *TUIEmitter) EmitSubAgentComplete(_, _ string, _ bool, _ int64) {}

func (e *TUIEmitter) EmitContextCompress(_, _ string, _ int, _, _ float64) {}

// SendMetrics pushes a runtime metrics snapshot to the TUI.
func (e *TUIEmitter) SendMetrics(m agent.RuntimeMetrics) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(metricsUpdateMsg{
		iteration:    m.Iteration,
		maxIter:      m.MaxIter,
		utilization:  m.Utilization,
		cacheCreate:  m.CacheCreate,
		cacheRead:    m.CacheRead,
		inputTokens:  m.InputTokens,
		outputTokens: m.OutputTokens,
		model:        m.Model,
		provider:     m.Provider,
	})
}
