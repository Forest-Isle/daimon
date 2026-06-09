# Daemon Design Spec

> Created: 2026-06-09 | Status: draft | Project: IronClaw → Daemon rebrand

## 1. Purpose

Daemon transforms IronClaw from a **request/response coding agent** into a **persistent personal agent that knows you, watches out for you, and acts on your behalf.** It is not a tool you summon — it is a presence that notices, remembers, infers, and interrupts.

**One-sentence differentiator:** Other agents wait for you to speak. Daemon already noticed what you were doing before you left.

## 2. Name

**Daemon.** Triple meaning:
- UNIX daemon — always-running background process
- Greek daimon (δαίμων) — personal guiding spirit, not a god, but an inner voice
- Pronounceable in Chinese (代蒙) and English, two syllables, clean

Rebrand scope: binary (`./bin/daemon`), config dir (`~/.daemon/`), Go module path (TBD — post-spec decision). IronClaw codebase retains internal package names until rename PR.

## 3. Architecture

Daemon is a **layer above IronClaw**, not a rewrite. IronClaw continues as the agent runtime (Gateway, Agent loop, Tools, Memory, Skills, Channels). Daemon adds two new systems: **PersonalCore** (a living model of the user) and **ComputerUse** (senses and hands on the host machine).

```
┌─────────────────────────────────────────┐
│               Daemon                     │
│  ┌─────────────────────────────────┐    │
│  │        PersonalCore             │    │
│  │  Observer  │  Timeline          │    │
│  │  Inferrer  │  Interrupter       │    │
│  └─────────────┬───────────────────┘    │
│  ┌─────────────┴───────────────────┐    │
│  │        ComputerUse              │    │
│  │  Capture (AX)  │  Vision (VLM)  │    │
│  │  Act (AX+CGEvent) │ Permissions │    │
│  └─────────────┬───────────────────┘    │
│  ┌─────────────┴───────────────────┐    │
│  │     IronClaw (existing)         │    │
│  │  Gateway │ Agent │ Memory       │    │
│  │  Tools │ Skills │ Scheduler     │    │
│  │  Channels (TUI + Telegram)      │    │
│  └─────────────────────────────────┘    │
└─────────────────────────────────────────┘
```

### 3.1 Data Flow

```
Scheduler ──→ PersonalCore.Inferrer (periodic: hourly)
                  │
ComputerUse ──→ PersonalCore.Observer (continuous, async)
  Capture           PersonalCore.Timeline (SQLite)
                  │
User (TUI/         │
Telegram) ──→ Agent Loop ──→ Tools ──→ ...
     ↑              ↑
     │              │
     └── PersonalCore.Interrupter ←────┘
          (push: "Your PR got a comment")
```

### 3.2 Gateway Integration

Two new subsystems in IronClaw's DepsBuilder pattern:

```go
// internal/gateway/init_daemon.go (new file)
func (gw *Gateway) initDaemon(b *DepsBuilder) error {
    if !gw.features.IsEnabled("daemon") { return nil }
    cu, _ := computeruse.NewDriver(...)
    pc, _ := personalcore.New(personalcore.Config{
        DB: gw.store, Driver: cu,
        InferrerLLM: gw.provider,
    })
    gw.computerUse = cu
    gw.personalCore = pc
    gw.schedulerChannel.Register(pc.AsTaskSource())
    gw.telegramChannel.RegisterInterruptSource(pc.Interrupts())
    return nil
}
```

## 4. PersonalCore — Living Personal Model

Four components, one SQLite table family.

### 4.1 Observer

Lightweight hooks on IronClaw's existing execution paths. Never on the critical path — fire-and-forget goroutine writes to a buffered channel.

| Hook point | What it records | Size |
|---|---|---|
| Tool call (audit interceptor) | `{ts, tool_name, category, param_summary}` | ~150 bytes |
| LLM interaction finish | `{ts, channel, topic_vector, sentiment_tag}` | ~180 bytes |
| File change (git hook / file watcher) | `{ts, repo, scope, commit_msg_topic}` | ~120 bytes |
| Time rhythm sampler | `{ts, active_channel, activity_type}` every 5m | ~80 bytes |
| AX change (ComputerUse) | `{ts, app_switched, window_title}` | ~100 bytes |

Observer does NOT store full content. That is Memory's job. Observer stores metadata patterns.

### 4.2 Timeline

Append-only observation log. Query by recency or by semantic relevance.

```sql
CREATE TABLE observations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TEXT NOT NULL,
    source      TEXT NOT NULL,   -- tool_call, file_change, ax_change, ...
    category    TEXT NOT NULL,
    summary     TEXT NOT NULL,   -- ≤ 200 chars
    embedding   BLOB,            -- optional float32[]
    project_id  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_obs_ts ON observations(ts);
CREATE INDEX idx_obs_source ON observations(source);
CREATE INDEX idx_obs_project ON observations(project_id);
```

### 4.3 Inferrer

Two-layer design:

