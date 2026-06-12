package tool

import (
	"fmt"
	"path/filepath"
	"strings"
)

type ShellPolicyFinding struct {
	Blocked bool
	Matched string
	Reason  string
}

func AnalyzeShellCommand(command string, blockedCommands []string) ShellPolicyFinding {
	command = strings.TrimSpace(command)
	if command == "" {
		return ShellPolicyFinding{}
	}
	if strings.Contains(command, ":(){") || strings.Contains(command, ":|:") {
		return ShellPolicyFinding{Blocked: true, Matched: "fork_bomb", Reason: "dangerous shell function pattern"}
	}

	for _, sub := range shellCommandSubstitutions(command) {
		if finding := AnalyzeShellCommand(sub, blockedCommands); finding.Blocked {
			finding.Reason = "command substitution: " + finding.Reason
			return finding
		}
	}

	segments := splitShellSegments(command)
	commands := make([][]string, 0, len(segments))
	for _, segment := range segments {
		fields := normalizeShellCommand(shellFields(segment))
		if len(fields) == 0 {
			continue
		}
		commands = append(commands, fields)
		if finding := matchConfiguredBlocked(fields, blockedCommands); finding.Blocked {
			return finding
		}
	}

	for i, fields := range commands {
		var next []string
		if i+1 < len(commands) {
			next = commands[i+1]
		}
		if finding := dangerousShellCommand(fields, next); finding.Blocked {
			return finding
		}
	}

	return ShellPolicyFinding{}
}

func matchConfiguredBlocked(fields []string, blockedCommands []string) ShellPolicyFinding {
	for _, blocked := range blockedCommands {
		blockedFields := normalizeShellCommand(shellFields(blocked))
		if len(blockedFields) == 0 {
			continue
		}
		if len(blockedFields) > len(fields) {
			continue
		}
		matched := true
		for i := range blockedFields {
			if !shellTokenMatches(blockedFields[i], fields[i]) {
				matched = false
				break
			}
		}
		if matched {
			return ShellPolicyFinding{
				Blocked: true,
				Matched: blocked,
				Reason:  "configured blocked command",
			}
		}
	}
	return ShellPolicyFinding{}
}

func shellTokenMatches(pattern, value string) bool {
	if pattern == value {
		return true
	}
	if matched, _ := filepath.Match(pattern, value); matched {
		return true
	}
	return false
}

func dangerousShellCommand(fields, next []string) ShellPolicyFinding {
	if len(fields) == 0 {
		return ShellPolicyFinding{}
	}
	cmd := filepath.Base(fields[0])
	switch cmd {
	case "shutdown", "reboot", "poweroff", "halt":
		return ShellPolicyFinding{Blocked: true, Matched: cmd, Reason: "host power command"}
	case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs", "fdisk", "parted":
		return ShellPolicyFinding{Blocked: true, Matched: cmd, Reason: "disk partition or format command"}
	case "diskutil":
		if len(fields) > 1 && strings.EqualFold(fields[1], "erasedisk") {
			return ShellPolicyFinding{Blocked: true, Matched: "diskutil eraseDisk", Reason: "disk erase command"}
		}
	case "rm":
		if shellHasRecursiveFlag(fields[1:]) && shellHasForceFlag(fields[1:]) {
			for _, target := range shellNonFlagArgs(fields[1:]) {
				if isCriticalShellTarget(target) {
					return ShellPolicyFinding{Blocked: true, Matched: "rm -rf " + target, Reason: "recursive forced removal of critical path"}
				}
			}
		}
	case "chmod", "chown":
		if shellHasRecursiveFlag(fields[1:]) {
			for _, target := range shellNonFlagArgs(fields[1:]) {
				if isCriticalShellTarget(target) {
					return ShellPolicyFinding{Blocked: true, Matched: cmd + " -R " + target, Reason: "recursive permission change on critical path"}
				}
			}
		}
	case "dd":
		for _, arg := range fields[1:] {
			if strings.HasPrefix(arg, "of=/dev/") || strings.HasPrefix(arg, "of=/") {
				return ShellPolicyFinding{Blocked: true, Matched: "dd " + arg, Reason: "raw disk or absolute output target"}
			}
		}
	case "find":
		if shellContains(fields[1:], "-delete") {
			for _, target := range shellNonFlagArgs(fields[1:]) {
				if isCriticalShellTarget(target) {
					return ShellPolicyFinding{Blocked: true, Matched: "find " + target + " -delete", Reason: "delete traversal on critical path"}
				}
			}
		}
	case "curl", "wget":
		if len(next) > 0 && isShellInterpreter(filepath.Base(next[0])) {
			return ShellPolicyFinding{Blocked: true, Matched: fmt.Sprintf("%s | %s", cmd, filepath.Base(next[0])), Reason: "download piped to shell"}
		}
	}
	return ShellPolicyFinding{}
}

func splitShellSegments(s string) []string {
	var segments []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			current.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			current.WriteRune(r)
		case ';', '|', '&', '\n':
			if segment := strings.TrimSpace(current.String()); segment != "" {
				segments = append(segments, segment)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if segment := strings.TrimSpace(current.String()); segment != "" {
		segments = append(segments, segment)
	}
	return segments
}

func shellFields(s string) []string {
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}

func normalizeShellCommand(fields []string) []string {
	for len(fields) > 0 {
		switch filepath.Base(fields[0]) {
		case "sudo", "command", "builtin", "nohup", "time":
			fields = fields[1:]
		case "env":
			fields = fields[1:]
			for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
				fields = fields[1:]
			}
		default:
			return fields
		}
	}
	return fields
}

func shellCommandSubstitutions(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '(' {
			if sub, end, ok := readBalancedShellSubstitution(s, i+2); ok {
				out = append(out, sub)
				i = end
			}
			continue
		}
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				out = append(out, s[i+1:i+1+end])
				i = i + end + 1
			}
		}
	}
	return out
}

func readBalancedShellSubstitution(s string, start int) (string, int, bool) {
	depth := 1
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[start:i], i, true
			}
		}
	}
	return "", 0, false
}

func shellHasRecursiveFlag(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			if arg == "--recursive" {
				return true
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && strings.ContainsAny(arg, "rR") {
			return true
		}
	}
	return false
}

func shellHasForceFlag(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			if arg == "--force" {
				return true
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && strings.Contains(arg, "f") {
			return true
		}
	}
	return false
}

func shellNonFlagArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func shellContains(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func isCriticalShellTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "/" {
		return true
	}
	target = strings.TrimSuffix(target, "/")
	switch target {
	case "", ".", "..":
		return false
	case "/", "/*", "~", "~/*", "$HOME", "$HOME/*":
		return true
	}
	criticalPrefixes := []string{"/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/private", "/sbin", "/System", "/usr", "/var"}
	for _, prefix := range criticalPrefixes {
		if target == prefix || strings.HasPrefix(target, prefix+"/") || target == prefix+"/*" {
			return true
		}
	}
	return false
}

func isShellInterpreter(cmd string) bool {
	switch cmd {
	case "sh", "bash", "zsh", "fish", "dash", "ksh":
		return true
	default:
		return false
	}
}
