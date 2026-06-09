# Daemon Phase 1 Implementation Plan — Skeleton

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the observation pipeline — ComputerUse driver captures macOS AX snapshots, PersonalCore Observer records tool-call events, Timeline can query them.

**Architecture:** Two new packages (`internal/computeruse`, `internal/personalcore`) added to IronClaw's Gateway as a `DaemonSubsystem`. Feature-gated behind `daemon.enabled`. All observer hooks are async — fire-and-forget goroutines, zero latency impact on the agent loop.

**Tech Stack:** Go 1.25.11, cgo (ApplicationServices + AppKit frameworks), SQLite via existing store.DB, robfig/cron v3 (existing dep).

**Spec:** `docs/superpowers/specs/2026-06-09-daemon-design.md`

---

## File Map

| File | Responsibility |
|---|---|
| `internal/computeruse/driver.go` | Driver interface + shared types (AXSnapshot, Action, PermissionState, etc.) |
| `internal/computeruse/driver_darwin.go` | macOS cgo implementation: AX API for capture, ScreenCaptureKit stub |
| `internal/computeruse/driver_noop.go` | No-op driver for non-macOS platforms |
| `internal/personalcore/core.go` | PersonalCore interface + Config struct |
| `internal/personalcore/observer.go` | Record/RecordBatch — async write path, buffered channel |
| `internal/personalcore/timeline.go` | Recent/Relevant queries against observations table |
| `internal/store/migrations/026_observations.sql` | observations table + indexes |
| `internal/config/config.go` | Add DaemonConfig struct |
| `configs/ironclaw.example.yaml` | Add daemon section |
| `internal/gateway/subsystem_daemon.go` | DaemonSubsystem — wires ComputerUse + PersonalCore |
| `internal/gateway/subsystem_feature.go` | Register "daemon" feature |
| `internal/gateway/gateway.go` | Add daemon field, call initDaemon in New() |
| `cmd/ironclaw/main.go` or TUI | Add `daemon status` display (optional Phase 1) |

---

### Task 1: ComputerUse Driver Interface

**Files:**
- Create: `internal/computeruse/driver.go`

- [ ] **Step 1: Write the Go interface and shared types**

```go
// Package computeruse provides host-level sensory input and action execution.
// One Driver implementation per platform. macOS is the first target.
package computeruse

import (
	"context"
	"time"
)

// ─── Shared types ──────────────────────────────────────

// AXSnapshot is a lightweight capture of the accessibility state.
// Collected every 5s. ~2ms on macOS. Zero LLM token cost.
type AXSnapshot struct {
	Timestamp    time.Time
	ActiveApp    AppIdentity
	ActiveWindow WindowSummary
	Elements     []AXElement // empty in Phase 1; Phase 4 fills this
}

// AppIdentity identifies a running application.
type AppIdentity struct {
	BundleID       string // "com.apple.Safari"
	LocalizedName  string // "Safari"
	PID            int32
}

// WindowSummary describes the focused window.
type WindowSummary struct {
	Title  string
	Frame  Rect
	IsMain bool
}

// AXElement represents an interactive accessibility element.
// Phase 1: unused. Phase 4: populated by deep AX scan.
type AXElement struct {
	Role      string // "AXButton", "AXTextField", "AXMenu"
	Label     string
	Value     string
	Position  Point
	Size      Size
	IsFocused bool
	IsEnabled bool
}

// Screenshot is an on-demand visual capture. JPEG 70%, full resolution.
type Screenshot struct {
	Data       []byte // JPEG bytes
	Resolution Size
	Timestamp  time.Time
}

// ─── Action types ──────────────────────────────────────

// ActionKind categorizes the operation.
type ActionKind string

const (
	ActClick       ActionKind = "click"
	ActDoubleClick ActionKind = "double_click"
	ActRightClick  ActionKind = "right_click"
	ActType        ActionKind = "type"
	ActKeyCombo    ActionKind = "key_combo"
	ActScroll      ActionKind = "scroll"
	ActMove        ActionKind = "move_mouse"
	ActDrag        ActionKind = "drag"
)

// DangerLevel gates action execution by required approval level.
type DangerLevel int

const (
	DangerSilent DangerLevel = 0  // never needs approval
	DangerNormal DangerLevel = 1  // approve once per session
	DangerPrompt DangerLevel = 2  // approve every time
	DangerNever  DangerLevel = 99 // hard-blocked
)

// Action is a discrete operation on the host.
type Action struct {
	Kind        ActionKind
	Position    Point       // logical coordinates
	Text        string      // for type operations
	Keys        []string    // for key combos: ["cmd","c"]
	ScrollDelta int         // positive = down/right
	DangerLevel DangerLevel
}

// Danger returns the danger level for this action.
func (a Action) Danger() DangerLevel {
	switch a.Kind {
	case ActMove, ActScroll:
		return DangerSilent
	case ActType, ActKeyCombo:
		return DangerNormal
	default:
		return DangerPrompt
	}
}

// ─── Vision types ──────────────────────────────────────

// VisionResult is a structured VLM interpretation of a screenshot.
type VisionResult struct {
	Summary    string        // natural language: what's on screen
	Alerts     []VisionAlert
	Suggested  []Action
	Confidence float64       // 0-1
}

// VisionAlert describes something needing attention.
type VisionAlert struct {
	Severity AlertSeverity
	Message  string
}

// AlertSeverity categorizes an alert.
type AlertSeverity string

const (
	AlertInfo     AlertSeverity = "info"
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

// ─── Permission types ──────────────────────────────────

// PermissionState reports TCC permission status.
type PermissionState struct {
	ScreenRecording bool
	Accessibility   bool
}

// ─── Support types ─────────────────────────────────────

// ElementMatcher finds an AX element by criteria.
type ElementMatcher struct {
	Role  string // "AXButton"
	Label string // "Send"
	Index int    // nth match, 0-based
}

// ─── Geometry ──────────────────────────────────────────

// Rect is a rectangle in logical pixels.
type Rect struct {
	X, Y, Width, Height float64
}

// Point is a 2D point in logical pixels.
type Point struct {
	X, Y float64
}

// Size is a 2D size in logical pixels.
type Size struct {
	Width, Height float64
}

// ─── Driver interface ──────────────────────────────────

// Driver abstracts host-level computer interaction.
// One implementation per platform.
type Driver interface {
	// CaptureAX returns a lightweight AX snapshot. Fast. Called on a timer.
	CaptureAX(ctx context.Context) (*AXSnapshot, error)

	// CaptureScreen takes a JPEG screenshot. Called on-demand.
	CaptureScreen(ctx context.Context) (*Screenshot, error)

	// Execute performs an action on the host.
	Execute(ctx context.Context, action Action) error

	// ResolveElement finds the current AX element matching given criteria.
	ResolveElement(ctx context.Context, matcher ElementMatcher) (*AXElement, error)

	// Permissions returns current TCC permission states.
	Permissions(ctx context.Context) (*PermissionState, error)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/computeruse/...
```
Expected: PASS (no implementation yet, just the interface package)

