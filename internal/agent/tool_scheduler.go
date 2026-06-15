package agent

import (
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type scheduledToolCall struct {
	call   mind.ToolUseBlock
	safety tool.ParallelSafety
	paths  []string
}

func (a *Agent) scheduleToolBatches(calls []mind.ToolUseBlock) [][]mind.ToolUseBlock {
	if len(calls) == 0 {
		return nil
	}

	maxParallel := a.maxParallelTools()
	if maxParallel <= 1 {
		batches := make([][]mind.ToolUseBlock, 0, len(calls))
		for _, call := range calls {
			batches = append(batches, []mind.ToolUseBlock{call})
		}
		return batches
	}

	var batches [][]mind.ToolUseBlock
	var current []scheduledToolCall
	currentPaths := make(map[string]bool)

	flush := func() {
		if len(current) == 0 {
			return
		}
		batch := make([]mind.ToolUseBlock, 0, len(current))
		for _, sc := range current {
			batch = append(batch, sc.call)
		}
		batches = append(batches, batch)
		current = nil
		currentPaths = make(map[string]bool)
	}

	for _, call := range calls {
		sc := a.classifyToolCall(call)
		if sc.safety == tool.ParallelNever {
			flush()
			batches = append(batches, []mind.ToolUseBlock{call})
			continue
		}

		if len(current) >= maxParallel || pathConflicts(currentPaths, sc.paths) {
			flush()
		}
		current = append(current, sc)
		for _, p := range sc.paths {
			currentPaths[p] = true
		}
	}
	flush()

	return batches
}

func (a *Agent) maxParallelTools() int {
	if !a.deps.Core.ToolsCfg.ConcurrentExecution.Enabled {
		return 1
	}
	max := a.deps.Core.Cfg.Execution.MaxParallelTools
	if max <= 0 {
		max = a.deps.Core.ToolsCfg.ConcurrentExecution.MaxConcurrency
	}
	if max <= 0 {
		max = 4
	}
	return max
}

func (a *Agent) classifyToolCall(call mind.ToolUseBlock) scheduledToolCall {
	sc := scheduledToolCall{call: call, safety: tool.ParallelNever}
	t, err := a.deps.Core.Tools.Get(call.Name)
	if err != nil {
		return sc
	}
	caps := tool.GetCapabilities(t)
	sc.safety = caps.ParallelSafety
	if sc.safety == "" {
		sc.safety = tool.ParallelNever
	}
	if sc.safety != tool.ParallelPathScoped {
		return sc
	}
	pathTool, ok := t.(tool.PathScopedTool)
	if !ok {
		sc.safety = tool.ParallelNever
		return sc
	}
	paths, err := pathTool.ExtractPaths([]byte(call.Input))
	if err != nil {
		sc.safety = tool.ParallelNever
		return sc
	}
	sc.paths = paths
	return sc
}

func pathConflicts(existing map[string]bool, paths []string) bool {
	for _, p := range paths {
		if existing[p] {
			return true
		}
	}
	return false
}
