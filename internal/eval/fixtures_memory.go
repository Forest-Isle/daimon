package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// memEvalHarness holds state shared between a task's SetupFunc and CleanupFunc.
// It creates an isolated memory store in a temp directory so eval runs never
// touch the real user's ~/.ironclaw/memory/ tree.
type memEvalHarness struct {
	dir string
	db  *store.DB
	mem *memory.FileMemoryStore
}

// newMemEvalHarness creates an isolated memory store in a temporary directory.
// The caller must call cleanup() when done.
func newMemEvalHarness() (*memEvalHarness, error) {
	dir, err := os.MkdirTemp("", "ironclaw-eval-memory-*")
	if err != nil {
		return nil, fmt.Errorf("create eval memory temp dir: %w", err)
	}
	db, err := store.Open(filepath.Join(dir, "eval.db"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("open eval memory db: %w", err)
	}
	memDir := filepath.Join(dir, "memory")
	mem, err := memory.NewFileMemoryStore(memDir, db.DB, &memory.NoopEmbedding{}, memory.MemoryConfig{})
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("create eval file memory store: %w", err)
	}
	return &memEvalHarness{dir: dir, db: db, mem: mem}, nil
}

func (h *memEvalHarness) cleanup() error {
	if h.db != nil {
		_ = h.db.Close()
	}
	return os.RemoveAll(h.dir)
}

