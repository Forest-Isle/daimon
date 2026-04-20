package tui

import tea "github.com/charmbracelet/bubbletea"

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

// SendMetrics pushes a metrics snapshot to the TUI.
func (e *TUIEmitter) SendMetrics(iteration, maxIter int, utilization float64, cacheCreate, cacheRead int64) {
	if e == nil || e.program == nil {
		return
	}
	e.program.Send(metricsUpdateMsg{
		iteration:   iteration,
		maxIter:     maxIter,
		utilization: utilization,
		cacheCreate: cacheCreate,
		cacheRead:   cacheRead,
	})
}
