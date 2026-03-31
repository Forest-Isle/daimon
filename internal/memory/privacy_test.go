package memory

import "testing"

func TestPIIDetectEmail(t *testing.T) {
	d := NewPIIDetector()
	detections := d.Detect("Contact me at john@example.com for details")
	if len(detections) != 1 {
		t.Fatalf("expected 1, got %d", len(detections))
	}
	if detections[0].Type != PIIEmail {
		t.Errorf("expected email, got %s", detections[0].Type)
	}
	if detections[0].Match != "john@example.com" {
		t.Errorf("wrong match: %s", detections[0].Match)
	}
}

func TestPIIDetectPhone(t *testing.T) {
	d := NewPIIDetector()
	if !d.HasPII("Call me at (555) 123-4567") {
		t.Error("should detect phone")
	}
	if !d.HasPII("Call me at 555-123-4567") {
		t.Error("should detect phone dashed")
	}
}

func TestPIIDetectSSN(t *testing.T) {
	d := NewPIIDetector()
	if !d.HasPII("My SSN is 123-45-6789") {
		t.Error("should detect SSN")
	}
}

func TestPIIDetectCreditCard(t *testing.T) {
	d := NewPIIDetector()
	if !d.HasPII("Card: 4111 1111 1111 1111") {
		t.Error("should detect credit card")
	}
}

func TestPIINoPII(t *testing.T) {
	d := NewPIIDetector()
	if d.HasPII("The weather is nice today") {
		t.Error("should not detect PII in normal text")
	}
}

func TestPIISuggestSensitivity(t *testing.T) {
	d := NewPIIDetector()
	if d.SuggestSensitivity("user@test.com") != "private" {
		t.Error("should suggest private for email")
	}
	if d.SuggestSensitivity("just normal text") != "public" {
		t.Error("should suggest public for normal text")
	}
}

func TestPIIRedaction(t *testing.T) {
	d := NewPIIDetector()
	detections := d.Detect("john@example.com")
	if len(detections) == 0 {
		t.Fatal("expected at least one detection")
	}
	// redact: first char + stars for rest of local part + @domain
	// "john" -> "j" + "***" = "j***"
	expected := "j***@example.com"
	if detections[0].Redacted != expected {
		t.Errorf("wrong redaction: got %q, want %q", detections[0].Redacted, expected)
	}
}
