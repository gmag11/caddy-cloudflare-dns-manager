package cfdnsmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	cf_caddyfile "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

// --- tunnel registry ---

func TestTunnelRegistryAdaptsAndResolves(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel edge ` + testTunnelUUID + `
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel edge
	}
	respond "ok"
}
`
	// Provisioning is required to see the resolved host: the handler carries it
	// through config JSON and registers it with the app at Provision time.
	app := provisionedApp(t, input)

	hosts := app.hostsSnapshot()
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1", len(hosts))
	}
	// The host must carry the resolved UUID, not the name: everything
	// downstream (CNAME target, plan grouping, API path) keys off the UUID.
	if hosts[0].TunnelID != testTunnelUUID {
		t.Errorf("TunnelID = %q, want the resolved UUID %q", hosts[0].TunnelID, testTunnelUUID)
	}
}

// provisionedApp adapts and provisions a config, returning the registered app.
// The adapter alone does not run Provision, so the host declarations are only
// observable after this.
func provisionedApp(t *testing.T, input string) *App {
	t.Helper()
	adapter := cf_caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(input), nil)
	if err != nil {
		t.Fatalf("adapting Caddyfile: %v\ninput:\n%s", err, input)
	}
	var cfg caddy.Config
	if err := caddy.StrictUnmarshalJSON(out, &cfg); err != nil {
		t.Fatalf("unmarshalling config: %v", err)
	}
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("provisioning config: %v", err)
	}
	if lastProvisionedApp == nil {
		t.Fatal("no app was provisioned")
	}
	return lastProvisionedApp
}

func TestAdaptRejectsDuplicateTunnelName(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel edge `+testTunnelUUID+`
		tunnel edge 8a7f3c2e-1234-4567-89ab-cdef01234567
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`, `tunnel "edge" is registered more than once`)
}

func TestAdaptRejectsMalformedRegistryUUID(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel edge not-a-uuid
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`, `"not-a-uuid" is not a UUID`)
}

func TestAdaptRejectsTunnelEntryMissingUUID(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel edge
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`, `tunnel requires a UUID after the name "edge"`)
}

// TestTunnelRegistryIsDeclarationOnly: registering a tunnel must not, by
// itself, make the plugin call the Tunnel API. Nothing declares a host for it.
func TestTunnelRegistryIsDeclarationOnly(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{{Service: "https://caddy:443"}},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// Registered (newIngressTestApp registers a and b) but no host declares it
	// and nothing was pruned.
	if err := runIngress(app, nil); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}
	if m.callCount("GET") != 0 {
		t.Errorf("got %d reads, want 0: the registry alone must not cause a call", m.callCount("GET"))
	}
	if m.putCount() != 0 {
		t.Errorf("got %d writes, want 0: the registry alone must not cause a write", m.putCount())
	}
}

func TestTunnelRegistryLookups(t *testing.T) {
	app := &App{Tunnels: []TunnelConfig{
		{Name: "edge", ID: testTunnelA},
		{Name: "lab", ID: testTunnelB},
	}}

	if id, ok := app.tunnelIDByName("edge"); !ok || id != testTunnelA {
		t.Errorf("tunnelIDByName(edge) = %q, %v", id, ok)
	}
	if _, ok := app.tunnelIDByName("nope"); ok {
		t.Error("an unregistered name must not resolve")
	}
	// Registration is checked by UUID at reconcile time, when the identifier in
	// hand comes from a deleted record rather than from a name.
	if !app.tunnelRegistered(testTunnelA) {
		t.Error("testTunnelA must be registered")
	}
	if app.tunnelRegistered("99999999-8888-7777-6666-555555555555") {
		t.Error("an unregistered UUID must not count as registered")
	}
	names := app.tunnelNames()
	if len(names) != 2 || names[0] != "edge" || names[1] != "lab" {
		t.Errorf("tunnelNames() = %v, want sorted [edge lab]", names)
	}
}

// --- CNAME target parsing: the only link from a deleted record to its tunnel ---

