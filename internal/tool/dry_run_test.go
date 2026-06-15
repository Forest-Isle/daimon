package tool

import (
	"context"
	"testing"
)

func TestDryRunContextRoundtrip(t *testing.T) {
	if IsDryRun(context.Background()) {
		t.Fatal("plain context must not be a dry run (fail-closed default)")
	}
	if !IsDryRun(WithDryRun(context.Background())) {
		t.Fatal("WithDryRun context must report IsDryRun true")
	}
}