**Statistical layer (cheap, local, runs hourly):**
- Time rhythm clustering: bucket activity by hour over 14-day window, find significant deviations (z < -1.5 or z > 1.5)
- Tool preference drift: compare 7-day-ago distribution vs recent 7-day distribution
- Project churn rate: count project_id switches per day
- Output: inferences with confidence scores

**LLM layer (expensive, runs every 6 hours, weekly deep analysis):**
- Feed: one week of observation summaries, existing inferences
- Prompt: "Based on these behavior patterns, what has changed? What should Daemon know?"
- Output: novel inferences that statistics can't catch (contextual meaning, intent shifts)
- Confidence threshold for action: >= 0.6

```sql
CREATE TABLE inferences (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern     TEXT NOT NULL,
    evidence    TEXT NOT NULL,   -- JSON array of observation IDs
    confidence  REAL NOT NULL,   -- 0-1
    category    TEXT NOT NULL,   -- time_pattern, tool_preference, ...
    suggestion  TEXT,            -- optional actionable suggestion
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_inf_category ON inferences(category);
CREATE INDEX idx_inf_confidence ON inferences(confidence);
```

### 4.4 Interrupter

Four-line defense before pushing to LO:

1. **Min interval gate:** never interrupt more than once every 5 minutes
2. **Current state scan:** classify last 10 minutes as coding/writing/idle/meeting
3. **Urgency composite:** event hint × state modifier × time-of-day modifier
4. **Channel routing:** urgent∧coding→Telegram silent, writing∧non-urgent→silent-queue, idle∧critical→Telegram notify, late-night→almost nothing

Initial rules hardcoded. LLM takes over individual judgments as confidence on related inferences exceeds 0.8.

### 4.5 ContextFor() — Injecting into LLM Prompts

Every agent interaction prepends compact personal context:

```
"14:00-17:00: low activity window (writing time). Current project: daemon.
 Recent preference: surgical edits over rewrites."
```

Built from active inferences filtered by confidence >= 0.7.

**Wiring:** Injected at agent loop start — `Agent.PrepareContext()` calls `PersonalCore.ContextFor()` and prepends output to the system prompt. Implemented as a pre-prompt hook in Gateway's agent builder (`init_agent.go`), not a tool. Zero additional LLM round-trips.

### 4.6 Retention Policy

Observations table grows unbounded. Retention strategy:

| Age | Policy |
|---|---|
| 0-30 days | Keep all raw observations |
| 30-90 days | Roll up by (source, category, day) — store aggregate counts, drop raw rows |
| 90+ days | Delete aggregates. Inferences that reference old observations retain their evidence IDs; the inference survives even if source observations are gone |

Retention compaction runs in Inferrer's hourly cycle, after statistical inference completes. Configurable: `daemon.personal_core.retention_days: 90`.

### 4.7 Event Coalescing

Multiple InterruptEvents arriving within the min-interval window are coalesced, not dropped. The Interrupter maintains a small pending queue (max 5 events, 10-minute TTL per event). On next fire window, it batches pending events into a single message ("3 things since 14:30: ...").

```go
type coalesceQueue struct {
    events   []InterruptEvent  // max 5
    fireAt   time.Time         // next allowed interrupt time
    maxAge   time.Duration     // 10 min — drop events older than this
}
```

## 5. ComputerUse — Senses and Hands

### 5.1 Industry Basis

Design informed by three reference implementations:

| System | Vision strategy | Action strategy | Scope |
|---|---|---|---|
| Anthropic Computer Use | Screenshot → VLM → action | Pure pixel (mouse_move, click, type, key) | VM/Docker X11 |
| Coasty (Open Computer Use) | ScreenCaptureKit + desktopCapturer, JPEG 70% | Platform-native (CGEvent, user32.dll, xdotool) | VM + local Electron overlay |
| mac-control MCP | AX API + CGDisplay | AX actions + CGEvent | macOS only |

**Daemon's approach: hybrid AX-first, vision as fallback.**

- AX tree: ~2ms, zero tokens, covers system apps + most Electron + standard controls
- Vision (screenshot → VLM): on-demand only, when AX insufficient or user explicitly requests

### 5.2 Go Interface

```go
package computeruse

type Driver interface {
    CaptureAX(ctx context.Context) (*AXSnapshot, error)
    CaptureScreen(ctx context.Context) (*Screenshot, error)
    Execute(ctx context.Context, action Action) error
    ResolveElement(ctx context.Context, matcher ElementMatcher) (*AXElement, error)
    Permissions(ctx context.Context) (*PermissionState, error)
}
```

### 5.3 Three-Layer Pipeline

```
Capture ──→ Vision ──→ Act
  │            │          │
  high-freq    on-demand  low-freq
  ~2ms         VLM cost   needs approval
  │            │          │
  └──→ Observer feeds ◄──┘
```

**Capture (continuous, no LLM):**
- AX tree snapshot every 5s: active app, window title, position, focused element
- No screenshot during continuous capture — too expensive

**Vision (on-demand, VLM):**
- Triggered by: Inferrer flag, user request, or scheduled checkpoint
- Screenshot via ScreenCaptureKit (macOS, zero flicker), JPEG 70%, full resolution
- Prompt always injected with PersonalCore context
- Returns structured `VisionResult{Summary, Alerts[], Suggested[]}`

