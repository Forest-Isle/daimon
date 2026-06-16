package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/world"
)

func TestBuildDailyBriefEmpty(t *testing.T) {
	now := time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC)
	got := buildDailyBrief(now, nil, nil, nil)

	for _, want := range []string{
		"**每日早报** — 2026-06-16 09:30  (过去 24h)",
		"（无活动记录）",
		"（无待决提案）",
		"（无待审批项）",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("brief missing %q:\n%s", want, got)
		}
	}
}

func TestBuildDailyBriefPopulated(t *testing.T) {
	now := time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC)
	got := buildDailyBrief(now,
		[]world.JournalEntry{
			{Kind: "outcome", Summary: " first "},
			{Kind: "note", Summary: "second"},
		},
		[]proposals.Proposal{
			{Title: "Review plan", Body: " Ship the next step. ", Urgency: 2},
		},
		[]action.Hold{
			{ToolName: "shell", ExecuteAt: "2026-06-16 10:00:00"},
		},
	)

	for _, want := range []string{
		"- [outcome] first",
		"- [note] second",
		"- Review plan (urgency 2)",
		"  Ship the next step.",
		"- shell — 执行于 2026-06-16 10:00:00",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("brief missing %q:\n%s", want, got)
		}
	}

	activity := strings.Index(got, "## 过去 24h 活动")
	proposalsSection := strings.Index(got, "## 提案队列")
	holds := strings.Index(got, "## 待审批")
	if !(activity >= 0 && proposalsSection > activity && holds > proposalsSection) {
		t.Fatalf("section order is wrong:\n%s", got)
	}

	first := strings.Index(got, "- [outcome] first")
	second := strings.Index(got, "- [note] second")
	if !(first >= 0 && second > first) {
		t.Fatalf("entry order is wrong:\n%s", got)
	}
}

func TestBuildDailyBriefOverflow(t *testing.T) {
	now := time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC)
	entries := make([]world.JournalEntry, 25)
	for i := range entries {
		entries[i] = world.JournalEntry{Kind: "event", Summary: fmt.Sprintf("entry %02d", i+1)}
	}

	got := buildDailyBrief(now, entries, nil, nil)
	if strings.Count(got, "- [event]") != 20 {
		t.Fatalf("expected 20 rendered entries:\n%s", got)
	}
	if !strings.Contains(got, "- …(+5 条)") {
		t.Fatalf("brief missing overflow line:\n%s", got)
	}
}

func TestBuildDailyBriefEmptySummary(t *testing.T) {
	now := time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC)
	got := buildDailyBrief(now, []world.JournalEntry{{Kind: "note"}}, nil, nil)

	if !strings.Contains(got, "- [note] (无摘要)") {
		t.Fatalf("brief missing empty summary fallback:\n%s", got)
	}
}