func TestTunnelIDFromCNAMETarget(t *testing.T) {
	cases := map[string]string{
		testTunnelA + ".cfargotunnel.com":                   testTunnelA,
		strings.ToUpper(testTunnelA) + ".cfargotunnel.com":  testTunnelA, // normalised
		testTunnelA + ".cfargotunnel.com.":                  testTunnelA, // trailing dot
		"  " + testTunnelA + ".cfargotunnel.com  ":          testTunnelA, // padding
		testTunnelB + ".cfargotunnel.com":                   testTunnelB,
		"somewhere.example.net":                             "", // not a tunnel
		"caddy.internal":                                    "",
		"":                                                  "",
		"cfargotunnel.com":                                  "", // no label
		"x." + testTunnelA + ".cfargotunnel.com":            "", // deeper name
		"not-a-uuid.cfargotunnel.com":                       "", // label is not a UUID
		"11111111222233334444555555555555.cfargotunnel.com": "", // UUID without dashes
		"cfargotunnel.com.evil.example.net":                 "", // suffix in the middle
		testTunnelA + ".cfargotunnel.com.evil.example.net":  "",
	}
	for in, want := range cases {
		if got := tunnelIDFromCNAMETarget(in); got != want {
			t.Errorf("tunnelIDFromCNAMETarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- prune-only write: the guard against the merge's catch-all bug ---

// TestPruneOnlyIngressPreservesEverythingElse pins the property the dedicated
// path exists for. mergeIngressPlan cannot express this: with an empty plan it
// drops every hostname-less rule and re-emits the catch-all from the plan, so
// it would write a configuration with no catch-all at all.
func TestPruneOnlyIngressPreservesEverythingElse(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "keep.example.com", Service: "https://keep:443", OriginRequest: json.RawMessage(`{"http2Origin":true}`)},
		{Hostname: "gone.example.com", Service: "http://gone:80"},
		{Hostname: "path.example.com", Service: "https://path:443", Path: json.RawMessage(`"^/api"`)},
		{Service: "http_status:404"},
	}
	pruned := map[string]prunedName{"gone.example.com": {Host: "gone.example.com", TunnelID: testTunnelA}}

	merged, changed, prunedOut := pruneOnlyIngress(current, pruned, nil)

	if !changed {
		t.Fatal("expected a change")
	}
	if len(prunedOut) != 1 || prunedOut[0] != "gone.example.com" {
		t.Errorf("pruned = %v, want [gone.example.com]", prunedOut)
	}
	// Exactly the read configuration minus the dead route, in order.
	if len(merged) != 3 {
		t.Fatalf("got %d rules, want 3: %#v", len(merged), merged)
	}
	if merged[0].Hostname != "keep.example.com" || string(merged[0].OriginRequest) != `{"http2Origin":true}` {
		t.Errorf("foreign rule mutated: %#v", merged[0])
	}
	if merged[1].Hostname != "path.example.com" || string(merged[1].Path) != `"^/api"` {
		t.Errorf("foreign path not preserved: %#v", merged[1])
	}
	// The catch-all must keep its own service, not be replaced by the config
	// default. That is the whole point of not calling the merge.
	if merged[2].Hostname != "" || merged[2].Service != "http_status:404" {
		t.Errorf("catch-all rewritten: %#v", merged[2])
	}
}

// TestPruneOnlyIngressNoOpWhenNothingMatches: a registered tunnel with no
// matching route must produce no write and no added rule.
func TestPruneOnlyIngressNoOpWhenNothingMatches(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "keep.example.com", Service: "https://keep:443"},
		{Service: "https_status:404"},
	}
	pruned := map[string]prunedName{"absent.example.com": {Host: "absent.example.com", TunnelID: testTunnelA}}

	merged, changed, prunedOut := pruneOnlyIngress(current, pruned, nil)

	if changed {
		t.Error("nothing matched, so nothing may change")
	}
	if len(prunedOut) != 0 {
		t.Errorf("pruned = %v, want none", prunedOut)
	}
	if len(merged) != 2 {
		t.Errorf("got %d rules, want the input unchanged", len(merged))
	}
}

// TestPruneOnlyIngressNeverTouchesCatchAll: the catch-all has no hostname, so
// it can never be a candidate however the pruned set is built.
func TestPruneOnlyIngressNeverTouchesCatchAll(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "gone.example.com", Service: "http://gone:80"},
		{Service: "https://caddy:443"},
	}
	// A pruned set keyed by the empty string is not reachable from a real run
	// (names come from DNS records), but the guard must hold structurally.
	pruned := map[string]prunedName{"": {Host: "", TunnelID: testTunnelA}}

	merged, changed, _ := pruneOnlyIngress(current, pruned, nil)

	if changed {
		t.Error("the catch-all must never be pruned")
	}
	if len(merged) != 2 {
		t.Errorf("got %d rules, want both kept", len(merged))
	}
}

