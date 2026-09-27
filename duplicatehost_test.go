package cfdnsmanager

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	cf_caddyfile "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

// requireLoadErr adapts a Caddyfile, then provisions and validates it, expecting
// a load-time failure. Duplicate-host detection happens at host registration
// during provisioning, so it is not visible to the adapter alone.
func requireLoadErr(t *testing.T, input, contains string) {
	t.Helper()
	adapter := cf_caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapting Caddyfile: %v\ninput:\n%s", err, input)
	}
	var cfg caddy.Config
	if err := caddy.StrictUnmarshalJSON(out, &cfg); err != nil {
		t.Fatalf("unmarshalling config: %v\ninput:\n%s", err, input)
	}
	err = caddy.Validate(&cfg)
	if err == nil {
		t.Fatalf("expected load error containing %q, got none\ninput:\n%s", contains, input)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected load error containing %q, got: %v", contains, err)
	}
}

// Two directives declaring the same literal FQDN, as produced by copying a site
// block and forgetting to change its host.
func TestLoadRejectsDuplicateLiteralHost(t *testing.T) {
	requireLoadErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

alpha.example.com {
	cf_dns_manager {
		host foo.example.com
	}
	respond "alpha"
}

beta.example.com {
	cf_dns_manager {
		host foo.example.com
	}
	respond "beta"
}
`, `host "foo.example.com" is declared more than once`)
}

// A matcher reference and a literal that resolve to the same FQDN are the same
// host.
func TestLoadRejectsDuplicateMatcherAndLiteral(t *testing.T) {
	requireLoadErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

alpha.example.com {
	@foo host foo.example.com
	handle @foo {
		cf_dns_manager {
			host @foo
		}
		respond "alpha"
	}
}

beta.example.com {
	cf_dns_manager {
		host foo.example.com
	}
	respond "beta"
}
`, `host "foo.example.com" is declared more than once`)
}

// Equivalent spellings (mixed case, trailing dot) identify the same host.
func TestLoadRejectsDuplicateEquivalentSpellings(t *testing.T) {
	requireLoadErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

alpha.example.com {
	cf_dns_manager {
		host Foo.Example.com.
	}
	respond "alpha"
}

beta.example.com {
	cf_dns_manager {
		host foo.example.com
	}
	respond "beta"
}
`, `host "foo.example.com" is declared more than once`)
}

// The collision is on the hostname, not on how the host is served: an address
// host and a tunnel host naming the same FQDN still collide.
func TestLoadRejectsDuplicateAcrossHostModes(t *testing.T) {
	requireLoadErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel edge `+testTunnelUUID+`
	}
}

alpha.example.com {
	cf_dns_manager {
		host foo.example.com
		ip 203.0.113.10
	}
	respond "alpha"
}

beta.example.com {
	cf_dns_manager {
		host foo.example.com
		tunnel edge
	}
	respond "beta"
}
`, `host "foo.example.com" is declared more than once`)
}

// Control for the spec's Unique host declarations requirement: distinct hosts
// all load and register.
func TestLoadAcceptsDistinctHosts(t *testing.T) {
	app := provisionedApp(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

alpha.example.com {
	cf_dns_manager {
		host alpha.example.com
	}
	respond "alpha"
}

beta.example.com {
	cf_dns_manager {
		host beta.example.com
	}
	respond "beta"
}
`)

	hosts := app.hostsSnapshot()
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
	got := map[string]bool{}
	for _, h := range hosts {
		got[h.Host] = true
	}
	for _, want := range []string{"alpha.example.com", "beta.example.com"} {
		if !got[want] {
			t.Errorf("host %q was not registered; got %v", want, got)
		}
	}
}
