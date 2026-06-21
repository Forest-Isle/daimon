package action

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/tool"
)

func TestHoldAwareClassifierClassifiesMemoryOperations(t *testing.T) {
	c := NewClassifierWithCompensableHTTP()
	caps := tool.ToolCapabilities{IsDestructive: true}

	tests := []struct {
		name       string
		input      string
		wantClass  Class
		wantGovern bool
	}{
		{name: "search", input: `{"operation":"search"}`, wantClass: Reversible, wantGovern: false},
		{name: "list", input: `{"operation":"list"}`, wantClass: Reversible, wantGovern: false},
		{name: "save", input: `{"operation":"save"}`, wantClass: Irreversible, wantGovern: true},
		{name: "delete", input: `{"operation":"delete"}`, wantClass: Irreversible, wantGovern: true},
		{name: "unknown", input: `{"operation":"unknown"}`, wantClass: Irreversible, wantGovern: true},
		{name: "malformed", input: `{`, wantClass: Irreversible, wantGovern: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, governed := c.Classify(&tool.ToolCall{
				ToolName:     "memory",
				Input:        tt.input,
				Capabilities: caps,
			})
			if class != tt.wantClass || governed != tt.wantGovern {
				t.Fatalf("Classify() = (%v, %v), want (%v, %v)", class, governed, tt.wantClass, tt.wantGovern)
			}
		})
	}
}

func TestHoldAwareClassifierClassifiesValuesOperations(t *testing.T) {
	c := NewClassifierWithCompensableHTTP()
	caps := tool.ToolCapabilities{IsDestructive: true}

	tests := []struct {
		name       string
		input      string
		wantClass  Class
		wantGovern bool
	}{
		{name: "list", input: `{"operation":"list"}`, wantClass: Reversible, wantGovern: false},
		{name: "record", input: `{"operation":"record"}`, wantClass: Irreversible, wantGovern: true},
		{name: "unknown", input: `{"operation":"unknown"}`, wantClass: Irreversible, wantGovern: true},
		{name: "malformed", input: `{`, wantClass: Irreversible, wantGovern: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, governed := c.Classify(&tool.ToolCall{
				ToolName:     "values",
				Input:        tt.input,
				Capabilities: caps,
			})
			if class != tt.wantClass || governed != tt.wantGovern {
				t.Fatalf("Classify() = (%v, %v), want (%v, %v)", class, governed, tt.wantClass, tt.wantGovern)
			}
		})
	}
}
