package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DockerSessionConfig holds settings for sandboxed Docker containers.
type DockerSessionConfig struct {
	Image        string
	NetworkMode  string
	MemoryLimit  string
	CPULimit     string
	AllowedDirs  []string
	ReadonlyDirs []string
	IdleTimeout  time.Duration
}

// DockerSession tracks a single running sandbox container.
type DockerSession struct {
	containerID string
	sessionID   string
	createdAt   time.Time
	lastUsedAt  time.Time
}

// DockerSessionManager manages per-session Docker sandbox containers.
type DockerSessionManager struct {
	mu        sync.Mutex
	sessions  map[string]*DockerSession
	config    DockerSessionConfig
	available bool
	stopCh    chan struct{}
	stopped   bool
}

// NewDockerSessionManager creates a manager. If Docker is available it starts
// a background goroutine that reaps idle sessions.
func NewDockerSessionManager(cfg DockerSessionConfig, dockerAvailable bool) *DockerSessionManager {
	m := &DockerSessionManager{
		sessions:  make(map[string]*DockerSession),
		config:    cfg,
		available: dockerAvailable,
		stopCh:    make(chan struct{}),
	}
	if dockerAvailable {
		go m.idleReaper()
	}
	return m
}

// Available reports whether Docker was detected at startup.
func (m *DockerSessionManager) Available() bool {
	return m.available
}

// GetOrCreate returns an existing session or creates a new sandbox container.
func (m *DockerSessionManager) GetOrCreate(ctx context.Context, sessionID string) (*DockerSession, error) {
	if !m.available {
		return nil, fmt.Errorf("docker sandbox unavailable")
	}

	m.mu.Lock()
	if s, ok := m.sessions[sessionID]; ok {
		s.lastUsedAt = time.Now()
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	containerID, err := m.createContainer(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	if err := m.startContainer(ctx, containerID); err != nil {
		_ = m.removeContainer(ctx, containerID)
		return nil, fmt.Errorf("start container: %w", err)
	}

	now := time.Now()
	s := &DockerSession{
		containerID: containerID,
		sessionID:   sessionID,
		createdAt:   now,
		lastUsedAt:  now,
	}

	m.mu.Lock()
	m.sessions[sessionID] = s
	m.mu.Unlock()

	return s, nil
}

// Exec runs a command inside the session's container and returns structured output.
func (s *DockerSession) Exec(ctx context.Context, command string) (stdout, stderr string, exitCode int, duration time.Duration, err error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "docker", "exec", s.containerID, "bash", "-c", command)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	duration = time.Since(start)
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			err = runErr
			exitCode = -1
		}
	}
	return
}

// Remove stops and removes the container for the given session.
func (m *DockerSessionManager) Remove(sessionID string) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.removeContainer(ctx, s.containerID)
	}
}

// CleanupAll removes every session and stops the idle reaper.
func (m *DockerSessionManager) CleanupAll() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stopCh)

	ids := make([]string, 0, len(m.sessions))
	for _, s := range m.sessions {
		ids = append(ids, s.containerID)
	}
	m.sessions = make(map[string]*DockerSession)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, id := range ids {
		_ = m.removeContainer(ctx, id)
	}
}

// CleanupOrphans removes any leftover ironclaw sandbox containers.
func CleanupOrphans(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=ironclaw=sandbox",
		"--format", "{{.ID}}")
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id == "" {
			continue
		}
		rm := exec.CommandContext(ctx, "docker", "rm", "-f", id)
		_ = rm.Run()
	}
}

// --- internal helpers ---

func (m *DockerSessionManager) createContainer(ctx context.Context, sessionID string) (string, error) {
	name := "ironclaw-sandbox-" + sessionID
	args := []string{
		"create",
		"--name", name,
		"--label", "ironclaw=sandbox",
	}
	if m.config.NetworkMode != "" {
		args = append(args, "--network", m.config.NetworkMode)
	}
	if m.config.MemoryLimit != "" {
		args = append(args, "--memory", m.config.MemoryLimit)
	}
	if m.config.CPULimit != "" {
		args = append(args, "--cpus", m.config.CPULimit)
	}
	for _, dir := range m.config.AllowedDirs {
		args = append(args, "-v", dir+":"+dir)
	}
	for _, dir := range m.config.ReadonlyDirs {
		args = append(args, "-v", dir+":"+dir+":ro")
	}
	args = append(args, m.config.Image, "sleep", "infinity")

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker create: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *DockerSessionManager) startContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "start", containerID)
	return cmd.Run()
}

func (m *DockerSessionManager) removeContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	return cmd.Run()
}

func (m *DockerSessionManager) idleReaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

func (m *DockerSessionManager) reapIdle() {
	m.mu.Lock()
	var expired []string
	for sid, s := range m.sessions {
		if m.config.IdleTimeout > 0 && time.Since(s.lastUsedAt) > m.config.IdleTimeout {
			expired = append(expired, sid)
		}
	}
	expiredSessions := make([]*DockerSession, 0, len(expired))
	for _, sid := range expired {
		expiredSessions = append(expiredSessions, m.sessions[sid])
		delete(m.sessions, sid)
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, s := range expiredSessions {
		_ = m.removeContainer(ctx, s.containerID)
	}
}