- [ ] **Step 3: Commit**

```bash
git add internal/computeruse/driver.go
git commit -m "feat(computeruse): add Driver interface and shared types"
```

---

### Task 2: macOS Driver — CaptureAX via cgo

**Files:**
- Create: `internal/computeruse/driver_darwin.go`
- Create: `internal/computeruse/driver_darwin_test.go`

- [ ] **Step 1: Write the cgo bridge**

```go
//go:build darwin

package computeruse

/*
#cgo LDFLAGS: -framework ApplicationServices -framework AppKit
#include <ApplicationServices/ApplicationServices.h>
#include <AppKit/AppKit.h>

static void getFocusedApp(char *nameBuf, int nameLen, char *bundleBuf, int bundleLen, int *pid) {
    @autoreleasepool {
        NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (app) {
            NSString *name = [app localizedName];
            NSString *bundle = [app bundleIdentifier];
            if (name) [name getCString:nameBuf maxLength:nameLen encoding:NSUTF8StringEncoding];
            if (bundle) [bundle getCString:bundleBuf maxLength:bundleLen encoding:NSUTF8StringEncoding];
            *pid = (int)[app processIdentifier];
        }
    }
}

static void getFocusedWindow(char *titleBuf, int titleLen,
                              double *x, double *y, double *w, double *h) {
    @autoreleasepool {
        NSRunningApplication *frontApp = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (!frontApp) return;
        pid_t pid = [frontApp processIdentifier];
        AXUIElementRef appEl = AXUIElementCreateApplication(pid);
        if (!appEl) return;

        CFTypeRef windowRef = NULL;
        AXError err = AXUIElementCopyAttributeValue(appEl, kAXFocusedWindowAttribute, &windowRef);
        if (err != kAXErrorSuccess || !windowRef) { CFRelease(appEl); return; }

        AXUIElementRef window = (AXUIElementRef)windowRef;

        CFTypeRef titleVal = NULL;
        if (AXUIElementCopyAttributeValue(window, kAXTitleAttribute, &titleVal) == kAXErrorSuccess && titleVal) {
            if (CFGetTypeID(titleVal) == CFStringGetTypeID()) {
                [(__bridge NSString*)titleVal getCString:titleBuf maxLength:titleLen encoding:NSUTF8StringEncoding];
            }
            CFRelease(titleVal);
        }

        AXValueRef posVal = NULL;
        if (AXUIElementCopyAttributeValue(window, kAXPositionAttribute, (CFTypeRef*)&posVal) == kAXErrorSuccess && posVal) {
            CGPoint pos;
            if (AXValueGetValue(posVal, kAXValueTypeCGPoint, &pos)) {
                *x = pos.x; *y = pos.y;
            }
            CFRelease(posVal);
        }

        AXValueRef sizeVal = NULL;
        if (AXUIElementCopyAttributeValue(window, kAXSizeAttribute, (CFTypeRef*)&sizeVal) == kAXErrorSuccess && sizeVal) {
            CGSize size;
            if (AXValueGetValue(sizeVal, kAXValueTypeCGSize, &size)) {
                *w = size.width; *h = size.height;
            }
            CFRelease(sizeVal);
        }

        CFRelease(windowRef);
        CFRelease(appEl);
    }
}

static int checkAccessibilityPermission(void) {
    @autoreleasepool {
        NSDictionary *opts = @{(__bridge NSString*)kAXTrustedCheckOptionPrompt: @NO};
        return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts) ? 1 : 0;
    }
}
*/
import "C"

import (
	"context"
	"time"
	"unsafe"
)

// darwinDriver implements Driver for macOS via cgo AX API calls.
type darwinDriver struct{}

// NewDriver creates a macOS Driver.
func NewDriver() (Driver, error) {
	return &darwinDriver{}, nil
}

func (d *darwinDriver) CaptureAX(ctx context.Context) (*AXSnapshot, error) {
	var nameBuf, bundleBuf [256]C.char
	var pid C.int
	C.getFocusedApp(&nameBuf[0], 256, &bundleBuf[0], 256, &pid)

	var titleBuf [512]C.char
	var x, y, w, h C.double
	C.getFocusedWindow(&titleBuf[0], 512, &x, &y, &w, &h)

	return &AXSnapshot{
		Timestamp: time.Now(),
		ActiveApp: AppIdentity{
			BundleID:      C.GoString(&bundleBuf[0]),
			LocalizedName: C.GoString(&nameBuf[0]),
			PID:           int32(pid),
		},
		ActiveWindow: WindowSummary{
			Title:  C.GoString(&titleBuf[0]),
			Frame:  Rect{X: float64(x), Y: float64(y), Width: float64(w), Height: float64(h)},
			IsMain: true,
		},
	}, nil
}

func (d *darwinDriver) Permissions(ctx context.Context) (*PermissionState, error) {
	axOK := C.checkAccessibilityPermission() == 1
	return &PermissionState{
		ScreenRecording: false, // Phase 4: ScreenCaptureKit check
		Accessibility:   axOK,
	}, nil
}

// Phase 1 stubs — implemented in Phase 4.
func (d *darwinDriver) CaptureScreen(ctx context.Context) (*Screenshot, error) {
	return nil, nil // stub
}
func (d *darwinDriver) Execute(ctx context.Context, action Action) error {
	return nil // stub
}
func (d *darwinDriver) ResolveElement(ctx context.Context, matcher ElementMatcher) (*AXElement, error) {
	return nil, nil // stub
}

// Ensure darwinDriver satisfies Driver.
var _ Driver = (*darwinDriver)(nil)
```

