package mcp

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// ---------------------------------------------------------------------------
// Mock: MCPClient for Manager tests (tracks Close calls via atomic counter)
// ---------------------------------------------------------------------------

type managerMockClient struct {
	mockMCPClient          // embed adapter mock for interface satisfaction
	closeCalled atomic.Bool
	closeErr    error
}

func (m *managerMockClient) Close() error {
	m.closeCalled.Store(true)
	return m.closeErr
}

// ---------------------------------------------------------------------------
// Tests: NewManager
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
	if n := len(m.RunningServers()); n != 0 {
		t.Errorf("new manager has %d running servers, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Tests: Close
// ---------------------------------------------------------------------------

func TestManager_Close_Empty(t *testing.T) {
	m := NewManager()
	if err := m.Close(); err != nil {
		t.Errorf("Close() on empty manager: %v", err)
	}
}

func TestManager_Close_Idempotent(t *testing.T) {
	m := NewManager()
	for i := 0; i < 3; i++ {
		if err := m.Close(); err != nil {
			t.Errorf("Close() #%d: %v", i+1, err)
		}
	}
}

func TestManager_Close_MultipleClients(t *testing.T) {
	m := NewManager()
	clients := make([]*managerMockClient, 3)
	m.mu.Lock()
	for i := range clients {
		clients[i] = &managerMockClient{}
		m.clients[fmt.Sprintf("client%d", i)] = clients[i]
	}
	m.mu.Unlock()

	if err := m.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
	for i, c := range clients {
		if !c.closeCalled.Load() {
			t.Errorf("client %d: Close() not called", i)
		}
	}
	if n := len(m.RunningServers()); n != 0 {
		t.Errorf("after Close(), %d servers still running", n)
	}
}

func TestManager_Close_WithClientError(t *testing.T) {
	m := NewManager()
	c := &managerMockClient{closeErr: errors.New("fail")}
	m.mu.Lock()
	m.clients["bad"] = c
	m.mu.Unlock()

	// Close logs the error but returns nil.
	if err := m.Close(); err != nil {
		t.Errorf("Close() should return nil even if client errors, got: %v", err)
	}
	if !c.closeCalled.Load() {
		t.Error("client Close() was not called")
	}
}

// ---------------------------------------------------------------------------
// Tests: RunningServers
// ---------------------------------------------------------------------------

func TestManager_RunningServers_Populated(t *testing.T) {
	m := NewManager()
	m.mu.Lock()
	m.clients["s1"] = &managerMockClient{}
	m.clients["s2"] = &managerMockClient{}
	m.mu.Unlock()

	running := m.RunningServers()
	if len(running) != 2 {
		t.Fatalf("RunningServers() returned %d, want 2", len(running))
	}
	for _, name := range []string{"s1", "s2"} {
		if _, ok := running[name]; !ok {
			t.Errorf("%q missing from RunningServers()", name)
		}
	}
}

func TestManager_RunningServers_ConcurrentSafe(t *testing.T) {
	m := NewManager()
	m.mu.Lock()
	m.clients["x"] = &managerMockClient{}
	m.mu.Unlock()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			rs := m.RunningServers()
			if len(rs) != 1 {
				t.Errorf("concurrent RunningServers() returned %d, want 1", len(rs))
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// ---------------------------------------------------------------------------
// Tests: StopServer
// ---------------------------------------------------------------------------

func TestManager_StopServer_NonExistent(t *testing.T) {
	m := NewManager()
	reg := tool.NewRegistry()
	// Must not panic.
	m.StopServer("ghost", reg)
}

func TestManager_StopServer_ClosesAndUnregisters(t *testing.T) {
	m := NewManager()
	reg := tool.NewRegistry()

	c := &managerMockClient{}
	m.mu.Lock()
	m.clients["srv"] = c
	m.mu.Unlock()

	// Pre-register some tools that should be cleaned up.
	reg.Register(NewToolAdapter(&mockMCPClient{}, "srv", mcp.Tool{Name: "a"}, false))
	reg.Register(NewToolAdapter(&mockMCPClient{}, "srv", mcp.Tool{Name: "b"}, false))
	// Tool from another server — must survive.
	reg.Register(NewToolAdapter(&mockMCPClient{}, "other", mcp.Tool{Name: "c"}, false))

	m.StopServer("srv", reg)

	if !c.closeCalled.Load() {
		t.Error("client Close() not called")
	}
	if _, ok := m.RunningServers()["srv"]; ok {
		t.Error("server still listed as running after StopServer")
	}
	// "mcp_srv_a" and "mcp_srv_b" should be gone.
	if _, err := reg.Get("mcp_srv_a"); err == nil {
		t.Error("mcp_srv_a still registered")
	}
	if _, err := reg.Get("mcp_srv_b"); err == nil {
		t.Error("mcp_srv_b still registered")
	}
	// "mcp_other_c" should remain.
	if _, err := reg.Get("mcp_other_c"); err != nil {
		t.Errorf("mcp_other_c incorrectly unregistered: %v", err)
	}
}

func TestManager_StopServer_Idempotent(t *testing.T) {
	m := NewManager()
	reg := tool.NewRegistry()

	c := &managerMockClient{}
	m.mu.Lock()
	m.clients["srv"] = c
	m.mu.Unlock()

	m.StopServer("srv", reg)
	if !c.closeCalled.Load() {
		t.Fatal("first StopServer did not close client")
	}

	// Second call on the same server must not panic.
	m.StopServer("srv", reg)
}

func TestManager_StopServer_OnlyTargetClosed(t *testing.T) {
	m := NewManager()
	reg := tool.NewRegistry()

	c1 := &managerMockClient{}
	c2 := &managerMockClient{}
	m.mu.Lock()
	m.clients["s1"] = c1
	m.clients["s2"] = c2
	m.mu.Unlock()

	m.StopServer("s1", reg)

	if !c1.closeCalled.Load() {
		t.Error("s1 client not closed")
	}
	if c2.closeCalled.Load() {
		t.Error("s2 client should not be closed")
	}
	running := m.RunningServers()
	if _, ok := running["s1"]; ok {
		t.Error("s1 still running")
	}
	if _, ok := running["s2"]; !ok {
		t.Error("s2 should still be running")
	}
}
