package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestDockerSessionManager_Unavailable(t *testing.T) {
	mgr := NewDockerSessionManager(DockerSessionConfig{
		Image:       "ubuntu:latest",
		IdleTimeout: 10 * time.Minute,
	}, false)
	if mgr.Available() {
		t.Error("should report unavailable")
	}
}

func TestDockerSessionManager_Available(t *testing.T) {
	mgr := NewDockerSessionManager(DockerSessionConfig{
		Image:       "ubuntu:latest",
		IdleTimeout: 10 * time.Minute,
	}, true)
	if !mgr.Available() {
		t.Error("should report available")
	}
	mgr.CleanupAll()
}

func TestDockerSessionManager_GetOrCreate_Unavailable(t *testing.T) {
	mgr := NewDockerSessionManager(DockerSessionConfig{
		Image:       "ubuntu:latest",
		IdleTimeout: 10 * time.Minute,
	}, false)
	_, err := mgr.GetOrCreate(context.Background(), "test-session")
	if err == nil {
		t.Error("should return error when Docker is unavailable")
	}
}