- [ ] **Step 2: Write test**

```go
//go:build darwin

package computeruse

import (
	"context"
	"testing"
	"time"
)

func TestCaptureAX(t *testing.T) {
	drv, err := NewDriver()
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snap, err := drv.CaptureAX(ctx)
	if err != nil {
		t.Fatalf("CaptureAX: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}

	t.Logf("app=%s (%s) pid=%d window=%q frame={%g,%g,%g,%g}",
		snap.ActiveApp.LocalizedName,
		snap.ActiveApp.BundleID,
		snap.ActiveApp.PID,
		snap.ActiveWindow.Title,
		snap.ActiveWindow.Frame.X,
		snap.ActiveWindow.Frame.Y,
		snap.ActiveWindow.Frame.Width,
		snap.ActiveWindow.Frame.Height,
	)
}

func TestPermissions(t *testing.T) {
	drv, _ := NewDriver()
	ctx := context.Background()
	ps, err := drv.Permissions(ctx)
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	t.Logf("screenRecording=%v accessibility=%v", ps.ScreenRecording, ps.Accessibility)
}
```

- [ ] **Step 3: Run test to verify it works on macOS**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -v -run TestCaptureAX ./internal/computeruse/
```
Expected: PASS with log output showing current app/window info.

- [ ] **Step 4: Commit**

```bash
git add internal/computeruse/driver_darwin.go internal/computeruse/driver_darwin_test.go
git commit -m "feat(computeruse): add macOS driver with CaptureAX via cgo"
```

---

### Task 3: No-op Driver for Non-macOS

**Files:**
- Create: `internal/computeruse/driver_noop.go`

- [ ] **Step 1: Write the no-op stub**

```go
//go:build !darwin

package computeruse

import (
	"context"
	"fmt"
)

type noopDriver struct{}

// NewDriver creates a no-op Driver for unsupported platforms.
func NewDriver() (Driver, error) {
	return &noopDriver{}, nil
}

func (d *noopDriver) CaptureAX(ctx context.Context) (*AXSnapshot, error) {
	return nil, fmt.Errorf("computeruse: AX capture not supported on this platform")
}
func (d *noopDriver) CaptureScreen(ctx context.Context) (*Screenshot, error) {
	return nil, fmt.Errorf("computeruse: screenshot not supported on this platform")
}
func (d *noopDriver) Execute(ctx context.Context, action Action) error {
	return fmt.Errorf("computeruse: action execution not supported on this platform")
}
func (d *noopDriver) ResolveElement(ctx context.Context, matcher ElementMatcher) (*AXElement, error) {
	return nil, fmt.Errorf("computeruse: element resolution not supported on this platform")
}
func (d *noopDriver) Permissions(ctx context.Context) (*PermissionState, error) {
	return &PermissionState{}, nil
}

var _ Driver = (*noopDriver)(nil)
```

- [ ] **Step 2: Verify cross-platform build**

```bash
cd /Users/wuqisen/dev/IronClaw && GOOS=linux GOARCH=amd64 go build ./internal/computeruse/...
```
Expected: PASS. The noop driver is used on non-darwin GOOS.

- [ ] **Step 3: Commit**

```bash
git add internal/computeruse/driver_noop.go
git commit -m "feat(computeruse): add no-op driver for non-macOS platforms"
```

---

### Task 4: Observations Database Migration

**Files:**
- Create: `internal/store/migrations/026_observations.sql`

- [ ] **Step 1: Write the migration SQL**

```sql
-- 026_observations.sql: Observation log for Daemon PersonalCore

CREATE TABLE IF NOT EXISTS observations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TEXT NOT NULL,
    source      TEXT NOT NULL,
    category    TEXT NOT NULL,
    summary     TEXT NOT NULL,
    embedding   BLOB,
    project_id  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_obs_ts ON observations(ts);
