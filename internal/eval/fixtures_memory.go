package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// evalMemEntry builds a memory.Entry with eval-user scope so the PERCEIVE
// phase can find it during an eval task (the runner uses UserID "eval_user").
func evalMemEntry(id, content string) memory.Entry {
	now := time.Now()
	return memory.Entry{
		ID:        id,
		Scope:     memory.ScopeUser,
		UserID:    "eval_user", // must match the UserID in CognitiveAgentRunner.RunTask
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// injectAndTrack writes entries via MemoryAwareRunner and returns a
// CleanupWithRunner func that deletes those IDs when the task finishes.
// If runner does not implement MemoryAwareRunner the task is skipped with
// an explanatory error so the suite reports the misconfiguration clearly.
func injectAndTrack(entries ...memory.Entry) (
	setup func(context.Context, AgentRunner) error,
	cleanup func(context.Context, AgentRunner) error,
) {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}

	setup = func(ctx context.Context, runner AgentRunner) error {
		mar, ok := runner.(MemoryAwareRunner)
		if !ok {
			return fmt.Errorf("runner does not implement MemoryAwareRunner; use CognitiveAgentRunner for memory eval tasks")
		}
		return mar.InjectMemory(ctx, entries...)
	}

	cleanup = func(ctx context.Context, runner AgentRunner) error {
		mar, ok := runner.(MemoryAwareRunner)
		if !ok {
			return nil
		}
		return mar.CleanupMemory(ctx, ids...)
	}

	return setup, cleanup
}

// MemorySuite returns tasks that evaluate the agent's memory system —
// storage, retrieval, relevance, and cross-session persistence.
//
// SetupWithRunner pre-populates the agent's live memory store via
// MemoryAwareRunner; CleanupWithRunner removes those entries afterwards.
// Both hooks require a CognitiveAgentRunner (or any MemoryAwareRunner).
func MemorySuite() []TaskCase {
	// mem-store-recall -------------------------------------------------------
	recallSetup, recallCleanup := injectAndTrack(
		evalMemEntry("eval-recall-001", "User's cat name is Muffin"),
	)

	// mem-relevance ----------------------------------------------------------
	relSetup, relCleanup := injectAndTrack(
		evalMemEntry("eval-rel-001", "User likes coffee"),
		evalMemEntry("eval-rel-002", "User's dog is named Rex"),
		evalMemEntry("eval-rel-003", "User works at Google"),
		evalMemEntry("eval-rel-004", "User drives a Tesla"),
		evalMemEntry("eval-rel-005", "User's birthday is March 15"),
	)

	// mem-update -------------------------------------------------------------
	updateSetup, updateCleanup := injectAndTrack(
		// old entry — timestamped 24 h ago so recency ranking deprioritises it
		memory.Entry{
			ID:        "eval-update-001",
			Scope:     memory.ScopeUser,
			UserID:    "eval_user",
			Content:   "User used to live in New York",
			CreatedAt: time.Now().Add(-24 * time.Hour),
			UpdatedAt: time.Now().Add(-24 * time.Hour),
		},
		// new entry — current timestamp
		evalMemEntry("eval-update-002", "User recently moved to San Francisco"),
	)

	// mem-profile-use --------------------------------------------------------
	profileSetup, profileCleanup := injectAndTrack(
		evalMemEntry("eval-profile-001", "User prefers terminal-based tools"),
		evalMemEntry("eval-profile-002", "User uses vim keybindings"),
	)

	// mem-cross-session ------------------------------------------------------
	crossSetup, crossCleanup := injectAndTrack(
		evalMemEntry("eval-cross-001", "User is learning Go programming"),
		evalMemEntry("eval-cross-002", "User is working on a web server project in Go"),
	)

	return []TaskCase{
		{
			ID:           "mem-store-recall",
			Goal:         "What is my cat's name?",
			Complexity:   "simple",
			Tags:         []string{"memory", "recall"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference:    &Reference{MustContain: []string{"Muffin"}},
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "accuracy", Description: "Did the agent correctly recall the cat's name as Muffin?", Weight: 1.0},
			}},
			SetupWithRunner:   recallSetup,
			CleanupWithRunner: recallCleanup,
		},
		{
			ID:           "mem-relevance",
			Goal:         "What pet do I have?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "relevance"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain:    []string{"Rex"},
				MustNotContain: []string{"coffee", "Tesla", "Google", "March"},
			},
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "precision", Description: "Did the agent return only pet-related info (Rex) without dumping all memories?", Weight: 0.6},
				{Name: "accuracy", Description: "Did it correctly identify Rex as the pet?", Weight: 0.4},
			}},
			SetupWithRunner:   relSetup,
			CleanupWithRunner: relCleanup,
		},
		{
			ID:           "mem-update",
			Goal:         "Where do I currently live?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "update"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference:    &Reference{MustContain: []string{"San Francisco"}},
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "recency", Description: "Did the agent return the updated location (San Francisco) not the old one?", Weight: 0.7},
				{Name: "acknowledgment", Description: "Did it acknowledge the move/update?", Weight: 0.3},
			}},
			SetupWithRunner:   updateSetup,
			CleanupWithRunner: updateCleanup,
		},
		{
			ID:           "mem-profile-use",
			Goal:         "Suggest a text editor for me to use.",
			Complexity:   "moderate",
			Tags:         []string{"memory", "profile", "personalization"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "personalization", Description: "Does the suggestion reflect terminal-based preference (e.g., neovim, helix, kakoune)?", Weight: 0.5},
				{Name: "vim_awareness", Description: "Does it consider vim keybinding preference?", Weight: 0.3},
				{Name: "reasoning", Description: "Is the recommendation well-reasoned?", Weight: 0.2},
			}},
			SetupWithRunner:   profileSetup,
			CleanupWithRunner: profileCleanup,
		},
		{
			ID:           "mem-no-hallucinate",
			Goal:         "What is my favorite movie? (Note: I have never told you about my movie preferences.)",
			Complexity:   "simple",
			Tags:         []string{"memory", "hallucination"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			// No setup/cleanup — intentionally tests that the agent admits
			// ignorance when no relevant memory exists.
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "honesty", Description: "Did the agent admit it doesn't know rather than making up an answer?", Weight: 0.7},
				{Name: "no_fabrication", Description: "Did it avoid fabricating a specific movie title as if from memory?", Weight: 0.3},
			}},
		},
		{
			ID:           "mem-cross-session",
			Goal:         "Based on what you know about me from our previous conversations, what Go resources would you recommend for my project?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "cross-session"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "context_awareness", Description: "Does the agent use retrieved memory about Go learning and the web server project?", Weight: 0.4},
				{Name: "relevance", Description: "Are the recommendations relevant to Go web server development?", Weight: 0.4},
				{Name: "honesty", Description: "If memory is unavailable, does it say so rather than pretending?", Weight: 0.2},
			}},
			SetupWithRunner:   crossSetup,
			CleanupWithRunner: crossCleanup,
		},
	}
}
