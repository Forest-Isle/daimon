package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/action"
	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/proposals"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/telemetry"
	"github.com/Forest-Isle/daimon/internal/world"
)

func newInspectTestGateway(t *testing.T) (*Gateway, *store.DB, *action.Store, *world.Store) {
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
		CREATE TABLE journal (
			id TEXT PRIMARY KEY,
			episode_id TEXT DEFAULT '',
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			detail TEXT DEFAULT '',
			occurred_at DATETIME NOT NULL DEFAULT (datetime('now')),
			rollup_id TEXT DEFAULT '',
			parent_episode_id TEXT DEFAULT '',
			value_created_usd REAL NOT NULL DEFAULT 0
		);
		CREATE TABLE proposals (
			id                TEXT PRIMARY KEY,
			title             TEXT NOT NULL,
			body              TEXT NOT NULL DEFAULT '',
			action_plan       TEXT NOT NULL DEFAULT '',
			urgency           INTEGER NOT NULL DEFAULT 0,
			source_commitment TEXT NOT NULL DEFAULT '',
			state             TEXT NOT NULL DEFAULT 'pending',
			created_at        INTEGER NOT NULL,
			expires_at        INTEGER NOT NULL DEFAULT 0,
			decided_at        INTEGER NOT NULL DEFAULT 0,
			action_kind       TEXT NOT NULL DEFAULT 'episode',
			action_ref        TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX idx_proposals_pending ON proposals(state, expires_at);
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	actionStore := action.NewStore(db.DB)
	worldStore := world.NewStore(db.DB)
	gw := &Gateway{
		db: db,
		toolSub: &ToolSubsystem{
			ActionStore: actionStore,
			WorldStore:  worldStore,
		},
	}
	return gw, db, actionStore, worldStore
}

func TestHandleEpisodes(t *testing.T) {
	gw, _, _, worldStore := newInspectTestGateway(t)
	ctx := context.Background()

	got, err := gw.handleEpisodes(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleEpisodes(empty) error = %v", err)
	}
	if !strings.Contains(got, "No episodes recorded.") {
		t.Fatalf("handleEpisodes(empty) = %q", got)
	}

	if err := worldStore.AppendJournal(ctx, world.JournalEntry{
		ID:         "outcome-1",
		EpisodeID:  "episode-1",
		Kind:       "outcome",
		Summary:    "shipped inspect commands",
		OccurredAt: "2030-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("AppendJournal() error = %v", err)
	}

	got, err = gw.handleEpisodes(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleEpisodes() error = %v", err)
	}
	assertContains(t, got, "episode-1", "shipped inspect commands")
}

func TestHandleTrust(t *testing.T) {
	gw, _, actionStore, _ := newInspectTestGateway(t)
	ctx := context.Background()

	got, err := gw.handleTrust(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleTrust(empty) error = %v", err)
	}
	if !strings.Contains(got, "No trust ledger entries.") {
		t.Fatalf("handleTrust(empty) = %q", got)
	}

	if err := actionStore.RecordAttempt(ctx, action.Reversible, "file.write|repo=daimon", true); err != nil {
		t.Fatalf("RecordAttempt() error = %v", err)
	}

	got, err = gw.handleTrust(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleTrust() error = %v", err)
	}
	assertContains(t, got, "reversible", "file.write|repo=daimon", "ask_first", "1/1")
}

func TestHandleHolds(t *testing.T) {
	gw, _, actionStore, _ := newInspectTestGateway(t)
	ctx := context.Background()

	got, err := gw.handleHolds(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleHolds(empty) error = %v", err)
	}
	if !strings.Contains(got, "No pending holds.") {
		t.Fatalf("handleHolds(empty) = %q", got)
	}

	if err := actionStore.CreateHold(ctx, action.Hold{ID: "hold-1", ToolName: "mail.send", ExecuteAt: "2030-01-01 00:00:00"}); err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	got, err = gw.handleHolds(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleHolds() error = %v", err)
	}
	assertContains(t, got, "hold-1", "mail.send", "2030-01-01")
}

func TestHandleProposals(t *testing.T) {
	gw, _, _, _ := newInspectTestGateway(t)
	ctx := context.Background()

	got, err := gw.handleProposals(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleProposals(empty) error = %v", err)
	}
	if !strings.Contains(got, "No pending proposals.") {
		t.Fatalf("handleProposals(empty) = %q", got)
	}

	if err := proposals.NewStore(gw.db.DB).Create(ctx, proposals.Proposal{
		ID:        "proposal-1",
		Title:     "Review pending release",
		Urgency:   2,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("Create proposal error = %v", err)
	}

	got, err = gw.handleProposals(ctx, nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleProposals() error = %v", err)
	}
	assertContains(t, got, "Review pending release", "2")
}

func TestHandleReplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gw, _, _, _ := newInspectTestGateway(t)

	// Empty state: no replays dir yet → clean zero report, not an error.
	got, err := gw.handleReplay(context.Background(), nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleReplay() error = %v", err)
	}
	assertContains(t, got, "Replay Summary", "sessions", "0", "tool_calls")

	// Seed a real replay file so the handler exercises LoadDir parsing and
	// Analyze aggregation, not just the empty path. One session, one exchange,
	// two tool round-trips (one failed).
	replayDir := filepath.Join(home, ".daimon", "replays")
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReplayJSONL(t, replayDir, "2030-01-01.jsonl",
		replayLine(t, agent.ProviderExchange{SessionID: "s1", Iteration: 0, Model: "m", ResponseText: "hi"}),
		replayLine(t, agent.ToolRoundTrip{SessionID: "s1", ToolName: "world_read", Succeeded: true}),
		replayLine(t, agent.ToolRoundTrip{SessionID: "s1", ToolName: "bash", Succeeded: false}),
	)

	got, err = gw.handleReplay(context.Background(), nil, channel.InboundMessage{})
	if err != nil {
		t.Fatalf("handleReplay(seeded) error = %v", err)
	}
	// tabwriter expands the column separator to spaces, so normalize runs of
	// whitespace before asserting on "<label> <value>" pairs.
	norm := strings.Join(strings.Fields(got), " ")
	assertContains(t, norm,
		"sessions 1",
		"exchanges 1",
		"tool_calls 2",
		"tool_failures 1",
	)
}

// replayLine marshals an agent replay event into a single JSONL record line,
// matching exactly what telemetry.ReplayRecorder writes.
func replayLine(t *testing.T, ev agent.Event) string {
	t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := telemetry.ReplayRecord{TS: "2030-01-01T00:00:00Z", Type: ev.EventType(), Payload: payload}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(b)
}

func writeReplayJSONL(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
