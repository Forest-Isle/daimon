package sandbox

import (
	"context"
	"os/exec"
	"time"
)

// ProbeDocker checks whether the Docker daemon is reachable.
func ProbeDocker(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	return cmd.Run() == nil
}