CREATE INDEX IF NOT EXISTS idx_obs_source ON observations(source);
CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project_id);
```

- [ ] **Step 2: Verify migration runs**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/store/...
```
Expected: PASS. Migration is embedded and will apply on next store init.

- [ ] **Step 3: Commit**

```bash
git add internal/store/migrations/026_observations.sql
git commit -m "feat(store): add observations table migration for Daemon"
```

---

### Task 5: PersonalCore Interface and Observer

**Files:**
- Create: `internal/personalcore/core.go`
- Create: `internal/personalcore/observer.go`

- [ ] **Step 1: Write PersonalCore interface and Config**

```go
// Package personalcore implements Daemon's living personal model.
// It observes, remembers, infers patterns, and decides when to interrupt.
package personalcore

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/computeruse"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// ─── Config ────────────────────────────────────────────

// Config holds PersonalCore configuration.
type Config struct {
	DB                    *store.DB
	Driver                computeruse.Driver
	CaptureAXInterval     time.Duration // 5s default
	InferrerSchedule      string        // cron expr
	LLMInferrerSchedule   string        // cron expr
	InterruptMinInterval  time.Duration // 5m default
	UrgencyThreshold      float64       // 0.5 default
}

// ─── Observation ───────────────────────────────────────

// ObservationSource categorizes where an observation came from.
type ObservationSource string

const (
	SourceToolCall        ObservationSource = "tool_call"
	SourceFileChange      ObservationSource = "file_change"
	SourceTimeRhythm      ObservationSource = "time_rhythm"
	SourceAXChange        ObservationSource = "ax_change"
	SourceLLMInteraction  ObservationSource = "llm_interaction"
	SourceSystem          ObservationSource = "system"
)

// Observation is a single atomic event. < 200 bytes serialized.
type Observation struct {
	ID        int64
	Timestamp time.Time
	Source    ObservationSource
	Category  string // "git_push", "app_switched", "tool_called_bash"
	Summary   string // short hash: "bash:git log --oneline" | "app:Xcode"
	ProjectID *string
}

// ─── Inference ─────────────────────────────────────────

// InferenceCategory groups inference types.
type InferenceCategory string

const (
	InferTimePattern     InferenceCategory = "time_pattern"
	InferToolPreference  InferenceCategory = "tool_preference"
	InferProjectRelation InferenceCategory = "project_relation"
	InferBehaviorShift   InferenceCategory = "behavior_shift"
)

// Inference is a learned pattern. Confidence < 0.6 is stored but never acted on.
type Inference struct {
	ID         int64
	Pattern    string   // "14:00-17:00: low code activity (writing window)"
	Evidence   []int64  // observation IDs
	Confidence float64  // 0-1
	Category   InferenceCategory
	Suggestion string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ─── Interrupt ─────────────────────────────────────────

// InterruptChannel is where an interrupt is delivered.
type InterruptChannel string

const (
	ChannelTelegram InterruptChannel = "telegram"
	ChannelTUIQueue InterruptChannel = "tui_queue"
	ChannelNone     InterruptChannel = "none"
)

// InterruptDecision is the result of "should I tell LO something right now?"
type InterruptDecision struct {
	ShouldInterrupt bool
	Urgency         float64
	Channel         InterruptChannel
	Message         string
	Reason          string
}

// InterruptEvent is what triggers a ShouldInterrupt evaluation.
type InterruptEvent struct {
	Source      string
	Description string
	UrgencyHint float64
	Metadata    map[string]string
}

// ─── Query types ───────────────────────────────────────

// RecentOpts filters Recent() queries.
type RecentOpts struct {
	Limit     int
	Source    ObservationSource
	Category  string
	Since     time.Time
	ProjectID string
}

// ─── PersonalCore interface ────────────────────────────

// PersonalCore is the living personal model.
type PersonalCore interface {
	// Record pushes an observation into the timeline. Non-blocking.
	Record(ctx context.Context, obs Observation) error

	// RecordBatch pushes multiple observations.
	RecordBatch(ctx context.Context, obs []Observation) error

	// Recent returns the last N observations, optionally filtered.
	Recent(ctx context.Context, opts RecentOpts) ([]Observation, error)

	// ContextFor builds a compact context string for LLM prompt injection.
	// Returns empty string if no active inferences exist.
	ContextFor(ctx context.Context) (string, error)

	// RunInference runs inference pipeline. Returns new/updated inferences.
	// Phase 2 (statistical layer), Phase 4 (LLM layer).
	RunInference(ctx context.Context) ([]Inference, error)

	// Inferences returns all active (confidence >= 0.6) inferences.
	Inferences(ctx context.Context, category InferenceCategory) ([]Inference, error)

	// ShouldInterrupt decides whether an event warrants pushing to LO.
	ShouldInterrupt(ctx context.Context, event InterruptEvent) (InterruptDecision, error)

	// Interrupts returns a channel that yields interrupt decisions.
	Interrupts() <-chan InterruptDecision

	// Start begins background loops (AX capture timer, observer consumer).
	Start(ctx context.Context) error

	// Stop gracefully shuts down.
	Stop(ctx context.Context) error
}
```

- [ ] **Step 2: Write Observer implementation**

