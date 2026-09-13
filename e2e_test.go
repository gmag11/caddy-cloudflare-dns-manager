package cfdnsmanager

import (
	"os"
	"testing"

	"github.com/caddyserver/caddy/v2"
	cf_caddyfile "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

// TestEndToEndWiring adapts a real Caddyfile, provisions it with caddy.Validate
// (which runs each hostHandler.Provision and registers hosts into the app), then
// runs the app's Start against a mock Cloudflare to confirm a full reconcile.
func TestEndToEndWiring(t *testing.T) {
	os.Setenv("CF_EXAMPLE", "testtoken")
	defer os.Unsetenv("CF_EXAMPLE")

	m := newMockCloudflare(t, "example.com", nil)
	cfSrv := m.server(t)

	// Public-IP detection endpoint.
	detSrv := startDetectionServer(t, "203.0.113.50")

	input := `{
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
	adapter := cf_caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}

	var cfg caddy.Config
	if err := caddy.StrictUnmarshalJSON(out, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Provision (runs handler Provision, registering hosts) without binding
	// listeners or starting apps.
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}

	app := lastProvisionedApp
	if app == nil {
		t.Fatal("app was not provisioned")
	}
	hosts := app.hostsSnapshot()
	if len(hosts) != 2 {
		t.Fatalf("expected 2 registered hosts, got %d: %+v", len(hosts), hosts)
	}

	// Point the provisioned app at the mock Cloudflare and local detection,
	// then Start (reconcile).
	app.apiBase = cfSrv.URL
	app.IPURL = detSrv.URL
	tag := app.ownershipTag()
	if err := app.Start(); err != nil {
		t.Fatalf("start (reconcile): %v", err)
	}

	foo := m.recordByName("foo")
	if foo == nil {
		t.Fatal("expected foo A record created")
	}
	if foo.Content != "203.0.113.50" {
		t.Errorf("foo content = %q, want detected public ip", foo.Content)
	}
	if !foo.Proxied {
		t.Error("foo should be proxied by default")
	}
	if foo.Comment != tag {
		t.Errorf("foo comment = %q, want %q", foo.Comment, tag)
	}

	tail := m.recordByName("tail")
	if tail == nil {
		t.Fatal("expected tail A record created")
	}
	if tail.Content != "100.64.10.5" {
		t.Errorf("tail content = %q, want ip override", tail.Content)
	}
	if tail.Proxied {
		t.Error("tail (private ip) must be dns-only")
	}
}

// TestEndToEndIPv6Wiring adapts a Caddyfile with ip6 auto, provisions it, and
// confirms both A and AAAA records are reconciled, then that disabling IPv6
// with prune retires the AAAA.
func TestEndToEndIPv6Wiring(t *testing.T) {
	os.Setenv("CF_EXAMPLE", "testtoken")
	defer os.Unsetenv("CF_EXAMPLE")

	m := newMockCloudflare(t, "example.com", nil)
	m.zonePrune = true
	cfSrv := m.server(t)

	det4 := startDetectionServer(t, "203.0.113.50")
	det6 := startDetectionServer(t, "2001:db8::50")

	input := `{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE} prune
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
	adapter := cf_caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	var cfg caddy.Config
	if err := caddy.StrictUnmarshalJSON(out, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}

	app := lastProvisionedApp
	if app == nil {
		t.Fatal("app was not provisioned")
	}
	app.apiBase = cfSrv.URL
	app.IPURL = det4.URL
	app.IP6URL = det6.URL
	if err := app.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if rec := m.recordByNameType("@", "A"); rec == nil || rec.Content != "203.0.113.50" {
		t.Errorf("apex A wrong: %+v", rec)
	}
	if rec := m.recordByNameType("@", "AAAA"); rec == nil || rec.Content != "2001:db8::50" {
		t.Errorf("apex AAAA wrong: %+v", rec)
	}
}
