package sandbox

import (
	"testing"
)

func TestNetworkPolicy_BlacklistBlock(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, []string{"evil.com"})

	if err := np.CheckURL("https://evil.com/path"); err == nil {
		t.Fatal("expected blacklisted host to be blocked")
	}
	if err := np.CheckURL("https://safe.com/path"); err != nil {
		t.Fatalf("expected non-blacklisted host to be allowed, got: %v", err)
	}
}

func TestNetworkPolicy_DefaultBlacklist(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, nil)

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:8080/admin",
		"http://127.0.0.1:9090/",
	}
	for _, u := range blocked {
		if err := np.CheckURL(u); err == nil {
			t.Errorf("expected default-blacklisted URL to be blocked: %s", u)
		}
	}
}

func TestNetworkPolicy_WhitelistAllow(t *testing.T) {
	np := NewNetworkPolicy("whitelist", []string{"api.example.com"}, nil)

	if err := np.CheckURL("https://api.example.com/v1"); err != nil {
		t.Fatalf("expected whitelisted host to be allowed, got: %v", err)
	}
	if err := np.CheckURL("https://other.com/v1"); err == nil {
		t.Fatal("expected non-whitelisted host to be blocked")
	}
}

func TestNetworkPolicy_NoneMode(t *testing.T) {
	np := NewNetworkPolicy("none", nil, nil)

	urls := []string{
		"https://anything.com",
		"http://169.254.169.254/",
		"http://localhost/",
	}
	for _, u := range urls {
		if err := np.CheckURL(u); err != nil {
			t.Errorf("none mode should allow all URLs, got error for %s: %v", u, err)
		}
	}
}

func TestNetworkPolicy_InvalidURL(t *testing.T) {
	np := NewNetworkPolicy("blacklist", nil, nil)

	if err := np.CheckURL("not-a-url"); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
