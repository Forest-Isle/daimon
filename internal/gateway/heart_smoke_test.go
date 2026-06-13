//go:build smoke

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/heart"
)

// TestHeartAutonomousLoopSmoke boots a real gateway with the heart enabled,
// fires a fast timer, and verifies the autonomous loop runs end to end in a live
// process: timer → heart persist → attention route → dispatch → internal episode.
//
// It is gated behind the `smoke` build tag so it never runs in `make test`. Run
// it explicitly:
//
//	CGO_ENABLED=1 go test -tags 'fts5 smoke' -run TestHeartAutonomousLoopSmoke -v -count=1 ./internal/gateway/
//
// Isolation: everything (DB, world, replays) lives under a temp HOME — it never
// touches ~/.daimon. It makes ONE real LLM call against whatever provider the
// ANTHROPIC_* env vars point at; if those creds are absent or rejected the
// episode fails fast (retries disabled) and only the journal stays empty — the
// wiring assertion still holds.
func TestHeartAutonomousLoopSmoke(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".daimon")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	// Standard provider config from env. Set these before running:
	//   ANTHROPIC_API_KEY  — the x-api-key for the endpoint
	//   ANTHROPIC_BASE_URL — the endpoint base URL
	//   DAIMON_SMOKE_MODEL — model id (defaults to claude-haiku-4-5)
	model := os.Getenv("DAIMON_SMOKE_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	dbPath := filepath.Join(base, "data", "daimon.db")
	cfgYAML := "" +
		"llm:\n" +
		"  provider: claude\n" +
		"  api_key: \"${ANTHROPIC_API_KEY}\"\n" +
		"  base_url: \"${ANTHROPIC_BASE_URL}\"\n" +
		"  model: \"" + model + "\"\n" +
		"  max_tokens: 1024\n" +
		"  retry:\n" +
		"    max_retries: 0\n" +
		"store:\n" +
		"  path: " + dbPath + "\n" +
		"agent:\n" +
		"  episode_enabled: true\n" +
		"  heart_enabled: true\n"
	cfgPath := filepath.Join(base, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	gw, err := New(cfg, GatewayOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("gateway New: %v", err)
	}
	if gw.heart == nil {
		t.Fatal("heart not built despite heart_enabled=true")
	}

	// Fast timer so the smoke finishes in seconds (the config interval is whole
	// minutes). In-package access lets the test register a source directly.
	gw.heart.heart.Register(&heart.TimerSource{
		SourceName: "smoke",
		Kind:       "internal.heartbeat",
		Interval:   2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := gw.Start(ctx); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() { _ = gw.Stop(context.Background()) }()

	// Poll the event/journal tables. A persisted heartbeat proves timer+heart;
	// routed_at proves the dispatcher ran; a journal row proves the episode
	// closed (LLM round-trip succeeded).
	var persisted, routed, journalRows int
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		_ = gw.db.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='internal.heartbeat'`).Scan(&persisted)
		_ = gw.db.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='internal.heartbeat' AND routed_at IS NOT NULL`).Scan(&routed)
		_ = gw.db.DB.QueryRow(`SELECT COUNT(*) FROM journal`).Scan(&journalRows)
		if routed > 0 && journalRows > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Logf("SMOKE: heartbeat events persisted=%d routed=%d | journal rows=%d", persisted, routed, journalRows)
	if persisted == 0 {
		t.Fatal("no internal.heartbeat event persisted — timer/heart did not run in the live process")
	}
	if routed == 0 {
		t.Fatal("heartbeat persisted but never routed — attention/dispatch did not run")
	}
	if journalRows == 0 {
		t.Log("WARNING: routed but no journal row — dispatch+episode ran but the LLM call did not close an outcome (check provider creds/model). Wiring is proven; full cognition is not.")
	} else {
		t.Log("FULL LOOP PROVEN: timer → heart → attention → episode → journal outcome.")
	}
}
