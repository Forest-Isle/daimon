package sandbox

import (
	"fmt"
	"net/url"
	"strings"
)

var defaultBlacklist = []string{
	"169.254.169.254",
	"metadata.google.internal",
	"127.0.0.1",
	"localhost",
	"0.0.0.0",
	"[::1]",
}

// NetworkPolicy validates URLs against whitelist/blacklist rules.
type NetworkPolicy struct {
	mode      string
	whitelist map[string]bool
	blacklist map[string]bool
}

// NewNetworkPolicy creates a network policy. User blacklist entries are appended to defaults.
func NewNetworkPolicy(mode string, whitelist, blacklist []string) *NetworkPolicy {
	np := &NetworkPolicy{
		mode:      mode,
		whitelist: make(map[string]bool),
		blacklist: make(map[string]bool),
	}
	for _, h := range defaultBlacklist {
		np.blacklist[strings.ToLower(h)] = true
	}
	for _, h := range blacklist {
		np.blacklist[strings.ToLower(h)] = true
	}
	for _, h := range whitelist {
		np.whitelist[strings.ToLower(h)] = true
	}
	return np
}

// CheckURL validates a URL against the policy.
func (p *NetworkPolicy) CheckURL(rawURL string) error {
	if p.mode == "none" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL: %s", rawURL)
	}
	host := strings.ToLower(u.Hostname())
	switch p.mode {
	case "blacklist":
		if p.blacklist[host] {
			return fmt.Errorf("blocked by network policy: host %s is blacklisted", host)
		}
		return nil
	case "whitelist":
		if p.whitelist[host] {
			return nil
		}
		return fmt.Errorf("blocked by network policy: host %s is not whitelisted", host)
	default:
		return nil
	}
}

func (p *NetworkPolicy) Mode() string { return p.mode }