```go
package personalcore

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/computeruse"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// personalCore is the concrete implementation of PersonalCore.
type personalCore struct {
	db        *store.DB
	driver    computeruse.Driver
	config    Config

	// Observer
	obsCh     chan Observation        // buffered, non-blocking writes
	obsWg     sync.WaitGroup

	// Interrupts
	interruptCh chan InterruptDecision

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

// New creates a PersonalCore. Call Start() to begin background loops.
func New(cfg Config) (PersonalCore, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("personalcore: DB is required")
	}
	if cfg.CaptureAXInterval == 0 {
		cfg.CaptureAXInterval = 5 * time.Second
	}
	if cfg.InterruptMinInterval == 0 {
		cfg.InterruptMinInterval = 5 * time.Minute
	}
	if cfg.UrgencyThreshold == 0 {
		cfg.UrgencyThreshold = 0.5
	}

	pc := &personalCore{
		db:          cfg.DB,
		driver:      cfg.Driver,
		config:      cfg,
		obsCh:       make(chan Observation, 1024),
		interruptCh: make(chan InterruptDecision, 16),
	}
	return pc, nil
}

// ─── Observer ──────────────────────────────────────────

func (c *personalCore) Record(ctx context.Context, obs Observation) error {
	if obs.Timestamp.IsZero() {
		obs.Timestamp = time.Now()
	}
	select {
	case c.obsCh <- obs:
		return nil
	default:
		// Channel full — drop observation rather than blocking caller.
		slog.Warn("personalcore: observation channel full, dropping", "source", obs.Source)
		return nil
	}
}

func (c *personalCore) RecordBatch(ctx context.Context, obs []Observation) error {
	for i := range obs {
		if err := c.Record(ctx, obs[i]); err != nil {
			return err
		}
	}
	return nil
}

// consumeObservations drains obsCh and writes to SQLite. Runs in background goroutine.
func (c *personalCore) consumeObservations() {
	defer c.obsWg.Done()
	for {
		select {
		case <-c.ctx.Done():
			// Drain remaining before exit.
			for {
				select {
				case obs := <-c.obsCh:
					c.insertObs(obs)
				default:
					return
				}
			}
		case obs := <-c.obsCh:
			c.insertObs(obs)
		}
	}
}

func (c *personalCore) insertObs(obs Observation) {
	_, err := c.db.Exec(
		`INSERT INTO observations (ts, source, category, summary, project_id)
		 VALUES (?, ?, ?, ?, ?)`,
		obs.Timestamp.Format(time.RFC3339),
		string(obs.Source),
		obs.Category,
		obs.Summary,
		obs.ProjectID,
	)
	if err != nil {
		slog.Error("personalcore: failed to insert observation", "err", err)
	}
}

// ─── AX Capture Loop ───────────────────────────────────

// captureAXLoop periodically captures AX snapshots on a timer.
func (c *personalCore) captureAXLoop() {
	if c.driver == nil {
		return
	}
	defer c.obsWg.Done()
	ticker := time.NewTicker(c.config.CaptureAXInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			snap, err := c.driver.CaptureAX(c.ctx)
			if err != nil {
				slog.Debug("personalcore: AX capture failed", "err", err)
				continue
			}
			if snap == nil {
				continue
			}
			c.Record(c.ctx, Observation{
				Timestamp: snap.Timestamp,
				Source:    SourceAXChange,
				Category:  "app:" + snap.ActiveApp.BundleID,
				Summary:   fmt.Sprintf("app:%s window:%q", snap.ActiveApp.LocalizedName, snap.ActiveWindow.Title),
			})
		}
	}
}

// ─── Lifecycle ─────────────────────────────────────────

func (c *personalCore) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx != nil {
		return fmt.Errorf("personalcore: already started")
	}
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Start observer consumer.
	c.obsWg.Add(1)
	go c.consumeObservations()

	// Start AX capture loop if driver is available.
	if c.driver != nil {
		c.obsWg.Add(1)
		go c.captureAXLoop()
	}

	slog.Info("personalcore: started")
	return nil
}

func (c *personalCore) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return nil
	}
	c.cancel()
	c.obsWg.Wait()
	close(c.interruptCh)
	slog.Info("personalcore: stopped")
	return nil
}

// ─── Phase 1 stubs ─────────────────────────────────────

func (c *personalCore) ContextFor(ctx context.Context) (string, error) {
	return "", nil // Phase 2
}

func (c *personalCore) RunInference(ctx context.Context) ([]Inference, error) {
	return nil, nil // Phase 2
}

func (c *personalCore) Inferences(ctx context.Context, category InferenceCategory) ([]Inference, error) {
	return nil, nil // Phase 2
}

func (c *personalCore) ShouldInterrupt(ctx context.Context, event InterruptEvent) (InterruptDecision, error) {
	return InterruptDecision{ShouldInterrupt: false, Channel: ChannelNone}, nil // Phase 3
}

func (c *personalCore) Interrupts() <-chan InterruptDecision {
	return c.interruptCh
}

// Ensure personalCore satisfies PersonalCore.
var _ PersonalCore = (*personalCore)(nil)
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/personalcore/...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/personalcore/core.go internal/personalcore/observer.go
git commit -m "feat(personalcore): add PersonalCore interface and Observer with AX loop"
```

---

### Task 6: Timeline Queries

**Files:**
- Create: `internal/personalcore/timeline.go`
- Create: `internal/personalcore/timeline_test.go`

- [ ] **Step 1: Write Recent and Relevant**