// TestPruneOnlyIngressDeclaredHostSurvives pins the helper's behaviour on the
// input that mirrors the planned path's guarantee: a rule whose hostname is
// declared survives even when the name appears in the pruned set. In production
// the caller never reaches here for a tunnel with declared hosts (such a tunnel
// has a plan and takes the merge path), so this covers the helper's contract
// rather than a live path — which is the point of keeping the two paths'
// refusals identical.
func TestPruneOnlyIngressDeclaredHostSurvives(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "declared.example.com", Service: "https://declared:443"},
		{Hostname: "gone.example.com", Service: "http://gone:80"},
		{Service: "http_status:404"},
	}
	pruned := map[string]prunedName{
		"declared.example.com": {Host: "declared.example.com", TunnelID: testTunnelA},
		"gone.example.com":     {Host: "gone.example.com", TunnelID: testTunnelA},
	}
	declared := map[string]bool{"declared.example.com": true}

	merged, changed, prunedOut := pruneOnlyIngress(current, pruned, declared)

	if !changed {
		t.Fatal("the undeclared dead route must still be pruned")
	}
	if len(prunedOut) != 1 || prunedOut[0] != "gone.example.com" {
		t.Errorf("pruned = %v, want only gone.example.com", prunedOut)
	}
	for _, r := range merged {
		if r.Hostname == "gone.example.com" {
			t.Error("the dead, undeclared route must be gone")
		}
	}
	if len(merged) != 2 {
		t.Fatalf("got %d rules, want declared + catch-all: %#v", len(merged), merged)
	}
}

// TestPruneOnlyWriteIsIdempotent: a second run over an already-pruned tunnel
// must issue no write, exactly like the planned path.
func TestPruneOnlyWriteIsIdempotent(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "gone.example.com", Service: "http://gone:80"},
			{Service: "http_status:404"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	if err := runIngressPruning(app, nil, "gone.example.com"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if m.putCount() != 1 {
		t.Fatalf("first run: got %d writes, want 1", m.putCount())
	}
	// The route is gone now, so a second run over the same pruned name finds
	// nothing to delete.
	if err := runIngressPruning(app, nil, "gone.example.com"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if m.putCount() != 1 {
		t.Errorf("second run: got %d total writes, want still 1", m.putCount())
	}
}

// routedTunnelServer mounts the DNS mock plus one tunnel mock per tunnel id, so
// a test with two tunnels can tell their traffic apart. combinedServer serves a
// single tunnel mock for every path, which cannot distinguish them.
func routedTunnelServer(t *testing.T, dns *mockCloudflare, tunnels map[string]*mockTunnelAPI) string {
	t.Helper()
	parent := http.NewServeMux()
	parent.Handle("/zones", dns.handler())
	parent.Handle("/zones/", dns.handler())
	parent.HandleFunc("/accounts/", func(w http.ResponseWriter, r *http.Request) {
		for id, m := range tunnels {
			if strings.Contains(r.URL.Path, "/"+id+"/configurations") {
				m.handleAccount(w, r)
				return
			}
		}
		http.Error(w, "unhandled tunnel path "+r.URL.Path, http.StatusNotFound)
	})
	srv := httptest.NewServer(parent)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestIngressPlannedAndUnplannedTunnelsInOneRun: a tunnel with a plan keeps the
// normal path while a tunnel reached only by a deletion gets a prune-only
// write, one write each, and neither is visited twice.
func TestIngressPlannedAndUnplannedTunnelsInOneRun(t *testing.T) {
	dns := newMockCloudflare(t, "example.com", nil)
	dns.zonePrune = true
	// The deleted record names tunnel B, which no host declares.
	dns.records = append(dns.records, cfDNSRecord{
		Name: "old", Type: "CNAME", Content: testTunnelB + ".cfargotunnel.com",
		Proxied: true, Comment: "caddy-cf-dns:test-host",
	})
	tunnelA := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{{Service: "http_status:404"}},
	})
	tunnelB := newMockTunnelAPI(t, testTunnelB, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "old.example.com", Service: "http://old:80"},
			{Service: "http_status:404"},
		},
	})

	app, _ := testApp(t, dns, "203.0.113.10", true)
	app.AccountID = "acct-1"
	app.TunnelAPIToken = "token-1"
	app.TunnelDefaultService = "https://caddy:443"
	app.apiBase = routedTunnelServer(t, dns, map[string]*mockTunnelAPI{
		testTunnelA: tunnelA,
		testTunnelB: tunnelB,
	})

	// Tunnel A is declared by a host; tunnel B is only named by the deleted
	// orphan record above.
	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Tunnel B: the orphan CNAME and its route are gone through the prune-only
	// path, which never had a plan for it.
	if tunnelB.putCount() != 1 {
		t.Fatalf("unplanned tunnel: got %d writes, want 1; calls=%v", tunnelB.putCount(), tunnelB.calls)
	}
	rules := tunnelB.lastPut().Ingress
	if len(rules) != 1 || rules[0].Hostname != "" {
		t.Errorf("unplanned tunnel: want only the untouched catch-all, got %#v", rules)
	}
	if rules[0].Service != "http_status:404" {
		t.Errorf("unplanned tunnel: catch-all was rewritten to %q", rules[0].Service)
	}
}

