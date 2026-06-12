package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
)

func TestReplayRecorderRecordsReplayEventsFromBus(t *testing.T) {
	dir := t.TempDir()
	recorder, err := NewReplayRecorder(dir)
	if err != nil {
		t.Fatalf("NewReplayRecorder() error = %v", err)
	}
	recorder.now = func() time.Time {
		return time.Date(2026, 6, 12, 3, 4, 5, 6, time.UTC)
	}
	defer func() { _ = recorder.Close() }()

	bus := agent.NewEventBus()
	sub := recorder.Subscribe(bus)
	defer sub.Unsubscribe()

	fullResult := strings.Repeat("full-payload-", 40)
	bus.Publish(agent.SessionStarted{SessionID: "ignored", Channel: "tui"})
	bus.Publish(agent.ProviderExchange{
		SessionID:     "sess_replay",
		Iteration:     7,
		Model:         "test-model",
		Provider:      "test-provider",
		SystemPrompt:  "system prompt",
		MessagesJSON:  json.RawMessage(`[{"role":"user","content":"hello"}]`),
		ResponseText:  "assistant response",
		ToolCallsJSON: json.RawMessage(`[{"id":"toolu_1","name":"bash","input":{"cmd":"printf hi"}}]`),
		StopReason:    "tool_use",
		DurationMs:    123,
	})
	bus.Publish(agent.ToolRoundTrip{
		SessionID:  "sess_replay",
		Iteration:  7,
		ToolName:   "bash",
		ArgsJSON:   json.RawMessage(`{"cmd":"printf hi"}`),
		ResultJSON: json.RawMessage(`{"output":"` + fullResult + `","metadata":{"source":"unit-test"}}`),
		Succeeded:  true,
		DurationMs: 45,
	})
	bus.Publish(agent.TurnClosed{
		SessionID:  "sess_replay",
		FinalReply: "final reply",
	})

	path := filepath.Join(dir, "2026-06-12.jsonl")
	records := waitReplayRecords(t, path, 3)
	if len(records) != 3 {
		t.Fatalf("record count = %d, want 3", len(records))
	}

	byType := make(map[string]ReplayRecord, len(records))
	for _, record := range records {
		if record.TS != "2026-06-12T03:04:05.000000006Z" {
			t.Fatalf("ts = %q", record.TS)
		}
		byType[record.Type] = record
	}
	if _, ok := byType["session.started"]; ok {
		t.Fatal("non-replay event was recorded")
	}

	var provider agent.ProviderExchange
	mustUnmarshalPayload(t, byType["replay.provider_exchange"], &provider)
	if provider.SessionID != "sess_replay" || provider.ResponseText != "assistant response" {
		t.Fatalf("provider payload = %#v", provider)
	}
	assertJSONEqual(t, provider.MessagesJSON, `[{"role":"user","content":"hello"}]`)
	assertJSONEqual(t, provider.ToolCallsJSON, `[{"id":"toolu_1","name":"bash","input":{"cmd":"printf hi"}}]`)

	var toolTrip agent.ToolRoundTrip
	mustUnmarshalPayload(t, byType["replay.tool_round_trip"], &toolTrip)
	if toolTrip.ToolName != "bash" || !toolTrip.Succeeded {
		t.Fatalf("tool payload = %#v", toolTrip)
	}
	assertJSONEqual(t, toolTrip.ArgsJSON, `{"cmd":"printf hi"}`)
	if !strings.Contains(string(toolTrip.ResultJSON), fullResult) {
		t.Fatalf("result payload missing full output: %s", toolTrip.ResultJSON)
	}

	var turn agent.TurnClosed
	mustUnmarshalPayload(t, byType["replay.turn_closed"], &turn)
	if turn.FinalReply != "final reply" {
		t.Fatalf("turn payload = %#v", turn)
	}
}

func TestReplayRecorderRollsOverByDate(t *testing.T) {
	dir := t.TempDir()
	recorder, err := NewReplayRecorder(dir)
	if err != nil {
		t.Fatalf("NewReplayRecorder() error = %v", err)
	}
	now := time.Date(2026, 6, 12, 23, 59, 0, 0, time.UTC)
	recorder.now = func() time.Time { return now }
	defer func() { _ = recorder.Close() }()

	if err := recorder.Record(agent.TurnClosed{SessionID: "day_one", FinalReply: "one"}); err != nil {
		t.Fatalf("Record(day one) error = %v", err)
	}
	now = time.Date(2026, 6, 13, 0, 1, 0, 0, time.UTC)
	if err := recorder.Record(agent.TurnClosed{SessionID: "day_two", FinalReply: "two"}); err != nil {
		t.Fatalf("Record(day two) error = %v", err)
	}

	first := readFile(t, filepath.Join(dir, "2026-06-12.jsonl"))
	second := readFile(t, filepath.Join(dir, "2026-06-13.jsonl"))
	if !strings.Contains(first, "day_one") || strings.Contains(first, "day_two") {
		t.Fatalf("first file contents = %s", first)
	}
	if !strings.Contains(second, "day_two") || strings.Contains(second, "day_one") {
		t.Fatalf("second file contents = %s", second)
	}
}

func waitReplayRecords(t *testing.T, path string, want int) []ReplayRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		lines := strings.Split(trimmed, "\n")
		if len(lines) == want {
			records := make([]ReplayRecord, 0, len(lines))
			for _, line := range lines {
				var record ReplayRecord
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("record JSON = %v: %s", err, line)
				}
				if !json.Valid(record.Payload) {
					t.Fatalf("payload is not JSON: %s", record.Payload)
				}
				records = append(records, record)
			}
			return records
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("read replay file: %v", lastErr)
	}
	t.Fatalf("timed out waiting for %d replay records in %s", want, path)
	return nil
}

func mustUnmarshalPayload(t *testing.T, record ReplayRecord, target any) {
	t.Helper()
	if len(record.Payload) == 0 {
		t.Fatalf("missing payload for record type %q", record.Type)
	}
	if err := json.Unmarshal(record.Payload, target); err != nil {
		t.Fatalf("payload JSON = %v: %s", err, record.Payload)
	}
}

func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotBuf bytes.Buffer
	if err := json.Compact(&gotBuf, got); err != nil {
		t.Fatalf("got JSON invalid: %v: %s", err, got)
	}
	var wantBuf bytes.Buffer
	if err := json.Compact(&wantBuf, []byte(want)); err != nil {
		t.Fatalf("want JSON invalid: %v: %s", err, want)
	}
	if gotBuf.String() != wantBuf.String() {
		t.Fatalf("JSON = %s, want %s", gotBuf.String(), wantBuf.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}
