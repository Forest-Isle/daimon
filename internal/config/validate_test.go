package config

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestCheckUnknownKeys_NoWarnings(t *testing.T) {
	// Known keys should not trigger warnings.
	yaml := []byte(`
llm:
  provider: claude
  api_key: test
telegram:
  token: test
  allowed_user_ids: [123]
agent:
  mode: simple
store:
  path: ./data/test.db
`)
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	CheckUnknownKeys(yaml)
	if buf.Len() > 0 {
		t.Errorf("unexpected warnings for known keys: %s", buf.String())
	}
}

func TestCheckUnknownKeys_WarnsOnUnknown(t *testing.T) {
	// Unknown top-level keys should trigger warnings.
	yaml := []byte(`
rl:
  enabled: true
  cold_start_episodes: 1000
some_typo:
  foo: bar
llm:
  provider: claude
  api_key: test
telegram:
  token: test
  allowed_user_ids: [123]
`)
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	CheckUnknownKeys(yaml)
	if buf.Len() == 0 {
		t.Error("expected warnings for unknown keys 'rl' and 'some_typo', got none")
	}
	t.Logf("warnings: %s", buf.String())
}

func TestCheckUnknownKeys_InvalidYAML(t *testing.T) {
	// Invalid YAML should not panic.
	yaml := []byte(`this is not valid: {{{`)
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	// Should not panic.
	CheckUnknownKeys(yaml)
}

func TestBuildKnownKeys_HasExpectedKeys(t *testing.T) {
	keys := buildKnownKeys()
	expected := []string{"llm", "telegram", "agent", "store", "memory", "tools", "dashboard", "evolution"}
	for _, k := range expected {
		if !keys[k] {
			t.Errorf("expected key %q in knownTopLevelKeys", k)
		}
	}
	// Verify removed keys are NOT present.
	if keys["rl"] {
		t.Error("'rl' key should not be in knownTopLevelKeys (RL was removed)")
	}
}
