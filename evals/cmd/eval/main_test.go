package main

import (
	"testing"

	"github.com/Forest-Isle/daimon/evals/checks"
	"github.com/Forest-Isle/daimon/evals/runner"
)

func TestCodingGateSelfCheckPasses(t *testing.T) {
	ok, reasons := codingGateSelfCheck()
	if !ok {
		t.Fatalf("self-check must pass on a healthy eval; reasons: %v", reasons)
	}
}

func TestToScorecard(t *testing.T) {
	r := runner.CorpusResult{
		Sessions:  52,
		ToolCalls: 95,
		Salvaged:  1,
		Failures: checks.FailureSummary{
			Total:            39,
			GovernanceDenied: 33,
			AgentError:       5,
			EnvError:         1,
			DeniedByTool:     map[string]int{"memory": 26},
		},
	}
	sc := toScorecard(r)
	if sc.Sessions != 52 || sc.Failures != 39 || sc.GovernanceDenied != 33 {
		t.Fatalf("projection wrong: %+v", sc)
	}
	if sc.DeniedByTool["memory"] != 26 {
		t.Fatalf("by-tool map not carried: %+v", sc.DeniedByTool)
	}
}
