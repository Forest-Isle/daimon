package memory

import (
	"fmt"
	"strings"
)

// PromptSections holds formatted prompt text for each memory type.
type PromptSections struct {
	MemoryLines    []string // episodic memories
	KnowledgeLines []string // semantic knowledge
	StrategyLines  []string // procedural strategies
	Combined       string   // all sections combined
}

// BuildPromptSection formats unified memories into a prompt section.
func (ur *UnifiedRetriever) BuildPromptSection(memories []*UnifiedMemory) *PromptSections {
	sections := &PromptSections{}
	for _, mem := range memories {
		if mem == nil {
			continue
		}
		line := truncateLine(mem.Content, 240)
		switch {
		case mem.Type == Procedural:
			sections.StrategyLines = append(sections.StrategyLines, "- "+line)
		case mem.Type == Semantic:
			sections.KnowledgeLines = append(sections.KnowledgeLines, "- "+line)
		default:
			sections.MemoryLines = append(sections.MemoryLines, "- "+line)
		}
	}
	sections.Combined = sections.Format()
	return sections
}

func (ps *PromptSections) Format() string {
	if ps == nil {
		return ""
	}

	var blocks []string
	appendBlock := func(header string, lines []string) {
		if len(lines) == 0 {
			return
		}
		blocks = append(blocks, fmt.Sprintf("%s\n%s", header, strings.Join(lines, "\n")))
	}

	appendBlock("## Relevant Memories", ps.MemoryLines)
	appendBlock("## Knowledge Context", ps.KnowledgeLines)
	appendBlock("## Past Successful Strategies", ps.StrategyLines)

	return strings.Join(blocks, "\n\n")
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