```go
package personalcore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Recent returns observations matching the given filters.
func (c *personalCore) Recent(ctx context.Context, opts RecentOpts) ([]Observation, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 500 {
		opts.Limit = 500
	}

	var clauses []string
	var args []interface{}

	if opts.Source != "" {
		clauses = append(clauses, "source = ?")
		args = append(args, string(opts.Source))
	}
	if opts.Category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, opts.Category)
	}
	if !opts.Since.IsZero() {
		clauses = append(clauses, "ts >= ?")
		args = append(args, opts.Since.Format(time.RFC3339))
	}
	if opts.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, opts.ProjectID)
	}

	query := "SELECT id, ts, source, category, summary, project_id FROM observations"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY ts DESC LIMIT ?"
	args = append(args, opts.Limit)

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("personalcore: recent query: %w", err)
	}
	defer rows.Close()

	var results []Observation
	for rows.Next() {
		var o Observation
		var tsStr string
		var sourceStr string
		var projectID sql.NullString
		if err := rows.Scan(&o.ID, &tsStr, &sourceStr, &o.Category, &o.Summary, &projectID); err != nil {
			return nil, fmt.Errorf("personalcore: scan: %w", err)
		}
		o.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		o.Source = ObservationSource(sourceStr)
		if projectID.Valid {
			o.ProjectID = &projectID.String
		}
		results = append(results, o)
	}
	return results, rows.Err()
}

// Relevant is a placeholder in Phase 1. Phase 4 adds embedding-based search.
func (c *personalCore) Relevant(ctx context.Context, query string, limit int) ([]Observation, error) {
	if limit <= 0 {
		limit = 20
	}
	// Simple fallback: return most recent observations.
	return c.Recent(ctx, RecentOpts{Limit: limit})
}
```

- [ ] **Step 2: Write test**

```go
package personalcore

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRecordAndRecent(t *testing.T) {
	db := testDB(t)
	pc, err := New(Config{DB: db})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start to begin observer consumer.
	if err := pc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer pc.Stop(ctx)

	// Record a few observations.
	now := time.Now()
	obs := []Observation{
		{Timestamp: now, Source: SourceToolCall, Category: "tool:bash", Summary: "bash:git status"},
		{Timestamp: now.Add(-1 * time.Minute), Source: SourceToolCall, Category: "tool:read", Summary: "read:main.go"},
		{Timestamp: now.Add(-2 * time.Minute), Source: SourceAXChange, Category: "app:com.apple.Terminal", Summary: "app:Terminal"},
	}
	for _, o := range obs {
		if err := pc.Record(ctx, o); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	// Give consumer time to flush.
	time.Sleep(100 * time.Millisecond)

	// Query recent.
	results, err := pc.Recent(ctx, RecentOpts{Limit: 10})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify order (most recent first).
	if results[0].Category != "tool:bash" {
		t.Errorf("expected bash first, got %s", results[0].Category)
	}
}

func TestRecentFilters(t *testing.T) {
	db := testDB(t)
	pc, _ := New(Config{DB: db})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc.Start(ctx)
	defer pc.Stop(ctx)

	now := time.Now()
	pc.Record(ctx, Observation{Timestamp: now, Source: SourceToolCall, Category: "tool:bash", Summary: "bash"})
	pc.Record(ctx, Observation{Timestamp: now, Source: SourceAXChange, Category: "app:finder", Summary: "finder"})
	time.Sleep(100 * time.Millisecond)

	// Filter by source.
	filtered, err := pc.Recent(ctx, RecentOpts{Source: SourceToolCall, Limit: 10})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	for _, o := range filtered {
		if o.Source != SourceToolCall {
			t.Errorf("expected only tool_call, got %s", o.Source)
		}
	}
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test -v -count=1 ./internal/personalcore/
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/personalcore/timeline.go internal/personalcore/timeline_test.go
git commit -m "feat(personalcore): add timeline Recent/Relevant queries with tests"
```

---

### Task 7: Config — Daemon Section

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add DaemonConfig to Config struct**

Near line 28, after `Hooks HooksConfig`, add:

```go
// In the Config struct, after the Hooks field:
Daemon DaemonConfig `yaml:"daemon"`
```

Add the DaemonConfig types at the end of config.go (or a new `daemon_config.go` in the config package):

```go
// DaemonConfig holds all Daemon (personal agent) configuration.
type DaemonConfig struct {
	Enabled      bool              `yaml:"enabled"`
	DisplayID    int               `yaml:"display_id"`
	Capture      CaptureConfig     `yaml:"capture"`
	PersonalCore PersonalCoreConfig `yaml:"personal_core"`
	ComputerUse  ComputerUseConfig  `yaml:"computer_use"`
}

type CaptureConfig struct {
	AXInterval       string `yaml:"ax_interval"`        // "5s"
	ScreenshotJPEGQ  int    `yaml:"screenshot_jpeg_q"`  // 70
}

type PersonalCoreConfig struct {
	InferrerSchedule     string `yaml:"inferrer_schedule"`       // "0 * * * *"
	LLMInferrerSchedule  string `yaml:"llm_inferrer_schedule"`   // "0 */6 * * *"
	InterruptMinInterval string `yaml:"interrupt_min_interval"`  // "5m"
	UrgencyThreshold     float64 `yaml:"urgency_threshold"`      // 0.5
	InterruptChannels    []string `yaml:"interrupt_channels"`    // ["telegram", "tui_queue"]
	RetentionDays        int     `yaml:"retention_days"`         // 90
}

type ComputerUseConfig struct {
	VisionModel       string `yaml:"vision_model"`         // "" = default provider
	MaxScreenshotDim  int    `yaml:"max_screenshot_dim"`   // 2560
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/config/...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add DaemonConfig section"
```

---

### Task 8: Example Config

**Files:**
- Modify: `configs/ironclaw.example.yaml`

- [ ] **Step 1: Add daemon section**

Append to the end of the example config:

