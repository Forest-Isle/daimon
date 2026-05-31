package util

import (
	"testing"
)

func TestTruncateStr_Shorter(t *testing.T) {
	result := TruncateStr("hello", 100)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateStr_Exact(t *testing.T) {
	result := TruncateStr("hello", 5)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateStr_Longer(t *testing.T) {
	result := TruncateStr("hello world", 5)
	expected := "hello..."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestTruncateStr_Empty(t *testing.T) {
	result := TruncateStr("", 5)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestTruncateStr_ZeroMaxLen(t *testing.T) {
	result := TruncateStr("hello", 0)
	if result != "..." {
		t.Errorf("expected '...', got %q", result)
	}
}

func TestTruncateStr_TruncationAppendsEllipsis(t *testing.T) {
	result := TruncateStr("abcdef", 3)
	if result != "abc..." {
		t.Errorf("expected 'abc...', got %q", result)
	}
}

func TestTruncateStr_ExactlyAtBoundary(t *testing.T) {
	result := TruncateStr("abcde", 5)
	if result != "abcde" {
		t.Errorf("expected 'abcde', got %q", result)
	}
	result2 := TruncateStr("abcdef", 5)
	if result2 != "abcde..." {
		t.Errorf("expected 'abcde...', got %q", result2)
	}
}

func TestTruncateRunes_ASCII(t *testing.T) {
	result := TruncateRunes("hello world", 5)
	expected := "hello…"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestTruncateRunes_Unicode(t *testing.T) {
	result := TruncateRunes("你好世界", 2)
	expected := "你好…"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestTruncateRunes_Shorter(t *testing.T) {
	result := TruncateRunes("hello", 10)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateRunes_Empty(t *testing.T) {
	result := TruncateRunes("", 5)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestTruncateRunes_Exact(t *testing.T) {
	result := TruncateRunes("hello", 5)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateRunes_Zero(t *testing.T) {
	result := TruncateRunes("hello", 0)
	if result != "…" {
		t.Errorf("expected '…', got %q", result)
	}
}

func TestTruncateRunes_MultiByteChars(t *testing.T) {
	// Each emoji is 4 bytes (1 rune)
	result := TruncateRunes("A🎉B🎉C", 3)
	expected := "A🎉B…"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
