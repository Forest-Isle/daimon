package wasm

import (
	"os"
	"path/filepath"
	"strings"
)

// CapabilitySet defines what a plugin instance is allowed to do.
type CapabilitySet struct {
	Network   *NetworkPolicy
	FS        *FilesystemPolicy
	Env       *EnvPolicy
	MemoryMB  int64
	TimeoutMS int64
}

// NetworkPolicy controls outbound network access.
type NetworkPolicy struct {
	AllowedHosts []string
	AllowedPorts []int
	DeniedHosts  []string
}

// IsAllowed checks if a (host, port) pair is permitted.
// Deny list takes priority, then allow list, default deny.
func (np *NetworkPolicy) IsAllowed(host string, port int) bool {
	if np == nil {
		return false
	}
	for _, denied := range np.DeniedHosts {
		if matchHost(denied, host) {
			return false
		}
	}
	for _, allowed := range np.AllowedHosts {
		if matchHost(allowed, host) {
			for _, p := range np.AllowedPorts {
				if p == port || p == 0 {
					return true
				}
			}
		}
	}
	return false
}

func matchHost(pattern, host string) bool {
	if pattern == "*" || pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix)
	}
	return false
}

// FilesystemPolicy controls file read/write access.
type FilesystemPolicy struct {
	AllowedReadPaths  []string
	AllowedWritePaths []string
}

// CanRead checks if a path is allowed for reading.
func (fp *FilesystemPolicy) CanRead(path string) bool {
	if fp == nil {
		return false
	}
	return fp.isUnder(path, fp.AllowedReadPaths)
}

// CanWrite checks if a path is allowed for writing.
func (fp *FilesystemPolicy) CanWrite(path string) bool {
	if fp == nil {
		return false
	}
	return fp.isUnder(path, fp.AllowedWritePaths)
}

func (fp *FilesystemPolicy) isUnder(target string, allowed []string) bool {
	abs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	clean := filepath.Clean(abs)
	for _, dir := range allowed {
		allowedAbs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		allowedClean := filepath.Clean(allowedAbs)
		if strings.HasPrefix(clean, allowedClean) {
			return true
		}
	}
	return false
}

// EnvPolicy controls environment variable access.
type EnvPolicy struct {
	Allowed []string
}

// IsAllowed checks if an env var name is in the allowlist.
func (ep *EnvPolicy) IsAllowed(name string) bool {
	if ep == nil {
		return false
	}
	for _, a := range ep.Allowed {
		if a == name || a == "*" {
			return true
		}
	}
	return false
}

// BuildCapabilitySet converts a CapabilityDecl into a CapabilitySet.
// Secure by default: an empty decl results in deny-all.
func BuildCapabilitySet(decl CapabilityDecl, cfg RuntimeConfig) *CapabilitySet {
	cs := &CapabilitySet{
		MemoryMB:  cfg.MemoryLimitMB,
		TimeoutMS: cfg.TimeoutMS,
	}

	if decl.Network != nil {
		cs.Network = &NetworkPolicy{
			AllowedHosts: append([]string{}, decl.Network.AllowHosts...),
			AllowedPorts: append([]int{}, decl.Network.AllowPorts...),
			DeniedHosts:  append([]string{}, decl.Network.DenyHosts...),
		}
	}

	if decl.Filesystem != nil {
		cs.FS = &FilesystemPolicy{
			AllowedReadPaths:  resolvePaths(decl.Filesystem.Read),
			AllowedWritePaths: resolvePaths(decl.Filesystem.Write),
		}
	}

	if len(decl.Env) > 0 {
		cs.Env = &EnvPolicy{Allowed: append([]string{}, decl.Env...)}
	}

	return cs
}

func resolvePaths(paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			resolved = append(resolved, abs)
		}
	}
	return resolved
}