**Act (gated by danger level):**
- Danger 0 (silent): move_mouse, scroll — never needs approval
- Danger 1 (session): type, key_combo — approve once per session
- Danger 2 (always): click, drag, right_click — approve every time
- Danger ∞ (never): system_shutdown, destructive ops — hard-blocked

### 5.4 Permission Model

macOS TCC requires two independent entitlements:
1. Screen Recording — for CaptureScreen()
2. Accessibility — for AX API, CGEvent actions

Structured error codes per Coasty's production-proven pattern:
```go
type PermissionState struct {
    ScreenRecording bool
    Accessibility   bool
}
// Errors carry {code, action?, origin?} — never raw string
```

**Graceful degradation matrix:**

| Permissions | What works | What doesn't |
|---|---|---|
| Both granted | Full AX capture + Vision + Act | — |
| AX only, no Screen Recording | AX capture + Act. Vision (screenshot) unavailable. | Screenshot → VLM |
| Screen Recording only, no AX | Vision (screenshot) works. AX capture and Act unavailable. | AX snapshots, automated clicks/typing |
| Neither | Daemon runs as pure chat agent. Observer limited to tool calls, git, time rhythm. No sensory input. | All ComputerUse functions |

Daemon never fails to start due to permissions. It degrades and records what's missing. First-run experience: detects missing permissions, surfaces structured prompt via TUI/Telegram with exact System Preferences deep-link URLs.

### 5.5 Platform Strategy

macOS first. Single platform until experience is right. Driver interface abstracts platform — Linux (AT-SPI + xdotool) and Windows (UI Automation + Win32) come later.

## 6. Config

```yaml
daemon:
  enabled: false
  display_id: 1
  capture:
    ax_interval: 5s
    screenshot_jpeg_q: 70
  personal_core:
    inferrer_schedule: "0 * * * *"
    llm_inferrer_schedule: "0 */6 * * *"
    interrupt_min_interval: 5m
    urgency_threshold: 0.5
    interrupt_channels: ["telegram", "tui_queue"]
  computer_use:
    vision_model: ""        # empty = default provider
    max_screenshot_dim: 2560
```

## 7. Implementation Phases

### Phase 1: Skeleton (Week 1) — "Daemon can see"

**Deliverables:**
- `internal/computeruse/driver_darwin.go` — cgo bridge to macOS AX API, ScreenCaptureKit
- `internal/personalcore/observer.go` — Record/RecordBatch
- `internal/personalcore/timeline.go` — Recent/Relevant queries
- SQLite migration for `observations`
- Config: daemon.enabled + capture section
- CLI: `daemon status` shows "observing, N events"

**Exit criteria:** AX snapshot captured every 5s, tool calls auto-recorded, queryable.

### Phase 2: Awareness (Weeks 2-3) — "Daemon learns LO"

**Deliverables:**
- `internal/personalcore/inferrer.go` — statistical layer (time clustering, drift detection)
- `internal/personalcore/context.go` — ContextFor() prompt injection
- SQLite migration for `inferences`
- Config: personal_core section

**Exit criteria:** Hourly inference produces patterns with confidence scores. Agent loop prepends ContextFor() output.

### Phase 3: Proactive (Weeks 3-4) — "Daemon reaches out"

**Deliverables:**
- `internal/personalcore/interrupter.go` — four-gate decision logic
- Gateway wiring: Interrupts channel → Telegram adapter
- Scheduler: register PersonalCore task source
- git hook → Observer → Interrupter → Telegram push (end-to-end)

**Exit criteria:** First proactive push sent from Daemon to LO's Telegram.

### Phase 4: Completeness (Weeks 5-6) — "Daemon closes the loop"

**Deliverables:**
- `internal/personalcore/inferrer_llm.go` — LLM inference layer
- `internal/computeruse/vision.go` — screenshot + VLM analysis
- `internal/computeruse/driver_darwin_deep.go` — deep AX element scanning
- `internal/computeruse/act.go` — AX + CGEvent action execution

**Exit criteria:** Full daily cycle operational — observe, infer, interrupt, act.

### Dependency Graph

```
Phase 1 ──→ Phase 2 ──→ Phase 3 ──→ Phase 4
  │            │            │
  │            │            └── needs Inference + Observer
  │            └── needs Observation data
  └── standalone: cgo + SQLite
```

Each phase merges independently and delivers user-visible value.

## 8. Non-Goals (for this spec)

- Multi-platform ComputerUse (macOS only for now)
- Daemon-to-Daemon communication (multi-device sync)
- Voice interface
- Plugin/app-store model for tools
- Cloud/remote agent execution (local-first is the premise)

## 9. Rename Scope

Post-spec, separate PR:
- Binary: `ironclaw` → `daemon`
- Config dir: `~/.ironclaw/` → `~/.daemon/`
- Go module: `github.com/ironclaw` → TBD
- Internal packages keep ironclaw references until rename PR — avoids massive diff during feature work
- Git history preserved (git-filter-repo or just move and commit)
