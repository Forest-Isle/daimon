package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
)

func TestJSONLExporterRecordsEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	exporter, err := NewJSONLExporter(path)
	if err != nil {
		t.Fatalf("NewJSONLExporter() error = %v", err)
	}
	defer func() { _ = exporter.Close() }()

	if err := exporter.Record(agent.SessionStarted{SessionID: "sess_1", Channel: "tui"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	line := strings.TrimSpace(string(data))
	var record EventRecord
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("record JSON = %v: %s", err, line)
	}
	if record.Type != "session.started" {
		t.Fatalf("type = %q", record.Type)
	}
	if !strings.Contains(string(record.Payload), "sess_1") {
		t.Fatalf("payload = %s", record.Payload)
	}
}
