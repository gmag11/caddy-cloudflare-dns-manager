package cfdnsmanager

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAdaptGlobalAndPerSite(t *testing.T) {
	os.Setenv("CF_EXAMPLE", "testtoken")
	defer os.Unsetenv("CF_EXAMPLE")

	input := `
{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE}
	}
}

*.example.com {
	@foo host foo.example.com
	handle @foo {
		cf_dns_manager {
			host @foo
		}
		respond "foo"
	}
	@tail host tail.example.com
	handle @tail {
		cf_dns_manager {
			host @tail
			ip 100.64.10.5
			proxied no
			force_adopt
		}
		respond "tail"
	}
}
`
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)
}

func TestAdaptSiteBlockApex(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "apex"
}
`
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)
}

func TestAdaptUndeclaredZoneFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

other.com {
	cf_dns_manager {
		host other.com
	}
	respond "other"
}
`
	requireAdaptErr(t, input, "not under any declared cf_dns_manager zone")
}

func TestAdaptMissingGlobalBlockFails(t *testing.T) {
	input := `
example.com {
	cf_dns_manager {
		host example.com
	}
	respond "x"
}
`
	requireAdaptErr(t, input, "global options block is required")
}

func TestAdaptMatcherMultiHostFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	@multi host a.example.com b.example.com
	handle @multi {
		cf_dns_manager {
			host @multi
		}
	}
}
`
	requireAdaptErr(t, input, "must map to exactly one host")
}

// TestAdaptIPv6OverrideFails verifies the ip override stays IPv4-only.
func TestAdaptIPv6OverrideFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		ip 2001:db8::1
	}
	respond "x"
}
`
	requireAdaptErr(t, input, "ip must be an IPv4 address")
}

func TestNormalizeIP6(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"false", "", false},
		{"no", "", false},
		{"off", "", false},
		{"FALSE", "", false},
		{"auto", IP6Auto, false},
		{"public", IP6Auto, false},
		{"2001:db8::1", "2001:db8::1", false},
		{"fd7a:115c:a1e0::1", "fd7a:115c:a1e0::1", false},
		{"203.0.113.10", "", true},
		{"banana", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := normalizeIP6(c.in)
		if c.err {
			if err == nil {
				t.Errorf("normalizeIP6(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeIP6(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeIP6(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAdaptIP6Forms(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
		ip6_url https://v6.example.net/ip
	}
}

example.com {
	cf_dns_manager {
		host example.com
		ip6 auto
	}
	respond "x"
}
`
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)
	apps := cfg["apps"].(map[string]any)
	raw, _ := json.Marshal(apps["cf_dns_manager"])
	var app App
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatalf("decoding app: %v", err)
	}
	if app.IP6URL != "https://v6.example.net/ip" {
		t.Errorf("ip6_url = %q", app.IP6URL)
	}
}

func TestAdaptIP6Literal(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		ip6 fd7a:115c:a1e0::1
	}
	respond "x"
}
`
	// Adaptation succeeds; the host carries the literal in the app's host config
	// (injected via the no-op handler), so just assert the config is valid.
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)
}

func TestAdaptIP6InvalidFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		ip6 banana
	}
	respond "x"
}
`
	requireAdaptErr(t, input, "must be false, auto, or an IPv6 address")
}

// TestHostConfigJSONBackCompat ensures a frozen config without ip6 decodes with
// IPv6 disabled (empty), preserving pre-change behavior.
func TestHostConfigJSONBackCompat(t *testing.T) {
	var hc HostConfig
	if err := json.Unmarshal([]byte(`{"host":"foo.example.com","ip":"203.0.113.10"}`), &hc); err != nil {
		t.Fatalf("decoding old HostConfig: %v", err)
	}
	if hc.IP6 != "" {
		t.Errorf("ip6 = %q, want disabled (empty)", hc.IP6)
	}
}

// assertAppPresent verifies the adapted config contains the cf_dns_manager app.
func assertAppPresent(t *testing.T, cfg map[string]any) {
	t.Helper()
	apps, ok := cfg["apps"].(map[string]any)
	if !ok {
		t.Fatalf("config has no apps: %v", cfg)
	}
	raw, ok := apps["cf_dns_manager"]
	if !ok {
		t.Fatalf("config apps do not include cf_dns_manager: %v", apps)
	}
	// Ensure it marshals as an object with a zones entry.
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshaling app: %v", err)
	}
	var app App
	if err := json.Unmarshal(b, &app); err != nil {
		t.Fatalf("decoding app: %v", err)
	}
	if len(app.Zones) == 0 {
		t.Fatalf("app has no zones: %s", b)
	}
}
