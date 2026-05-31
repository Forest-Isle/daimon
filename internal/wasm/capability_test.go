package wasm

import (
	"testing"
)

func TestNetworkPolicy_IsAllowed(t *testing.T) {
	tests := []struct {
		name   string
		policy *NetworkPolicy
		host   string
		port   int
		allow  bool
	}{
		{
			name:   "nil policy denies all",
			policy: nil,
			host:   "example.com",
			port:   80,
			allow:  false,
		},
		{
			name: "explicit allow",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"example.com"},
				AllowedPorts: []int{80},
			},
			host:  "example.com",
			port:  80,
			allow: true,
		},
		{
			name: "wrong port",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"example.com"},
				AllowedPorts: []int{80},
			},
			host:  "example.com",
			port:  443,
			allow: false,
		},
		{
			name: "port 0 means any port",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"example.com"},
				AllowedPorts: []int{0},
			},
			host:  "example.com",
			port:  9999,
			allow: true,
		},
		{
			name: "deny overrides allow",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"*"},
				AllowedPorts: []int{80},
				DeniedHosts:  []string{"evil.com"},
			},
			host:  "evil.com",
			port:  80,
			allow: false,
		},
		{
			name: "wildcard allow all hosts",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"*"},
				AllowedPorts: []int{443},
			},
			host:  "anything.example.com",
			port:  443,
			allow: true,
		},
		{
			name: "subdomain wildcard",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"*.example.com"},
				AllowedPorts: []int{80},
			},
			host:  "api.example.com",
			port:  80,
			allow: true,
		},
		{
			name: "subdomain wildcard no match",
			policy: &NetworkPolicy{
				AllowedHosts: []string{"*.example.com"},
				AllowedPorts: []int{80},
			},
			host:  "evil.com",
			port:  80,
			allow: false,
		},
		{
			name: "empty allowlists deny all",
			policy: &NetworkPolicy{
				AllowedHosts: []string{},
				AllowedPorts: []int{80},
			},
			host:  "example.com",
			port:  80,
			allow: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.IsAllowed(tt.host, tt.port)
			if got != tt.allow {
				t.Errorf("IsAllowed(%q, %d) = %v, want %v", tt.host, tt.port, got, tt.allow)
			}
		})
	}
}

func TestFilesystemPolicy_CanRead(t *testing.T) {
	tests := []struct {
		name   string
		policy *FilesystemPolicy
		path   string
		allow  bool
	}{
		{
			name:   "nil policy denies all",
			policy: nil,
			path:   "/tmp/file.txt",
			allow:  false,
		},
		{
			name: "allowed read path",
			policy: &FilesystemPolicy{
				AllowedReadPaths: []string{"/tmp"},
			},
			path:  "/tmp/file.txt",
			allow: true,
		},
		{
			name: "path outside allowed",
			policy: &FilesystemPolicy{
				AllowedReadPaths: []string{"/tmp"},
			},
			path:  "/etc/passwd",
			allow: false,
		},
		{
			name: "subdirectory of allowed path",
			policy: &FilesystemPolicy{
				AllowedReadPaths: []string{"/data"},
			},
			path:  "/data/sub/dir/file.txt",
			allow: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.CanRead(tt.path)
			if got != tt.allow {
				t.Errorf("CanRead(%q) = %v, want %v", tt.path, got, tt.allow)
			}
		})
	}
}

func TestFilesystemPolicy_CanWrite(t *testing.T) {
	tests := []struct {
		name   string
		policy *FilesystemPolicy
		path   string
		allow  bool
	}{
		{
			name:   "nil policy denies",
			policy: nil,
			path:   "/tmp/out.txt",
			allow:  false,
		},
		{
			name: "allowed write path",
			policy: &FilesystemPolicy{
				AllowedWritePaths: []string{"/tmp/output"},
			},
			path:  "/tmp/output/result.txt",
			allow: true,
		},
		{
			name: "path outside write area",
			policy: &FilesystemPolicy{
				AllowedWritePaths: []string{"/tmp/output"},
			},
			path:  "/tmp/other.txt",
			allow: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.CanWrite(tt.path)
			if got != tt.allow {
				t.Errorf("CanWrite(%q) = %v, want %v", tt.path, got, tt.allow)
			}
		})
	}
}

func TestEnvPolicy_IsAllowed(t *testing.T) {
	tests := []struct {
		name   string
		policy *EnvPolicy
		env    string
		allow  bool
	}{
		{
			name:   "nil policy denies all",
			policy: nil,
			env:    "PATH",
			allow:  false,
		},
		{
			name: "explicit allow",
			policy: &EnvPolicy{
				Allowed: []string{"PATH", "HOME"},
			},
			env:   "PATH",
			allow: true,
		},
		{
			name: "not in allowlist",
			policy: &EnvPolicy{
				Allowed: []string{"PATH"},
			},
			env:   "SECRET_KEY",
			allow: false,
		},
		{
			name: "wildcard allow all",
			policy: &EnvPolicy{
				Allowed: []string{"*"},
			},
			env:   "anything",
			allow: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.IsAllowed(tt.env)
			if got != tt.allow {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.env, got, tt.allow)
			}
		})
	}
}

func TestBuildCapabilitySet(t *testing.T) {
	decl := CapabilityDecl{
		Network: &NetworkDecl{
			AllowHosts: []string{"api.example.com"},
			AllowPorts: []int{443},
			DenyHosts:  []string{"evil.com"},
		},
		Filesystem: &FilesystemDecl{
			Read:  []string{"/tmp/read"},
			Write: []string{"/tmp/write"},
		},
		Env: []string{"PATH", "HOME"},
	}
	cfg := RuntimeConfig{
		MemoryLimitMB: 128,
		TimeoutMS:     5000,
		MaxInstances:  2,
	}

	cs := BuildCapabilitySet(decl, cfg)
	if cs == nil {
		t.Fatal("expected non-nil capability set")
	}

	if cs.Network == nil {
		t.Fatal("expected network policy")
	}
	if !cs.Network.IsAllowed("api.example.com", 443) {
		t.Error("expected api.example.com:443 to be allowed")
	}
	if cs.Network.IsAllowed("evil.com", 443) {
		t.Error("expected evil.com to be denied")
	}

	if cs.FS == nil {
		t.Fatal("expected filesystem policy")
	}

	if cs.Env == nil {
		t.Fatal("expected env policy")
	}
	if !cs.Env.IsAllowed("PATH") {
		t.Error("expected PATH to be allowed")
	}

	if cs.MemoryMB != 128 {
		t.Errorf("expected MemoryMB 128, got %d", cs.MemoryMB)
	}
}

func TestBuildCapabilitySet_EmptyDecl(t *testing.T) {
	cs := BuildCapabilitySet(CapabilityDecl{}, RuntimeConfig{})
	if cs == nil {
		t.Fatal("expected non-nil capability set")
	}
	if cs.Network != nil {
		t.Error("expected nil network policy for empty decl")
	}
	if cs.FS != nil {
		t.Error("expected nil filesystem policy for empty decl")
	}
	if cs.Env != nil {
		t.Error("expected nil env policy for empty decl")
	}
}

func TestMatchHost(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		match   bool
	}{
		{"*", "anything", true},
		{"example.com", "example.com", true},
		{"example.com", "other.com", false},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "evil.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.host, func(t *testing.T) {
			got := matchHost(tt.pattern, tt.host)
			if got != tt.match {
				t.Errorf("matchHost(%q, %q) = %v, want %v", tt.pattern, tt.host, got, tt.match)
			}
		})
	}
}
