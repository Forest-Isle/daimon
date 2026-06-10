package agent

type DepsBuilder struct {
	Core       CoreDeps
	Memory     MemoryDeps
	Security   SecurityDeps
	MultiAgent MultiAgentDeps
}

func NewDepsBuilder() *DepsBuilder { return &DepsBuilder{} }

func (b *DepsBuilder) Build() AgentDeps {
	return AgentDeps{
		Core: b.Core, Memory: b.Memory, Security: b.Security,
		MultiAgent: b.MultiAgent,
	}.WithDefaults()
}
