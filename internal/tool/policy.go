package tool

import "strings"

// Policy checks whether a tool execution should be allowed.
type Policy struct {
	blockedCommands []string
}

func NewPolicy(blockedCommands []string) *Policy {
	return &Policy{blockedCommands: blockedCommands}
}

// CheckBashCommand returns an error message if the command is blocked, or empty string if allowed.
func (p *Policy) CheckBashCommand(cmd string) string {
	normalized := strings.TrimSpace(cmd)
	for _, blocked := range p.blockedCommands {
		if strings.Contains(normalized, blocked) {
			return "command blocked by security policy: " + blocked
		}
	}
	return ""
}