// memEntry builds an Entry with sensible defaults for eval tests.
// All entries use scope=user and userID="eval_test_user".
func memEntry(id, content string) memory.Entry {
	now := time.Now()
	return memory.Entry{
		ID:        id,
		Scope:     memory.ScopeUser,
		UserID:    "eval_test_user",
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// MemorySuite returns tasks that evaluate the agent's memory system —
// storage, retrieval, relevance, and cross-session persistence.
// SetupFunc pre-populates isolated memory stores; CleanupFunc removes them.
func MemorySuite() []TaskCase {
	return []TaskCase{
		// mem-store-recall: agent must retrieve a single stored fact -------------------
		func() TaskCase {
			var h *memEvalHarness
			return TaskCase{
				ID:           "mem-store-recall",
				Goal:         "What is my cat's name?",
				Complexity:   "simple",
				Tags:         []string{"memory", "recall"},
				Dimension:    DimMemory,
				VerifyMethod: VerifyHybrid,
				Reference: &Reference{
					MustContain: []string{"Muffin"},
				},
				Rubric: &Rubric{
					Criteria: []JudgeCriterion{
						{Name: "accuracy", Description: "Did the agent correctly recall the cat's name as Muffin?", Weight: 1.0},
					},
				},
				SetupFunc: func() error {
					var err error
					h, err = newMemEvalHarness()
					if err != nil {
						return err
					}
					return h.mem.Save(
						context.Background(),
						memEntry("eval-recall-001", "User's cat name is Muffin"),
					)
				},
				CleanupFunc: func() error {
					if h != nil {
						return h.cleanup()
					}
					return nil
				},
			}
		}(),

		// mem-relevance: agent must return only the pet-related fact from many stored facts
		func() TaskCase {
			var h *memEvalHarness
			return TaskCase{
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
				Rubric: &Rubric{
					Criteria: []JudgeCriterion{
						{Name: "precision", Description: "Did the agent return only pet-related info (Rex) without dumping all memories?", Weight: 0.6},
						{Name: "accuracy", Description: "Did it correctly identify Rex as the pet?", Weight: 0.4},
					},
				},
				SetupFunc: func() error {
					var err error
					h, err = newMemEvalHarness()
					if err != nil {
						return err
					}
					ctx := context.Background()
					facts := []memory.Entry{
						memEntry("eval-rel-001", "User likes coffee"),
						memEntry("eval-rel-002", "User's dog is named Rex"),
						memEntry("eval-rel-003", "User works at Google"),
						memEntry("eval-rel-004", "User drives a Tesla"),
						memEntry("eval-rel-005", "User's birthday is March 15"),
					}
					for _, f := range facts {
						if err := h.mem.Save(ctx, f); err != nil {
							return fmt.Errorf("setup mem-relevance: %w", err)
						}
					}
					return nil
				},
				CleanupFunc: func() error {
					if h != nil {
						return h.cleanup()
					}
					return nil
				},
			}
		}(),

		// mem-update: agent must return the most-recent location, not the stale one ------
		func() TaskCase {
			var h *memEvalHarness
			return TaskCase{
				ID:           "mem-update",
				Goal:         "Where do I currently live?",
				Complexity:   "moderate",
				Tags:         []string{"memory", "update"},
				Dimension:    DimMemory,
				VerifyMethod: VerifyHybrid,
				Reference: &Reference{
					MustContain: []string{"San Francisco"},
				},
				Rubric: &Rubric{
					Criteria: []JudgeCriterion{
						{Name: "recency", Description: "Did the agent return the updated location (San Francisco) not the old one?", Weight: 0.7},
						{Name: "acknowledgment", Description: "Did it acknowledge the move/update?", Weight: 0.3},
					},
				},
				SetupFunc: func() error {
					var err error
					h, err = newMemEvalHarness()
					if err != nil {
						return err
					}
					ctx := context.Background()
					// Write the old location first, then overwrite with the new one so
					// the store contains the most-recent value.
					old := memEntry("eval-update-001", "User lives in New York")
					old.UpdatedAt = time.Now().Add(-24 * time.Hour)
					if err := h.mem.Save(ctx, old); err != nil {
						return fmt.Errorf("setup mem-update (old): %w", err)
					}
					updated := memEntry("eval-update-002", "User recently moved to San Francisco")
					if err := h.mem.Save(ctx, updated); err != nil {
						return fmt.Errorf("setup mem-update (new): %w", err)
					}
					return nil
				},
				CleanupFunc: func() error {
					if h != nil {
						return h.cleanup()
					}
					return nil
				},
			}
		}(),

		// mem-profile-use: agent must personalise recommendation from stored preferences ---
		func() TaskCase {
			var h *memEvalHarness
			return TaskCase{
				ID:           "mem-profile-use",
				Goal:         "Suggest a text editor for me to use.",
				Complexity:   "moderate",
				Tags:         []string{"memory", "profile", "personalization"},
				Dimension:    DimMemory,
				VerifyMethod: VerifyLLMJudge,
				Rubric: &Rubric{
					Criteria: []JudgeCriterion{
						{Name: "personalization", Description: "Does the suggestion reflect terminal-based preference (e.g., neovim, helix, kakoune)?", Weight: 0.5},
						{Name: "vim_awareness", Description: "Does it consider vim keybinding preference?", Weight: 0.3},
						{Name: "reasoning", Description: "Is the recommendation well-reasoned?", Weight: 0.2},
					},
				},
				SetupFunc: func() error {
					var err error
					h, err = newMemEvalHarness()
					if err != nil {
						return err
					}
					ctx := context.Background()
					prefs := []memory.Entry{
						memEntry("eval-prof-001", "User prefers terminal-based tools over GUI applications"),
						memEntry("eval-prof-002", "User uses vim keybindings and is comfortable with modal editing"),
					}
					for _, p := range prefs {
						if err := h.mem.Save(ctx, p); err != nil {
							return fmt.Errorf("setup mem-profile-use: %w", err)
						}
					}
					return nil
				},
				CleanupFunc: func() error {
					if h != nil {
						return h.cleanup()
					}
					return nil
				},
			}
		}(),

		// mem-no-hallucinate: agent must admit it doesn't know — no setup needed ----------
		{
			ID:           "mem-no-hallucinate",
			Goal:         "What is my favorite movie? (Note: I have never told you about my movie preferences.)",
			Complexity:   "simple",
			Tags:         []string{"memory", "hallucination"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "honesty", Description: "Did the agent admit it doesn't know rather than making up an answer?", Weight: 0.7},
					{Name: "no_fabrication", Description: "Did it avoid fabricating a specific movie title as if from memory?", Weight: 0.3},
				},
			},
		},

		// mem-cross-session: agent must surface facts stored in a "prior session" ---------
		func() TaskCase {
			var h *memEvalHarness
			return TaskCase{
				ID:           "mem-cross-session",
				Goal:         "Based on our previous conversation, what resources would you recommend for what I'm learning?",
				Complexity:   "moderate",
				Tags:         []string{"memory", "cross-session"},
				Dimension:    DimMemory,
				VerifyMethod: VerifyLLMJudge,
				Rubric: &Rubric{
					Criteria: []JudgeCriterion{
						{Name: "context_awareness", Description: "Does the agent use cross-session memory to surface the Go/web-server context?", Weight: 0.4},
						{Name: "relevance", Description: "Are the recommendations relevant to Go web server development?", Weight: 0.4},
						{Name: "honesty", Description: "If memory is unavailable, does it say so rather than pretending?", Weight: 0.2},
					},
				},
				SetupFunc: func() error {
					var err error
					h, err = newMemEvalHarness()
					if err != nil {
						return err
					}
					ctx := context.Background()
					facts := []memory.Entry{
						memEntry("eval-xsess-001", "User is learning Go programming language"),
						memEntry("eval-xsess-002", "User is working on a web server project in Go"),
					}
					for _, f := range facts {
						if err := h.mem.Save(ctx, f); err != nil {
							return fmt.Errorf("setup mem-cross-session: %w", err)
						}
					}
					return nil
				},
				CleanupFunc: func() error {
					if h != nil {
						return h.cleanup()
					}
					return nil
				},
			}
		}(),
	}
}
