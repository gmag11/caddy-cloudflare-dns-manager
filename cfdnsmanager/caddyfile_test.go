package cf_dns_manager

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