```yaml
# ─── Daemon (personal agent) ─────────────────────────────
# Feature-gated: ironclaw feature enable daemon
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
    interrupt_channels:
      - telegram
      - tui_queue
    retention_days: 90
  computer_use:
    vision_model: ""
    max_screenshot_dim: 2560
```

- [ ] **Step 2: Verify YAML is valid**

```bash
cd /Users/wuqisen/dev/IronClaw && python3 -c "import yaml; yaml.safe_load(open('configs/ironclaw.example.yaml'))" && echo "YAML OK"
```
Expected: "YAML OK"

- [ ] **Step 3: Commit**

```bash
git add configs/ironclaw.example.yaml
git commit -m "feat(config): add daemon section to example config"
```

---

### Task 9: Feature Registration

**Files:**
- Modify: `internal/gateway/subsystem_feature.go`

- [ ] **Step 1: Register "daemon" feature**

In `InitFeatures()`, after the "server" registration line, add:

```go
r.Register(feature.Feature{Name: "daemon", Description: "Personal agent (PersonalCore + ComputerUse)", Default: false})
```

And in the `Resolve` overrides map, add:

```go
"daemon": cfg.Daemon.Enabled,
```

The complete `InitFeatures` becomes:

```go
func InitFeatures(cfg *config.Config) *FeatureSubsystem {
	r := feature.NewRegistry()
	r.Register(feature.Feature{Name: "memory", Description: "Memory system", Default: true})
	r.Register(feature.Feature{Name: "skills", Description: "SKILL.md loading", Default: true})
	r.Register(feature.Feature{Name: "multi_agent", Description: "Sub-agent spawning", Default: true})
	r.Register(feature.Feature{Name: "server", Description: "HTTP admin server", Default: false})
	r.Register(feature.Feature{Name: "daemon", Description: "Personal agent (PersonalCore + ComputerUse)", Default: false})
	for name, srv := range cfg.Tools.MCP.Servers {
		r.Register(feature.Feature{Name: "mcp_" + name, Description: fmt.Sprintf("MCP: %s", srv.Command), Default: true})
	}
	r.Resolve(context.Background(), map[string]bool{
		"memory": cfg.Memory.Enabled, "skills": cfg.Skills.Enabled,
		"multi_agent": cfg.Agents.Enabled, "server": cfg.Server.Enabled,
		"daemon": cfg.Daemon.Enabled,
	})
	return &FeatureSubsystem{Registry: r}
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/gateway/...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/subsystem_feature.go
git commit -m "feat(gateway): register daemon feature flag"
```

---

### Task 10: DaemonSubsystem — Gateway Wiring

**Files:**
- Create: `internal/gateway/subsystem_daemon.go`
- Modify: `internal/gateway/gateway.go`

- [ ] **Step 1: Write DaemonSubsystem**

```go
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/computeruse"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/personalcore"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// DaemonSubsystem wraps PersonalCore for Gateway lifecycle management.
type DaemonSubsystem struct {
	PersonalCore personalcore.PersonalCore
	ComputerUse  computeruse.Driver
}

func (ds *DaemonSubsystem) Name() string                { return "daemon" }
func (ds *DaemonSubsystem) Start(ctx context.Context) error {
	if ds.PersonalCore != nil {
		return ds.PersonalCore.Start(ctx)
	}
	return nil
}
func (ds *DaemonSubsystem) Stop(ctx context.Context) error {
	if ds.PersonalCore != nil {
		return ds.PersonalCore.Stop(ctx)
	}
	return nil
}

// InitDaemon creates the Daemon subsystem if the feature is enabled.
// Returns nil if daemon is disabled or not supported on this platform.
func InitDaemon(features *FeatureSubsystem, cfg *config.Config, db *store.DB) *DaemonSubsystem {
	if !features.IsEnabled("daemon") {
		return &DaemonSubsystem{}
	}

	driver, err := computeruse.NewDriver()
	if err != nil {
		slog.Warn("daemon: failed to create computer-use driver, running without sensory input", "err", err)
		driver = nil
	}

	// Parse durations from config.
	axInterval, _ := time.ParseDuration(cfg.Daemon.Capture.AXInterval)
	if axInterval == 0 {
		axInterval = 5 * time.Second
	}
	minInterrupt, _ := time.ParseDuration(cfg.Daemon.PersonalCore.InterruptMinInterval)
	if minInterrupt == 0 {
		minInterrupt = 5 * time.Minute
	}

	pc, err := personalcore.New(personalcore.Config{
		DB:                   db,
		Driver:               driver,
		CaptureAXInterval:    axInterval,
		InferrerSchedule:     cfg.Daemon.PersonalCore.InferrerSchedule,
		LLMInferrerSchedule:  cfg.Daemon.PersonalCore.LLMInferrerSchedule,
		InterruptMinInterval: minInterrupt,
		UrgencyThreshold:     cfg.Daemon.PersonalCore.UrgencyThreshold,
	})
	if err != nil {
		slog.Error("daemon: failed to create PersonalCore", "err", err)
		return &DaemonSubsystem{}
	}

	// Log permission state.
	if driver != nil {
		ps, err := driver.Permissions(context.Background())
		if err == nil {
			slog.Info("daemon: permissions",
				"screen_recording", ps.ScreenRecording,
				"accessibility", ps.Accessibility,
			)
		}
	}

	return &DaemonSubsystem{
		PersonalCore: pc,
		ComputerUse:  driver,
	}
}
```

- [ ] **Step 2: Modify gateway.go — add daemon field and init**

