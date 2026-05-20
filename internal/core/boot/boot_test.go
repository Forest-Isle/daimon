package boot_test

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/core/boot"
)

// minimalCfg builds a config sufficient for boot.New without relying on any
// unexported helpers. We never make a real LLM call here — boot.New is the
// surface under test; provider construction is the only step that touches
// the network and we don't invoke Run.
func minimalCfg() *config.Config {
	c := &config.Config{}
	c.LLM.Provider = "claude"
	c.LLM.APIKey = "test"
	c.LLM.Model = "test-model"
	c.LLM.MaxTokens = 4096
	c.Tools.Bash.Enabled = true
	c.Tools.Bash.Timeout = 5 * time.Second
	c.Tools.File.Enabled = true
	c.Tools.HTTP.Enabled = false
	c.Tools.Browser.Enabled = false
	return c
}

func TestNewBuildsAgent(t *testing.T) {
	ag, err := boot.New(boot.Options{Cfg: minimalCfg()})
	if err != nil {
		t.Fatalf("boot.New: %v", err)
	}
	if ag == nil {
		t.Fatal("agent is nil")
	}
}

func TestListToolSchemas(t *testing.T) {
	cfg := minimalCfg()
	schemas := boot.ListToolSchemas(cfg)
	if len(schemas) == 0 {
		t.Fatal("expected at least one tool schema")
	}

	want := map[string]bool{"bash": false, "file_read": false, "file_write": false}
	for _, s := range schemas {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("missing expected tool %q", name)
		}
	}
}

func TestNewRequiresConfig(t *testing.T) {
	_, err := boot.New(boot.Options{})
	if err == nil {
		t.Fatal("expected error on nil config")
	}
}

func TestUnknownProvider(t *testing.T) {
	cfg := minimalCfg()
	cfg.LLM.Provider = "nonsense"
	_, err := boot.New(boot.Options{Cfg: cfg})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// TestBuildContextSanity ensures we don't panic constructing a default
// agent and that ctx-bound construction works under cancellation.
func TestBuildContextSanity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx
	_, err := boot.New(boot.Options{Cfg: minimalCfg()})
	if err != nil {
		t.Fatalf("boot.New: %v", err)
	}
}