// TestRevertWithNoTunnelDeclaredPrunesRoute is the end-to-end shape of the bug
// this change fixes: both tunnel hosts switch to address hosts in one reload, so
// no host declares the tunnel any more, yet each reverted host's route is
// deleted in the same run because the deleted CNAME named a registered tunnel.
func TestRevertWithNoTunnelDeclaredPrunesRoute(t *testing.T) {
	dns := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{Name: "one", Type: "CNAME", Content: testTunnelA + ".cfargotunnel.com", Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{Name: "two", Type: "CNAME", Content: testTunnelA + ".cfargotunnel.com", Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	dns.zonePrune = true

	tunnel := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "one.example.com", Service: "https://caddy:443"},
			{Hostname: "two.example.com", Service: "https://caddy:443"},
			{Service: "https://caddy:443"},
		},
	})

	app, _ := testApp(t, dns, "203.0.113.10", true)
	app.AccountID = "acct-1"
	app.TunnelAPIToken = "token-1"
	app.TunnelDefaultService = "https://caddy:443"
	app.apiBase = combinedServer(t, dns, tunnel)

	// No tunnel host at all: both are address hosts now, which is exactly the
	// state that used to leave the routes behind.
	hosts := []HostConfig{
		{Host: "one.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
		{Host: "two.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Both CNAMEs are gone (mandatory: a CNAME cannot coexist with A/AAAA).
	if rec := dns.recordByNameType("one", "CNAME"); rec != nil {
		t.Error("revert must delete the CNAME")
	}
	if rec := dns.recordByNameType("two", "CNAME"); rec != nil {
		t.Error("revert must delete the CNAME")
	}

	// And both routes went with them, in a single prune-only write.
	if tunnel.putCount() != 1 {
		t.Fatalf("got %d writes, want exactly 1", tunnel.putCount())
	}
	rules := tunnel.lastPut().Ingress
	if len(rules) != 1 || rules[0].Hostname != "" {
		t.Errorf("want only the untouched catch-all, got %#v", rules)
	}
	if rules[0].Service != "https://caddy:443" {
		t.Errorf("catch-all rewritten to %q, want the original", rules[0].Service)
	}
}

// TestIngressReportsUnattributablePrunedName: a pruned address record names no
// tunnel, so its route (if any) cannot be identified and nothing is written.
func TestIngressReportsUnattributablePrunedName(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{{Service: "https://caddy:443"}},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// An A record was pruned: no tunnel attributed.
	pruned := map[string][]prunedName{
		"example.com": {{Host: "addr.example.com", TunnelID: ""}},
	}
	if err := app.reconcileIngressPhase(context.Background(), nil, testAccounts(), pruned); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}
	if m.putCount() != 0 {
		t.Errorf("got %d writes, want 0", m.putCount())
	}
	if m.callCount("GET") != 0 {
		t.Errorf("got %d reads, want 0: no tunnel can be attributed", m.callCount("GET"))
	}
}
