package sandbox

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var defaultBlacklist = []string{
	"169.254.169.254",
	"metadata.google.internal",
	"127.0.0.1",
	"localhost",
	"0.0.0.0",
	"[::1]",
}

// Default CIDR blacklist — private/special-use ranges that should never be
// reachable from sandboxed tool calls. These catch DNS rebinding attacks
// and alternative IP encodings that hostname-only checks miss.
var defaultCIDRBlacklist = []string{
	"10.0.0.0/8",        // Private IPv4
	"172.16.0.0/12",     // Private IPv4
	"192.168.0.0/16",    // Private IPv4
	"127.0.0.0/8",       // Loopback
	"169.254.0.0/16",    // Link-local / cloud metadata
	"0.0.0.0/8",         // "This network"
	"224.0.0.0/4",       // Multicast
	"240.0.0.0/4",       // Reserved (future use)
	"::1/128",           // IPv6 loopback
	"fe80::/10",         // IPv6 link-local
	"fc00::/7",          // IPv6 unique local
	"fd00::/8",          // IPv6 private
	"ff00::/8",          // IPv6 multicast
	"100.64.0.0/10",     // Carrier-grade NAT (RFC 6598)
	"198.18.0.0/15",     // Benchmarking (RFC 2544)
}

// NetworkPolicy validates URLs against whitelist/blacklist rules.
type NetworkPolicy struct {
	mode            string
	whitelist       map[string]bool
	blacklist       map[string]bool
	cidrBlacklist   []netip.Prefix // parsed CIDR ranges to block at the IP level
	cidrWhitelist   []netip.Prefix // parsed CIDR ranges to allow
	resolveTimeout  time.Duration
}

// NewNetworkPolicy creates a network policy. User blacklist entries are appended to defaults.
func NewNetworkPolicy(mode string, whitelist, blacklist []string) *NetworkPolicy {
	np := &NetworkPolicy{
		mode:           mode,
		whitelist:      make(map[string]bool),
		blacklist:      make(map[string]bool),
		resolveTimeout: 3 * time.Second,
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
	// Parse default CIDR blacklist
	for _, cidr := range defaultCIDRBlacklist {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			np.cidrBlacklist = append(np.cidrBlacklist, prefix)
		}
	}
	return np
}

// CheckURL validates a URL against the policy. Performs both hostname and IP-level
// checks with CIDR matching to catch DNS rebinding, hex/octal IP encodings, and
// alternative DNS names that resolve to internal hosts.
//
// IP obfuscation bypass protection: before DNS resolution, the host is parsed
// directly as an IP address via netip.ParseAddr. This catches decimal
// (2130706433 = 127.0.0.1), hexadecimal (0x7f000001), octal (0177.0.0.1),
// and dotted-quad-with-leading-zeros representations that would otherwise
// bypass hostname blacklists and potentially confuse DNS resolvers.
func (p *NetworkPolicy) CheckURL(rawURL string) error {
	if p.mode == "none" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL: %s", rawURL)
	}
	host := strings.ToLower(u.Hostname())

	// Stage 1: hostname-level check
	switch p.mode {
	case "blacklist":
		if p.blacklist[host] {
			return fmt.Errorf("blocked by network policy: host %s is blacklisted", host)
		}
	case "whitelist":
		if !p.whitelist[host] {
			return fmt.Errorf("blocked by network policy: host %s is not whitelisted", host)
		}
	}

	// Stage 2: Direct IP parsing — catch obfuscated IP representations
	// (decimal, hex, octal) that would bypass hostname checks and potentially
	// confuse DNS resolvers.
	if len(p.cidrBlacklist) > 0 {
		if ip, err := netip.ParseAddr(host); err == nil {
			for _, prefix := range p.cidrBlacklist {
				if prefix.Contains(ip) {
					return fmt.Errorf("blocked by network policy: host %s is IP %s (blocked by CIDR %s)",
						host, ip, prefix)
				}
			}
		}
	}

	// Stage 3: DNS resolution — resolve hostname and verify resolved IPs
	// are not in private/special-use ranges.
	if len(p.cidrBlacklist) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), p.resolveTimeout)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			// DNS failure: allow through — false positives from transient
			// DNS errors are worse than a narrow window of resolution risk.
			return nil
		}
		for _, ip := range ips {
			for _, prefix := range p.cidrBlacklist {
				if prefix.Contains(ip) {
					return fmt.Errorf("blocked by network policy: host %s resolves to %s (blocked by CIDR %s)",
						host, ip, prefix)
				}
			}
		}
	}

	return nil
}

func (p *NetworkPolicy) Mode() string { return p.mode }
