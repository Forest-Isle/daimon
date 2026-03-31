package memory

import (
	"regexp"
	"strings"
)

// PIIType identifies the type of PII detected.
type PIIType string

const (
	PIIEmail      PIIType = "email"
	PIIPhone      PIIType = "phone"
	PIISSNumber   PIIType = "ssn"
	PIICreditCard PIIType = "credit_card"
)

// PIIDetection represents a detected PII instance.
type PIIDetection struct {
	Type     PIIType
	Match    string
	Redacted string // masked version e.g. "j***@example.com"
}

// PIIDetector detects personally identifiable information in text.
type PIIDetector struct {
	patterns map[PIIType]*regexp.Regexp
}

// NewPIIDetector creates a new PII detector with standard regex patterns.
func NewPIIDetector() *PIIDetector {
	return &PIIDetector{
		patterns: map[PIIType]*regexp.Regexp{
			PIIEmail:      regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
			PIIPhone:      regexp.MustCompile(`(?:(?:\+?1[\s\-.]?)?\(?[0-9]{3}\)?[\s\-.]?[0-9]{3}[\s\-.]?[0-9]{4})`),
			PIISSNumber:   regexp.MustCompile(`\b\d{3}[\-\s]?\d{2}[\-\s]?\d{4}\b`),
			PIICreditCard: regexp.MustCompile(`\b(?:\d{4}[\s\-]?){3}\d{4}\b`),
		},
	}
}

// Detect scans text for PII patterns and returns all detections.
func (d *PIIDetector) Detect(text string) []PIIDetection {
	var detections []PIIDetection
	for piiType, pattern := range d.patterns {
		matches := pattern.FindAllString(text, -1)
		for _, match := range matches {
			detections = append(detections, PIIDetection{
				Type:     piiType,
				Match:    match,
				Redacted: redact(match, piiType),
			})
		}
	}
	return detections
}

// HasPII returns true if any PII is detected in the text.
func (d *PIIDetector) HasPII(text string) bool {
	for _, pattern := range d.patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// SuggestSensitivity returns the recommended sensitivity level based on PII detection.
func (d *PIIDetector) SuggestSensitivity(text string) string {
	if d.HasPII(text) {
		return "private"
	}
	return "public"
}

func redact(match string, piiType PIIType) string {
	switch piiType {
	case PIIEmail:
		parts := strings.SplitN(match, "@", 2)
		if len(parts) == 2 {
			name := parts[0]
			if len(name) > 1 {
				return string(name[0]) + strings.Repeat("*", len(name)-1) + "@" + parts[1]
			}
			return "*@" + parts[1]
		}
		return "***"
	case PIIPhone:
		if len(match) >= 4 {
			return strings.Repeat("*", len(match)-4) + match[len(match)-4:]
		}
		return "****"
	case PIISSNumber:
		return "***-**-" + match[len(match)-4:]
	case PIICreditCard:
		if len(match) >= 4 {
			return strings.Repeat("*", len(match)-4) + match[len(match)-4:]
		}
		return "****"
	default:
		return "***"
	}
}