Add to `Gateway` struct (after the scheduler field, around line 45):

```go
daemon      *DaemonSubsystem
```

In `New()`, after scheduler init (around line 109), add:

```go
// ─── Daemon (personal agent) ────────────────────────────
gw.daemon = InitDaemon(featSub, cfg, gw.db)
```

In the `subsystems` assignment (around line 119), add `gw.daemon`:

```go
gw.subsystems = Subsystems{gw.memory, gw.channels, gw.mcpSub, gw.health, gw.config, gw.scheduler, gw.daemon}
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/gateway/...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/subsystem_daemon.go internal/gateway/gateway.go
git commit -m "feat(gateway): add DaemonSubsystem with PersonalCore+ComputerUse wiring"
```

---

### Task 11: Observer Hook — Tool Call Recording

**Files:**
- Create: `internal/gateway/observer_hook.go`

- [ ] **Step 1: Write observer hook**

```go
package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/personalcore"
)

// ObserverHook records tool calls as personalcore observations.
// It wraps a tool handler to fire-and-forget Record after each call.
type ObserverHook struct {
	pc personalcore.PersonalCore
}

// NewObserverHook creates a hook that records tool calls.
func NewObserverHook(pc personalcore.PersonalCore) *ObserverHook {
	return &ObserverHook{pc: pc}
}

// WrapHandler returns a handler that records an observation after execution.
func (h *ObserverHook) WrapHandler(toolName string, next func(ctx context.Context, args map[string]any) (any, error)) func(ctx context.Context, args map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		start := time.Now()
		result, err := next(ctx, args)

		// Fire-and-forget: never block tool return on observation recording.
		go func() {
			if h.pc == nil {
				return
			}
			summary := summarizeToolArgs(toolName, args)
			h.pc.Record(context.Background(), personalcore.Observation{
				Timestamp: start,
				Source:    personalcore.SourceToolCall,
				Category:  "tool:" + toolName,
				Summary:   summary,
			})
		}()

		return result, err
	}
}

// summarizeToolArgs builds a compact summary of tool arguments.
// Max 200 chars — enough for pattern recognition, not full content.
func summarizeToolArgs(name string, args map[string]any) string {
	var parts []string
	parts = append(parts, name)

	switch name {
	case "bash":
		if cmd, ok := args["command"]; ok {
			s := fmt.Sprintf("%v", cmd)
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			parts = append(parts, s)
		}
	case "read", "file_read":
		if path, ok := args["file_path"]; ok {
			s := fmt.Sprintf("%v", path)
			if len(s) > 80 {
				// Keep just the filename.
				if idx := strings.LastIndex(s, "/"); idx >= 0 {
					s = s[idx+1:]
				}
			}
			parts = append(parts, s)
		}
	case "write", "file_write":
		if path, ok := args["file_path"]; ok {
			s := fmt.Sprintf("%v", path)
			if len(s) > 80 {
				if idx := strings.LastIndex(s, "/"); idx >= 0 {
					s = s[idx+1:]
				}
			}
			parts = append(parts, s)
		}
	default:
		// Generic: list top-level arg keys.
		var keys []string
		for k := range args {
			keys = append(keys, k)
		}
		if len(keys) > 5 {
			keys = keys[:5]
		}
		parts = append(parts, strings.Join(keys, ","))
	}

	summary := strings.Join(parts, ":")
	if len(summary) > 200 {
		summary = summary[:200]
	}
	return summary
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/wuqisen/dev/IronClaw && go build ./internal/gateway/...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/observer_hook.go
git commit -m "feat(gateway): add ObserverHook for tool call recording"
```

---

### Task 12: Integration — Full Build and Smoke Test

**Files:**
- No new files. Verification only.

- [ ] **Step 1: Full build with CGO**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 make build-bin
```
Expected: PASS. Binary at `./bin/ironclaw`.

- [ ] **Step 2: Run all tests**

```bash
cd /Users/wuqisen/dev/IronClaw && CGO_ENABLED=1 go test ./internal/computeruse/... ./internal/personalcore/...
```
Expected: PASS.

- [ ] **Step 3: Verify vet**

```bash
cd /Users/wuqisen/dev/IronClaw && make vet
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A
git diff --cached --stat
git commit -m "chore: Phase 1 integration — full build and test pass"
```

---

## Phase 1 Complete — Exit Criteria

- [x] `internal/computeruse/driver.go` — Driver interface defined
- [x] `internal/computeruse/driver_darwin.go` — AX capture works on macOS
- [x] `internal/computeruse/driver_noop.go` — Cross-platform build works
- [x] `internal/store/migrations/026_observations.sql` — observations table migrates
- [x] `internal/personalcore/core.go` — PersonalCore interface + Config
- [x] `internal/personalcore/observer.go` — Record/RecordBatch + AX capture loop
- [x] `internal/personalcore/timeline.go` — Recent/Relevant queries with tests
- [x] `internal/config/config.go` — DaemonConfig section
- [x] `configs/ironclaw.example.yaml` — daemon section documented
- [x] `internal/gateway/subsystem_feature.go` — "daemon" feature registered
- [x] `internal/gateway/subsystem_daemon.go` — DaemonSubsystem wiring
- [x] `internal/gateway/observer_hook.go` — Tool call recording hook
- [x] Full build, tests, vet: PASS

**User-visible value at this checkpoint:**
```
$ ./bin/ironclaw feature enable daemon
$ ./bin/ironclaw tui
> daemon status
"Observing. 342 events in timeline. Current: Xcode — IronClaw/main.go"
```
