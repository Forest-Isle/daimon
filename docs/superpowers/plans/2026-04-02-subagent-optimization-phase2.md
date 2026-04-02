# Subagent Optimization Phase 2: Background Async + Prompt Cache + Sidechain

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add background async agent execution, prompt cache for system prompt deduplication, and sidechain execution recording for subagent history.

**Architecture:** BackgroundManager manages fire-and-forget goroutine agents with status channels. PromptCache deduplicates system prompt construction across agents. SidechainRecorder captures independent execution history per subagent with SQLite and file-based stores. Wire executeBackground() into AgentTool and PromptCache into Runtime.

**Tech Stack:** Go 1.22+, SQLite (existing store.DB), existing agent/session/channel packages

---

### Task 1: Implement BackgroundManager

**Files:**
- Create: `internal/agent/background.go`
- Create: `internal/agent/background_test.go`

### Task 2: Implement PromptCache

**Files:**
- Create: `internal/agent/prompt_cache.go`
- Create: `internal/agent/prompt_cache_test.go`

### Task 3: Implement SidechainRecorder with file store

**Files:**
- Create: `internal/agent/sidechain.go`
- Create: `internal/agent/sidechain_test.go`

### Task 4: Add SQLite sidechain store + migration

**Files:**
- Create: `internal/store/migrations/015_sidechain_entries.sql`
- Modify: `internal/agent/sidechain.go` (add SQLiteSidechainStore)

### Task 5: Wire BackgroundManager into AgentTool

**Files:**
- Modify: `internal/agent/agent_tool.go`
- Modify: `internal/agent/runtime.go`

### Task 6: Wire PromptCache into Runtime

**Files:**
- Modify: `internal/agent/runtime.go`
- Modify: `internal/gateway/gateway.go`

### Task 7: Integration tests + full verification

**Files:**
- Modify: `internal/agent/integration_test.go`
