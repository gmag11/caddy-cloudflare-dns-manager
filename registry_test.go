package cfdnsmanager

import (
	"testing"
)

func TestRecordName(t *testing.T) {
	cases := []struct {
		host, zone, want string
	}{
		{"example.com", "example.com", "@"},
		{"foo.example.com", "example.com", "foo"},
		{"a.app.example.com", "app.example.com", "a"},
		{"a.app.example.com", "example.com", "a.app"},
		{"EXAMPLE.com", "example.COM", "@"},
		{"foo.example.com.", "example.com", "foo"},
	}
	for _, c := range cases {
		if got := recordName(c.host, c.zone); got != c.want {
			t.Errorf("recordName(%q, %q) = %q, want %q", c.host, c.zone, got, c.want)
		}
	}
}

func TestAssignZoneLongestSuffix(t *testing.T) {
	zones := []ZoneConfig{
		{Zone: "example.com", APIToken: "t1"},
		{Zone: "app.example.com", APIToken: "t2"},
	}

	// Deepest suffix wins.
	hc := HostConfig{Host: "a.app.example.com"}
	if err := assignZone(zones, &hc); err != nil {
		t.Fatalf("assignZone: %v", err)
	}
	if hc.ZoneConfig.Zone != "app.example.com" {
		t.Errorf("expected zone app.example.com, got %q", hc.ZoneConfig.Zone)
	}

	// Apex matches root zone.
	hc = HostConfig{Host: "example.com"}
	if err := assignZone(zones, &hc); err != nil {
		t.Fatalf("assignZone apex: %v", err)
	}
	if hc.ZoneConfig.Zone != "example.com" {
		t.Errorf("expected zone example.com, got %q", hc.ZoneConfig.Zone)
	}

	// Unrelated host is an error.
	hc = HostConfig{Host: "other.net"}
	if err := assignZone(zones, &hc); err == nil {
		t.Fatal("expected error for host outside declared zones")
	}
}

func TestFQDN(t *testing.T) {
	cases := []struct {
		name, zone, want string
	}{
		{"foo", "example.com", "foo.example.com"},
		{"@", "example.com", "example.com"},
		{"", "example.com", "example.com"},
		{"foo.example.com", "example.com", "foo.example.com"},
		{"example.com", "example.com", "example.com"},
		{"a.app", "example.com", "a.app.example.com"},
		{"FOO", "Example.COM", "foo.example.com"},
		{"foo.example.com.", "example.com", "foo.example.com"},
	}
	for _, c := range cases {
		if got := fqdn(c.name, c.zone); got != c.want {
			t.Errorf("fqdn(%q, %q) = %q, want %q", c.name, c.zone, got, c.want)
		}
	}
}

func TestCanonicalNameKey(t *testing.T) {
	cases := []struct {
		name, zone, want string
	}{
		{"foo", "example.com", "foo"},
		{"foo.example.com", "example.com", "foo"},
		{"@", "example.com", "@"},
		{"", "example.com", "@"},
		{"example.com", "example.com", "@"},
		{"a.app.example.com", "example.com", "a.app"},
		{"a.app", "example.com", "a.app"},
		{"FOO.example.com.", "example.com", "foo"},
	}
	for _, c := range cases {
		if got := canonicalNameKey(c.name, c.zone); got != c.want {
			t.Errorf("canonicalNameKey(%q, %q) = %q, want %q", c.name, c.zone, got, c.want)
		}
	}
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"100.64.10.5", // CGNAT / Tailscale
		"127.0.0.1",
		"169.254.1.1",
	}
	for _, ip := range privates {
		if !isPrivateIP(ip) {
			t.Errorf("expected %s to be private", ip)
		}
	}
	pub := []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"}
	for _, ip := range pub {
		if isPrivateIP(ip) {
			t.Errorf("expected %s to be public", ip)
		}
	}
}

func TestEffectiveProxied(t *testing.T) {
	pub := "8.8.8.8"
	priv := "100.64.10.5"

	// default (no proxied directive) + public => proxied
	if !effectiveProxied(HostConfig{}, pub) {
		t.Error("expected proxied default on public IP")
	}
	// explicit proxied no + public => dns-only
	no := false
	if effectiveProxied(HostConfig{Proxied: &no}, pub) {
		t.Error("expected dns-only when proxied no on public IP")
	}
	// private IP forces dns-only even with proxied yes
	yes := true
	if effectiveProxied(HostConfig{Proxied: &yes}, priv) {
		t.Error("expected dns-only forced on private IP")
	}
	// private IP + default => dns-only
	if effectiveProxied(HostConfig{}, priv) {
		t.Error("expected dns-only forced on private IP by default")
	}
}

func TestOwnershipTagAndMatch(t *testing.T) {
	app := &App{TagPrefix: "caddy-cf-dns", Instance: "host1"}
	tag := app.ownershipTag()
	if tag != "caddy-cf-dns:host1" {
		t.Fatalf("unexpected tag %q", tag)
	}
	if !isOwnedByInstance(tag, tag) {
		t.Error("exact tag should be owned")
	}
	if isOwnedByInstance("caddy-cf-dns:other", tag) {
		t.Error("different instance must not be owned")
	}
	if isOwnedByInstance("", tag) {
		t.Error("empty comment must not be owned")
	}
}
