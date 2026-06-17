package gateway

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type holdTestTool struct {
	called  int
	inputs  []string
	failErr error
}

func (t *holdTestTool) Name() string                { return "http" }
func (t *holdTestTool) Description() string         { return "test http" }
func (t *holdTestTool) InputSchema() map[string]any { return nil }
func (t *holdTestTool) RequiresApproval() bool      { return false }
func (t *holdTestTool) Execute(_ context.Context, in []byte) (tool.Result, error) {
	t.called++
	t.inputs = append(t.inputs, string(in))
	if t.failErr != nil {
		return tool.Result{}, t.failErr
	}
	return tool.Result{Output: "ok"}, nil
}

func newHoldTestGateway(t *testing.T, enabled bool) (*Gateway, *store.DB, *action.Store, *holdTestTool, func()) {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db := &store.DB{DB: raw}
	if _, err := db.Exec(`
		CREATE TABLE trust_ledger (
			action_class TEXT NOT NULL,
			context_key  TEXT NOT NULL,
			attempts     INTEGER NOT NULL DEFAULT 0,
			verified_ok  INTEGER NOT NULL DEFAULT 0,
			corrected    INTEGER NOT NULL DEFAULT 0,
			level        INTEGER NOT NULL DEFAULT 0,
			updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (action_class, context_key)
		);
		CREATE TABLE holds (
			id         TEXT PRIMARY KEY,
			receipt_id TEXT NOT NULL,
			tool_name  TEXT NOT NULL,
			payload    TEXT NOT NULL DEFAULT '',
			execute_at DATETIME NOT NULL,
			state      TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_holds_state ON holds(state);
		CREATE INDEX idx_holds_execute_at ON holds(execute_at);
	`); err != nil {
		t.Fatal(err)
	}
	st := action.NewStore(db.DB)
	reg := tool.NewRegistry()
	httpTool := &holdTestTool{}
	reg.Register(httpTool)
	cfg := &config.Config{}
	cfg.Agent.Action.HoldEnabled = enabled
	gw := &Gateway{
		config: InitConfig(cfg, ""),
		toolSub: &ToolSubsystem{
			Registry:    reg,
			ActionStore: st,
		},
	}
	return gw, db, st, httpTool, func() { _ = db.Close() }
}

func TestDrainHoldsExecutesDueHoldAndMarksExecuted(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, true)
	defer closeDB()
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{"method":"POST"}`, ExecuteAt: "2000-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	gw.drainHolds(ctx)

	if httpTool.called != 1 || httpTool.inputs[0] != `{"method":"POST"}` {
		t.Fatalf("tool called=%d inputs=%#v, want one execution with payload", httpTool.called, httpTool.inputs)
	}
	assertHoldState(t, db, "h1", "executed")
	lvl, err := st.TrustLevel(ctx, action.Compensable, "http")
	if err != nil {
		t.Fatalf("TrustLevel() error = %v", err)
	}
	if lvl != action.AskEvery {
		t.Fatalf("level = %v, want AskEvery after unverified compensable execution", lvl)
	}
}

func TestDrainHoldsMarksFailedOnToolError(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, true)
	defer closeDB()
	httpTool.failErr = errors.New("boom")
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{"method":"POST"}`, ExecuteAt: "2000-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	gw.drainHolds(ctx)

	if httpTool.called != 1 {
		t.Fatalf("tool called=%d, want 1 fire attempt", httpTool.called)
	}
	// A failed fire is terminal ('failed'), never silently 'executed', and is
	// not re-drained on the next tick.
	assertHoldState(t, db, "h1", "failed")
	gw.drainHolds(ctx)
	if httpTool.called != 1 {
		t.Fatalf("failed hold re-fired: called=%d, want 1 (no retry)", httpTool.called)
	}
}

func TestDrainHoldsRecoversStaleExecutingHold(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, true)
	defer closeDB()
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{"method":"POST"}`, ExecuteAt: "2000-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}
	// Simulate a crash mid-claim: hold stuck in transient 'executing'.
	if _, err := db.Exec(`UPDATE holds SET state = 'executing' WHERE id = 'h1'`); err != nil {
		t.Fatalf("seed executing: %v", err)
	}
	// Without recovery the drain skips it (not pending), so it is orphaned.
	gw.drainHolds(ctx)
	if httpTool.called != 0 {
		t.Fatalf("executing hold fired before recovery: called=%d", httpTool.called)
	}
	n, err := st.RecoverStaleHolds(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RecoverStaleHolds() = %d, %v; want 1, nil", n, err)
	}
	gw.drainHolds(ctx)
	if httpTool.called != 1 {
		t.Fatalf("recovered hold not fired: called=%d, want 1", httpTool.called)
	}
	assertHoldState(t, db, "h1", "executed")
}

func TestDrainHoldsSkipsRecalledHold(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, true)
	defer closeDB()
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{}`, ExecuteAt: "2000-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}
	if err := st.RecallHold(ctx, "h1"); err != nil {
		t.Fatalf("RecallHold() error = %v", err)
	}

	gw.drainHolds(ctx)

	if httpTool.called != 0 {
		t.Fatalf("recalled hold executed %d time(s), want 0", httpTool.called)
	}
	assertHoldState(t, db, "h1", "recalled")
}

func TestDrainHoldsSkipsNotYetDueHold(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, true)
	defer closeDB()
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{}`, ExecuteAt: "2999-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	gw.drainHolds(ctx)

	if httpTool.called != 0 {
		t.Fatalf("not-yet-due hold executed %d time(s), want 0", httpTool.called)
	}
	assertHoldState(t, db, "h1", "pending")
}

func TestDrainHoldsDisabledDoesNothing(t *testing.T) {
	gw, db, st, httpTool, closeDB := newHoldTestGateway(t, false)
	defer closeDB()
	ctx := context.Background()
	if err := st.CreateHold(ctx, action.Hold{ID: "h1", ToolName: "http", Payload: `{}`, ExecuteAt: "2000-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	gw.drainHolds(ctx)

	if httpTool.called != 0 {
		t.Fatalf("disabled drain executed %d time(s), want 0", httpTool.called)
	}
	assertHoldState(t, db, "h1", "pending")
}

func assertHoldState(t *testing.T, db *store.DB, id, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT state FROM holds WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read hold state: %v", err)
	}
	if got != want {
		t.Fatalf("hold %s state = %q, want %q", id, got, want)
	}
}
