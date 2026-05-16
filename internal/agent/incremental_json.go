package agent

import (
	"encoding/json"
	"strings"
)

// IncrementalJSONParser extracts complete JSON values from streaming text.
type IncrementalJSONParser struct {
	buf      strings.Builder
	depth    int
	inString bool
	escaped  bool
}

// NewIncrementalJSONParser creates a new parser.
func NewIncrementalJSONParser() *IncrementalJSONParser {
	return &IncrementalJSONParser{}
}

// Feed appends a chunk of text to the buffer.
func (p *IncrementalJSONParser) Feed(chunk string) {
	if chunk == "" {
		return
	}
	p.buf.WriteString(chunk)
}

// ExtractCompleteObjects finds and extracts fully-closed JSON objects/arrays.
func (p *IncrementalJSONParser) ExtractCompleteObjects() []json.RawMessage {
	text := p.buf.String()
	if text == "" {
		return nil
	}

	var (
		results []json.RawMessage
		keep    strings.Builder
		start   = -1
	)

	p.depth = 0
	p.inString = false
	p.escaped = false

	for i := 0; i < len(text); i++ {
		ch := text[i]

		if p.inString {
			if p.escaped {
				p.escaped = false
				continue
			}
			if ch == '\\' {
				p.escaped = true
				continue
			}
			if ch == '"' {
				p.inString = false
			}
			continue
		}

		switch ch {
		case '"':
			p.inString = true
		case '{', '[':
			if p.depth == 0 {
				start = i
			}
			p.depth++
		case '}', ']':
			if p.depth == 0 {
				continue
			}
			p.depth--
			if p.depth == 0 && start >= 0 {
				raw := strings.TrimSpace(text[start : i+1])
				if json.Valid([]byte(raw)) {
					results = append(results, json.RawMessage(raw))
				} else {
					keep.WriteString(raw)
				}
				start = -1
			}
		default:
			if p.depth == 0 && start == -1 {
				keep.WriteByte(ch)
			}
		}
	}

	if start >= 0 {
		keep.WriteString(text[start:])
	}

	p.buf.Reset()
	p.buf.WriteString(strings.TrimSpace(keep.String()))
	return results
}

// Finalize parses whatever remains in the buffer.
func (p *IncrementalJSONParser) Finalize() []json.RawMessage {
	results := p.ExtractCompleteObjects()
	remaining := strings.TrimSpace(p.buf.String())
	if remaining == "" {
		return results
	}
	if json.Valid([]byte(remaining)) {
		results = append(results, json.RawMessage(remaining))
		p.buf.Reset()
	}
	return results
}
