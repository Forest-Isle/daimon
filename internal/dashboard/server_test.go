package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAgentStateEndpoint(t *testing.T) {
	bus := NewBus(16)
	tracker := NewAgentStateTracker(bus)

	deps := ServerDeps{
		Tracker:  tracker,
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/agent/state")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap StateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status != "idle" {
		t.Fatalf("status = %s, want idle", snap.Status)
	}
}

func TestHealthEndpoint(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["status"] != "ok" {
		t.Fatalf("health = %v, want ok", m["status"])
	}
}

func TestSPAFallback(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html>SPA</html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/some/route")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<html>SPA</html>" {
		t.Fatalf("SPA fallback failed, got %s", string(body))
	}
}

func TestSessionsEndpointNoDB(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSessionMessagesNoDB(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/abc/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSessionToolsNoDB(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/abc/tools")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestTokenAuthWrongToken(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
		Token:    "correct-token",
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/agent/state", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}
}

func TestTokenAuth(t *testing.T) {
	deps := ServerDeps{
		Tracker:  NewAgentStateTracker(NewBus(1)),
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
		Token:    "secret123",
	}
	handler := NewServerMux(deps)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// No token → 401
	resp, _ := http.Get(srv.URL + "/api/agent/state")
	if resp.StatusCode != 401 {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}

	// With token → 200
	req, _ := http.NewRequest("GET", srv.URL+"/api/agent/state", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("with token: status = %d, want 200", resp.StatusCode)
	}

	// Token via query param (for WebSocket) → 200
	resp, _ = http.Get(srv.URL + "/api/agent/state?token=secret123")
	if resp.StatusCode != 200 {
		t.Fatalf("query token: status = %d, want 200", resp.StatusCode)
	}
}
