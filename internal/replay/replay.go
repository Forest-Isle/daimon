// Package replay reads the append-only replay journals written by the telemetry
// recorder (one JSONL file per day under <appdir>/replays) and reconstructs the
// agent sessions they captured. It is the read side of the replay harness: it
// turns recorded provider exchanges, tool round-trips and turn closures back
// into structured Session values and computes offline health metrics over them.
//
// This package never re-runs anything and never contacts a provider — it only
// analyzes what was recorded. Re-running recorded exchanges against a candidate
// config ("daimon replay --against <config>") builds on the Session model here.
package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/telemetry"
)

// Session is one agent session reconstructed from its recorded replay events.
// Exchanges and Tools preserve recorded (chronological) order; FinalReply is the
// last TurnClosed reply seen for the session, and Salvaged is true if any episode
// in the session had to be framework-recovered.
type Session struct {
	SessionID  string
	Exchanges  []agent.ProviderExchange
	Tools      []agent.ToolRoundTrip
	FinalReply string
	Salvaged   bool
}

// SessionMetrics is the offline health summary for a single session.
type SessionMetrics struct {
	SessionID     string
	Exchanges     int
	ToolCalls     int
	ToolFailures  int
	AbnormalStops int
	MaxTokenStops int
	Salvaged      bool
}

// Report aggregates offline metrics across the analyzed sessions. The counters
// are diagnostics, not a verdict: a replay journal is best-effort telemetry, so
// SkippedLines records lines that could not be parsed (e.g. a torn final write
// from a crash) rather than failing the whole report.
type Report struct {
	Sessions      int
	Exchanges     int
	ToolCalls     int
	ToolFailures  int
	AbnormalStops int
	MaxTokenStops int
	Salvaged      int
	SkippedLines  int
	PerSession    []SessionMetrics
}

// event type tags as emitted by agent.Event.EventType().
const (
	typeProviderExchange = "replay.provider_exchange"
	typeToolRoundTrip    = "replay.tool_round_trip"
	typeTurnClosed       = "replay.turn_closed"
	typeEpisodeSalvaged  = "replay.episode_salvaged"
)

// LoadDir reads every *.jsonl replay file under dir in filename (date) order and
// reconstructs the sessions they contain. A missing directory yields no sessions
// (nothing has been recorded yet), not an error. Unparseable lines are skipped
// and counted in the returned skipped total.
func LoadDir(dir string) (sessions []Session, skipped int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read replay dir: %w", err)
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths) // YYYY-MM-DD.jsonl sorts chronologically

	var records []telemetry.ReplayRecord
	for _, p := range paths {
		recs, skip, err := parseFile(p)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, recs...)
		skipped += skip
	}

	return buildSessions(records), skipped, nil
}

// parseFile reads one JSONL replay file. It tolerates arbitrarily long lines
// (full system prompts and message arrays) and skips lines it cannot decode,
// returning the skip count. A torn final line left by a crash mid-write is one
// such skipped line, not a hard failure.
func parseFile(path string) (records []telemetry.ReplayRecord, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open replay file: %w", err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := trimNewline(line)
			if len(trimmed) > 0 {
				var rec telemetry.ReplayRecord
				if json.Unmarshal(trimmed, &rec) != nil {
					skipped++
				} else {
					records = append(records, rec)
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("read replay file %s: %w", path, readErr)
		}
	}
	return records, skipped, nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// buildSessions groups records by SessionID, preserving the order in which each
// session first appears. Records arrive chronologically (append order within a
// day, days in filename order), so per-session slices stay in recorded order.
func buildSessions(records []telemetry.ReplayRecord) []Session {
	byID := make(map[string]*Session)
	var order []string

	get := func(id string) *Session {
		s, ok := byID[id]
		if !ok {
			s = &Session{SessionID: id}
			byID[id] = s
			order = append(order, id)
		}
		return s
	}

	for _, rec := range records {
		switch rec.Type {
		case typeProviderExchange:
			var ev agent.ProviderExchange
			if json.Unmarshal(rec.Payload, &ev) != nil {
				continue
			}
			s := get(ev.SessionID)
			s.Exchanges = append(s.Exchanges, ev)
		case typeToolRoundTrip:
			var ev agent.ToolRoundTrip
			if json.Unmarshal(rec.Payload, &ev) != nil {
				continue
			}
			s := get(ev.SessionID)
			s.Tools = append(s.Tools, ev)
		case typeTurnClosed:
			var ev agent.TurnClosed
			if json.Unmarshal(rec.Payload, &ev) != nil {
				continue
			}
			s := get(ev.SessionID)
			s.FinalReply = ev.FinalReply
		case typeEpisodeSalvaged:
			var ev agent.EpisodeSalvaged
			if json.Unmarshal(rec.Payload, &ev) != nil {
				continue
			}
			s := get(ev.SessionID)
			s.Salvaged = true
		default:
			// Unknown record type: ignore (forward-compatible with new events).
		}
	}

	sessions := make([]Session, 0, len(order))
	for _, id := range order {
		sessions = append(sessions, *byID[id])
	}
	return sessions
}

// Analyze computes the offline health report over the given sessions. skipped is
// the count of unparseable journal lines carried through from loading.
func Analyze(sessions []Session, skipped int) Report {
	rep := Report{SkippedLines: skipped, PerSession: make([]SessionMetrics, 0, len(sessions))}
	for _, s := range sessions {
		m := SessionMetrics{SessionID: s.SessionID, Salvaged: s.Salvaged}
		for _, ex := range s.Exchanges {
			m.Exchanges++
			switch ex.StopReason {
			case string(mind.StopAbnormal):
				m.AbnormalStops++
			case string(mind.StopMaxToken):
				m.MaxTokenStops++
			}
		}
		for _, t := range s.Tools {
			m.ToolCalls++
			if !t.Succeeded {
				m.ToolFailures++
			}
		}

		rep.Sessions++
		rep.Exchanges += m.Exchanges
		rep.ToolCalls += m.ToolCalls
		rep.ToolFailures += m.ToolFailures
		rep.AbnormalStops += m.AbnormalStops
		rep.MaxTokenStops += m.MaxTokenStops
		if m.Salvaged {
			rep.Salvaged++
		}
		rep.PerSession = append(rep.PerSession, m)
	}
	return rep
}
