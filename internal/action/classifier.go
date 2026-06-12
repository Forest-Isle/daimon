package action

import (
	"encoding/json"
	"strings"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// Classifier assigns a reversibility Class to a tool call. The governed flag is
// false for read-only tools, which carry no autonomy implications and are not
// recorded in the trust ledger.
type Classifier interface {
	Classify(call *tool.ToolCall) (class Class, governed bool)
	ContextKey(call *tool.ToolCall) string
}

type defaultClassifier struct{}

// NewClassifier returns the default reversibility classifier.
func NewClassifier() Classifier { return defaultClassifier{} }

func (defaultClassifier) Classify(call *tool.ToolCall) (Class, bool) {
	if call == nil {
		return Reversible, false
	}
	if call.Capabilities.IsReadOnly {
		return Reversible, false
	}
	// bash is classified by its command, since a single tool spans the full
	// reversibility range. The permission engine still does the real gating;
	// this heuristic only feeds the trust record.
	if call.ToolName == "bash" {
		if bashLooksDestructive(call.Input) {
			return Irreversible, true
		}
		return Reversible, true
	}
	if call.Capabilities.IsDestructive {
		return Irreversible, true
	}
	return Reversible, true
}

func (defaultClassifier) ContextKey(call *tool.ToolCall) string {
	if call == nil {
		return ""
	}
	return call.ToolName
}

// bashLooksDestructive is a conservative heuristic over the bash command. It is
// not a security boundary (the permission policy is) — it only decides whether a
// command earns the cautious Irreversible classification for trust accounting.
// A future task can replace it with a shell AST parse.
func bashLooksDestructive(input string) bool {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return true
	}
	lower := strings.ToLower(in.Command)
	for _, marker := range []string{"rm ", "rm-", "rmdir", "dd ", "mkfs", "shred", "truncate", "> /dev", ":(){", "fdisk", "format ", "git push --force", "drop table", "drop database"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
