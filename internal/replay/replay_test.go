package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/telemetry"
)

// line marshals an agent replay event into a single JSONL journal line, matching
// exactly what telemetry.ReplayRecorder writes.
func line(t *testing.T, ev agent.Event) string {
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

// writeJSONL writes lines (already-rendered) joined by newlines, with a trailing
// newline, to <dir>/<name>.
func writeJSONL(t *testing.T, dir, name string, lines ...string) {
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

func TestLoadDirReconstructsSessions(t *testing.T) {
	dir := t.TempDir()
	// Two sessions interleaved within one day file.
	writeJSONL(t, dir, "2030-01-01.jsonl",
		line(t, agent.ProviderExchange{SessionID: "s1", Iteration: 0, Model: "m", ResponseText: "hi"}),
		line(t, agent.ToolRoundTrip{SessionID: "s1", ToolName: "world_read", Succeeded: true}),
		line(t, agent.ProviderExchange{SessionID: "s2", Iteration: 0, Model: "m"}),
		line(t, agent.ToolRoundTrip{SessionID: "s1", ToolName: "bash", Succeeded: false}),
		line(t, agent.TurnClosed{SessionID: "s1", FinalReply: "done"}),
		line(t, agent.EpisodeSalvaged{SessionID: "s2", EpisodeID: "e1"}),
		line(t, agent.TurnClosed{SessionID: "s2", FinalReply: "salvaged reply"}),
	)

	sessions, skipped, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Order = first appearance: s1 then s2.
	if sessions[0].SessionID != "s1" || sessions[1].SessionID != "s2" {
		t.Fatalf("session order wrong: %q, %q", sessions[0].SessionID, sessions[1].SessionID)
	}

	s1 := sessions[0]
	if len(s1.Exchanges) != 1 || len(s1.Tools) != 2 {
		t.Fatalf("s1 shape wrong: %d exchanges, %d tools", len(s1.Exchanges), len(s1.Tools))
	}
	if s1.Tools[0].ToolName != "world_read" || s1.Tools[1].ToolName != "bash" {
		t.Fatal("s1 tool order not preserved")
	}
	if s1.FinalReply != "done" {
		t.Fatalf("s1 final reply: %q", s1.FinalReply)
	}
	if s1.Salvaged {
		t.Fatal("s1 must not be salvaged")
	}

	s2 := sessions[1]
	if !s2.Salvaged {
		t.Fatal("s2 must be salvaged")
	}
	if s2.FinalReply != "salvaged reply" {
		t.Fatalf("s2 final reply: %q", s2.FinalReply)
	}
}

func TestLoadDirAcrossDaysChronological(t *testing.T) {
	dir := t.TempDir()
	// Same session spans two day files; later file must extend, not replace.
	writeJSONL(t, dir, "2030-01-02.jsonl",
		line(t, agent.ProviderExchange{SessionID: "s1", Iteration: 1, ResponseText: "day2"}),
	)
	writeJSONL(t, dir, "2030-01-01.jsonl",
		line(t, agent.ProviderExchange{SessionID: "s1", Iteration: 0, ResponseText: "day1"}),
	)

	sessions, _, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(sessions) != 1 || len(sessions[0].Exchanges) != 2 {
		t.Fatalf("expected 1 session with 2 exchanges, got %d sessions", len(sessions))
	}
	// Files are read in filename (date) order, so day1 precedes day2.
	if sessions[0].Exchanges[0].ResponseText != "day1" || sessions[0].Exchanges[1].ResponseText != "day2" {
		t.Fatal("cross-day exchanges not chronological")
	}
}

func TestParseFileSkipsMalformedAndTornFinalLine(t *testing.T) {
	dir := t.TempDir()
	good := line(t, agent.ProviderExchange{SessionID: "s1", Model: "m"})
	// One good line, one garbage interior line, one torn final line (no newline,
	// truncated JSON) — both bad lines counted as skipped, good line parsed.
	content := good + "\n" + "{not valid json" + "\n" + `{"ts":"x","type":"replay.turn_closed","payl`
	if err := os.WriteFile(filepath.Join(dir, "2030-01-01.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, skipped, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if skipped != 2 {
		t.Fatalf("expected 2 skipped lines, got %d", skipped)
	}
	if len(sessions) != 1 || len(sessions[0].Exchanges) != 1 {
		t.Fatalf("good line not parsed: %d sessions", len(sessions))
	}
}

func TestLoadDirMissingDir(t *testing.T) {
	sessions, skipped, err := LoadDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir must not error, got %v", err)
	}
	if sessions != nil || skipped != 0 {
		t.Fatalf("missing dir must yield no sessions, got %d / skipped %d", len(sessions), skipped)
	}
}

func TestLoadDirIgnoresNonJSONL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("garbage not jsonl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, dir, "2030-01-01.jsonl", line(t, agent.ProviderExchange{SessionID: "s1"}))

	sessions, skipped, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(sessions) != 1 || skipped != 0 {
		t.Fatalf("non-jsonl file must be ignored: %d sessions, %d skipped", len(sessions), skipped)
	}
}

func TestAnalyzeMetrics(t *testing.T) {
	sessions := []Session{
		{
			SessionID: "s1",
			Exchanges: []agent.ProviderExchange{
				{StopReason: string(agent.StopEndTurn)},
				{StopReason: string(agent.StopAbnormal)},
				{StopReason: string(agent.StopMaxToken)},
			},
			Tools: []agent.ToolRoundTrip{
				{Succeeded: true},
				{Succeeded: false},
			},
		},
		{
			SessionID: "s2",
			Exchanges: []agent.ProviderExchange{{StopReason: string(agent.StopEndTurn)}},
			Salvaged:  true,
		},
	}

	rep := Analyze(sessions, 3)
	if rep.Sessions != 2 {
		t.Fatalf("sessions: %d", rep.Sessions)
	}
	if rep.Exchanges != 4 {
		t.Fatalf("exchanges: %d", rep.Exchanges)
	}
	if rep.ToolCalls != 2 || rep.ToolFailures != 1 {
		t.Fatalf("tool calls/failures: %d/%d", rep.ToolCalls, rep.ToolFailures)
	}
	if rep.AbnormalStops != 1 || rep.MaxTokenStops != 1 {
		t.Fatalf("abnormal/maxtoken: %d/%d", rep.AbnormalStops, rep.MaxTokenStops)
	}
	if rep.Salvaged != 1 {
		t.Fatalf("salvaged: %d", rep.Salvaged)
	}
	if rep.SkippedLines != 3 {
		t.Fatalf("skipped carried through: %d", rep.SkippedLines)
	}
	if len(rep.PerSession) != 2 || rep.PerSession[0].SessionID != "s1" {
		t.Fatal("per-session metrics missing or misordered")
	}
	if rep.PerSession[0].AbnormalStops != 1 || rep.PerSession[0].MaxTokenStops != 1 {
		t.Fatal("per-session s1 stop counts wrong")
	}
	if !rep.PerSession[1].Salvaged {
		t.Fatal("per-session s2 salvaged flag wrong")
	}
}
