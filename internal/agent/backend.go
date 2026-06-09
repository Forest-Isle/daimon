package agent

// BackendType identifies which execution backend to use.
// Only in-process execution is supported; the field exists in AgentSpec so
// specs can declare it explicitly and be validated.
type BackendType string

const (
	BackendInProcess BackendType = "in_process" // goroutine (default)
)

