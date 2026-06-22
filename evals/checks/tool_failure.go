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
	// ClassGovernanceDenied means a governance layer (approval interceptor,
	// security policy, or value gate) refused the call.
	ClassGovernanceDenied
	// ClassAgentError means the agent's own mistake (bad path, wrong tool).
	ClassAgentError
	// ClassEnvError means an environment/tooling limit (timeout, etc.).
	ClassEnvError
	// ClassUnknown means the call failed but its error could not be read
	// (missing or undecodable result payload) — kept out of agent-error so a
	// telemetry gap never inflates the agent's mistake count.
	ClassUnknown
)

// UndecodableError is the sentinel a caller supplies when a failed call's result
// payload is absent or could not be decoded. It classifies as ClassUnknown.
const UndecodableError = "<undecodable tool result>"

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
	case ClassUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ClassifyToolError maps a tool-result error string to a FailureClass. An empty
// string is ClassOK and the undecodable sentinel is ClassUnknown. Governance and
// agent-path patterns are matched before the environment patterns so an agent
// path error that merely contains a timeout-like substring (e.g. a file named
// "timeout") is not miscounted as an environment limit. Anything unrecognized is
// attributed to the agent, so the agent-error bucket never under-counts.
func ClassifyToolError(errStr string) FailureClass {
	e := strings.ToLower(strings.TrimSpace(errStr))
	switch {
	case e == "":
		return ClassOK
	case e == UndecodableError:
		return ClassUnknown
	case isGovernanceDenial(e):
		return ClassGovernanceDenied
	case isAgentPathError(e):
		return ClassAgentError
	case isEnvLimit(e):
		return ClassEnvError
	default:
		return ClassAgentError
	}
}

// isGovernanceDenial matches the denial strings daimon's governance layers emit:
// the approval interceptor ("execution denied by user"), the shell security
// policy ("command blocked by security policy"), and the value gate ("action
// blocked by value gate"). All are lowercased before matching.
func isGovernanceDenial(e string) bool {
	return strings.Contains(e, "denied") ||
		strings.Contains(e, "blocked by security policy") ||
		strings.Contains(e, "blocked by value gate")
}

// isAgentPathError matches filesystem mistakes the agent itself made.
func isAgentPathError(e string) bool {
	return strings.Contains(e, "is a directory") ||
		strings.Contains(e, "no such file") ||
		strings.Contains(e, "no such")
}

// isEnvLimit matches environment/tooling limits. It deliberately matches the
// full phrase "timed out" / "deadline exceeded" rather than the bare token
// "timeout", which can appear inside an unrelated file path.
func isEnvLimit(e string) bool {
	return strings.Contains(e, "timed out") ||
		strings.Contains(e, "deadline exceeded")
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
	Unknown          int
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
		case ClassUnknown:
			s.Unknown++
		}
	}
	return s
}
