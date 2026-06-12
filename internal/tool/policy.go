package tool

// Policy checks whether a tool execution should be allowed.
type Policy struct {
	blockedCommands []string
}

func NewPolicy(blockedCommands []string) *Policy {
	return &Policy{blockedCommands: blockedCommands}
}

// CheckBashCommand returns an error message if the command is blocked, or empty string if allowed.
func (p *Policy) CheckBashCommand(cmd string) string {
	if p == nil {
		return ""
	}
	finding := AnalyzeShellCommand(cmd, p.blockedCommands)
	if finding.Blocked {
		if finding.Matched != "" {
			return "command blocked by security policy: " + finding.Matched
		}
		return "command blocked by security policy: " + finding.Reason
	}
	return ""
}
