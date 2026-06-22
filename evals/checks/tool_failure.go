package checks

import "strings"

// FailureClass categorizes a tool-call failure by its locus. The headline
// "tool failure rate" is misleading until decomposed: a governance denial (the
// approval interceptor refusing an approval-gated tool in an autonomous episode)
// is not an agent mistake, and scoring agent quality off the raw failure count
// measures the wrong thing. See evals/error-analysis-v1.md §2.
type FailureClass int

const (
	// ClassOK means the call did not fail (empty error string).
	ClassOK FailureClass = iota
	// ClassGovernanceDenied means the approval interceptor denied the call.
	ClassGovernanceDenied
	// ClassAgentError means the agent's own mistake (bad path, wrong tool).
	ClassAgentError
	// ClassEnvError means an environment/tooling limit (timeout, etc.).
	ClassEnvError
)

// String renders the class as a short stable label.
func (c FailureClass) String() string {
	switch c {
	case ClassOK:
		return "ok"
	case ClassGovernanceDenied:
		return "governance-denied"
	case ClassAgentError:
		return "agent-error"
	case ClassEnvError:
		return "env-error"
	default:
		return "unknown"
	}
}

// ClassifyToolError maps a tool-result error string to a FailureClass. An empty
// string is ClassOK. Matching is deliberately conservative: anything not
// recognized as a denial or an environment limit is attributed to the agent, so
// the agent-error bucket never under-counts.
func ClassifyToolError(errStr string) FailureClass {
	e := strings.ToLower(strings.TrimSpace(errStr))
	switch {
	case e == "":
		return ClassOK
	case strings.Contains(e, "denied"):
		return ClassGovernanceDenied
	case strings.Contains(e, "timed out"),
		strings.Contains(e, "timeout"),
		strings.Contains(e, "context deadline"):
		return ClassEnvError
	default:
		return ClassAgentError
	}
}

// ToolFailure is one failed tool call: the tool name and its error string.
type ToolFailure struct {
	Tool  string
	Error string
}

// FailureSummary decomposes a set of tool failures by class, and — for
// governance denials — by tool name (the FM-1 signal: which approval-gated
// tools are being denied in autonomous episodes).
type FailureSummary struct {
	Total            int
	GovernanceDenied int
	AgentError       int
	EnvError         int
	DeniedByTool     map[string]int
}

// SummarizeFailures classifies every failure and tallies counts. ClassOK
// entries (empty error) are skipped, so Total counts only real failures.
func SummarizeFailures(failures []ToolFailure) FailureSummary {
	s := FailureSummary{DeniedByTool: map[string]int{}}
	for _, f := range failures {
		class := ClassifyToolError(f.Error)
		if class == ClassOK {
			continue
		}
		s.Total++
		switch class {
		case ClassGovernanceDenied:
			s.GovernanceDenied++
			s.DeniedByTool[f.Tool]++
		case ClassAgentError:
			s.AgentError++
		case ClassEnvError:
			s.EnvError++
		}
	}
	return s
}
