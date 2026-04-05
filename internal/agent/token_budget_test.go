package agent

import (
	"strings"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

func TestTokenBudgetCheck(t *testing.T) {
	cases := []struct {
		name           string
		systemPrompt   string
		messages       []session.Message
		expectedAction BudgetAction
	}{
		{
			name:         "low usage — no action",
			systemPrompt: "You are a helpful assistant.",
			messages: []session.Message{
				{Content: "Hello"},
				{Content: "Hi there!"},
			},
			expectedAction: BudgetOK,
		},
		{
			name:         "light threshold",
			systemPrompt: strings.Repeat("a", 100_000),
			messages: func() []session.Message {
				// 500K chars spread across 10 messages (50K each)
				msgs := make([]session.Message, 10)
				for i := range msgs {
					msgs[i] = session.Message{Content: strings.Repeat("b", 50_000)}
				}
				return msgs
			}(),
			// totalChars ≈ 100000 + 500000 + 10*20 = 600200
			// ratio = 600200 * 0.25 / 200000 ≈ 0.75 → BudgetCompressLight
			expectedAction: BudgetCompressLight,
		},
		{
			name:         "heavy threshold",
			systemPrompt: strings.Repeat("a", 200_000),
			messages: func() []session.Message {
				// 1M chars spread across 10 messages (100K each)
				msgs := make([]session.Message, 10)
				for i := range msgs {
					msgs[i] = session.Message{Content: strings.Repeat("b", 100_000)}
				}
				return msgs
			}(),
			// totalChars ≈ 200000 + 1000000 + 10*20 = 1200200
			// ratio = 1200200 * 0.25 / 200000 ≈ 1.50 → BudgetCompressHeavy
			expectedAction: BudgetCompressHeavy,
		},
	}

	tb := NewTokenBudget(0, 0, 0, 0, 0) // use all defaults

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tb.Check(tc.messages, tc.systemPrompt)
			if result.Action != tc.expectedAction {
				t.Errorf("expected action %s, got %s (ratio=%.4f, totalChars=%d)",
					tc.expectedAction, result.Action, result.UsageRatio, result.TotalChars)
			}
		})
	}
}

func TestTokenBudgetDefaultsApplied(t *testing.T) {
	tb := NewTokenBudget(0, 0, 0, 0, 0)

	if tb.ModelLimit != 200000 {
		t.Errorf("expected ModelLimit=200000, got %d", tb.ModelLimit)
	}
	if tb.LightThreshold != 0.70 {
		t.Errorf("expected LightThreshold=0.70, got %f", tb.LightThreshold)
	}
	if tb.MediumThreshold != 0.80 {
		t.Errorf("expected MediumThreshold=0.80, got %f", tb.MediumThreshold)
	}
	if tb.HeavyThreshold != 0.90 {
		t.Errorf("expected HeavyThreshold=0.90, got %f", tb.HeavyThreshold)
	}
	if tb.EstimateRatio != 0.25 {
		t.Errorf("expected EstimateRatio=0.25, got %f", tb.EstimateRatio)
	}
}
