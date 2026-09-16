package cfdnsmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
)

const (
	testTunnelA = "8a7f3c2e-1234-4567-89ab-cdef01234567"
	testTunnelB = "1b2c3d4e-5678-49ab-9cde-f01234567890"
	// testTag is the ownership marker written into rule descriptions.
	testTag = "caddy-cf-dns:test-host"
)

// --- Group 3: plan derivation and merge -------------------------------------

func TestDeriveIngressPlanOneRulePerHost(t *testing.T) {
	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA},
		{Host: "ha.example.com", TunnelID: testTunnelA},
	}
	plans := deriveIngressPlan(hosts, "https://caddy:443")

	rules := plans[testTunnelA]
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 2 hosts + catch-all: %#v", len(rules), rules)
	}
	for i, want := range []string{"git.example.com", "ha.example.com"} {
		if rules[i].Hostname != want {
			t.Errorf("rule %d hostname = %q, want %q", i, rules[i].Hostname, want)
		}
		if rules[i].Service != "https://caddy:443" {
			t.Errorf("rule %d service = %q, want the default", i, rules[i].Service)
		}
	}
	last := rules[len(rules)-1]
	if last.Hostname != "" {
		t.Errorf("catch-all hostname = %q, want empty", last.Hostname)
	}
	if last.Service != "https://caddy:443" {
		t.Errorf("catch-all service = %q, want configured default", last.Service)
	}
}

func TestDeriveIngressPlanDeterministicOrder(t *testing.T) {
	hosts := []HostConfig{
		{Host: "zeta.example.com", TunnelID: testTunnelA},
		{Host: "alpha.example.com", TunnelID: testTunnelA},
		{Host: "mid.example.com", TunnelID: testTunnelA},
	}
	first := deriveIngressPlan(hosts, "")[testTunnelA]

	// Reversed input must yield the identical order: hosts accumulate from
	// concurrent handler Provision calls, so input order is not stable.
	reversed := []HostConfig{hosts[2], hosts[1], hosts[0]}
	second := deriveIngressPlan(reversed, "")[testTunnelA]

	if len(first) != len(second) {
		t.Fatalf("rule counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Hostname != second[i].Hostname || first[i].Service != second[i].Service {
			t.Fatalf("order not deterministic at %d: %#v vs %#v", i, first[i], second[i])
		}
	}
	if first[0].Hostname != "alpha.example.com" {
		t.Errorf("first rule = %q, want alpha.example.com", first[0].Hostname)
	}
}

func TestDeriveIngressPlanPerTunnelIndependence(t *testing.T) {
	hosts := []HostConfig{
		{Host: "a.example.com", TunnelID: testTunnelA},
		{Host: "b.example.com", TunnelID: testTunnelB},
	}
	plans := deriveIngressPlan(hosts, "")

	for _, tc := range []struct{ tunnel, want string }{
		{testTunnelA, "a.example.com"},
		{testTunnelB, "b.example.com"},
	} {
		rules := plans[tc.tunnel]
		if len(rules) != 2 {
			t.Fatalf("tunnel %s: got %d rules, want 1 host + catch-all", tc.tunnel, len(rules))
		}
		if rules[0].Hostname != tc.want {
			t.Errorf("tunnel %s: hostname = %q, want %q", tc.tunnel, rules[0].Hostname, tc.want)
		}
	}
}

func TestDeriveIngressPlanPerHostOverride(t *testing.T) {
	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA, TunnelService: "ssh://caddy:22"},
		{Host: "ha.example.com", TunnelID: testTunnelA},
	}
	rules := deriveIngressPlan(hosts, "https://caddy:443")[testTunnelA]

	byHost := map[string]string{}
	for _, r := range rules {
		byHost[r.Hostname] = r.Service
	}
	if byHost["git.example.com"] != "ssh://caddy:22" {
		t.Errorf("override not applied: %q", byHost["git.example.com"])
	}
	if byHost["ha.example.com"] != "https://caddy:443" {
		t.Errorf("default not applied to non-overridden host: %q", byHost["ha.example.com"])
	}
	if byHost[""] != "https://caddy:443" {
		t.Errorf("catch-all = %q, want the global default", byHost[""])
	}
}

func TestDeriveIngressPlanSkipsNonTunnelHosts(t *testing.T) {
	hosts := []HostConfig{
		{Host: "plain.example.com"},
		{Host: "git.example.com", TunnelID: testTunnelA},
	}
	plans := deriveIngressPlan(hosts, "")
	if len(plans) != 1 {
		t.Fatalf("got %d tunnel plans, want 1", len(plans))
	}
	if len(plans[testTunnelA]) != 2 {
		t.Errorf("got %d rules, want 1 host + catch-all", len(plans[testTunnelA]))
	}
}

func TestDeriveIngressPlanFailClosedDefault(t *testing.T) {
	rules := deriveIngressPlan([]HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA},
	}, "")[testTunnelA]

	if rules[len(rules)-1].Service != defaultTunnelService {
		t.Errorf("catch-all = %q, want fail-closed %q", rules[len(rules)-1].Service, defaultTunnelService)
	}
}

// TestDeriveIngressPlanDoesNotAuthorDescription: the plugin no longer writes an
// ownership marker into rule descriptions. The field is undocumented in the API
// model and the dashboard clears it when a rule is edited, so relying on it
// would be fragile; route cleanup keys off the DNS record instead.
func TestDeriveIngressPlanDoesNotAuthorDescription(t *testing.T) {
	rules := deriveIngressPlan([]HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA},
		{Host: "ha.example.com", TunnelID: testTunnelA},
	}, "")[testTunnelA]

	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}
	for i, r := range rules {
		if r.Description != "" {
			t.Errorf("rule %d description = %q, want it left unauthored", i, r.Description)
		}
	}
}

// TestDeriveIngressPlanPreservesOperatorDescription checks the other direction:
// a description the plugin finds on a declared rule is carried through, not
// cleared.
func TestDeriveIngressPlanPreservesOperatorDescription(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "git.example.com", Service: "http://stale:80", Description: "set by hand"},
		{Service: "http://stale:80"},
	}
	plan := deriveIngressPlan([]HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA},
	}, "https://caddy:443")[testTunnelA]

	merged, _, _, _ := mergeIngressPlan(current, plan, nil)

	var host, catchAll string
	for _, r := range merged {
		if r.Hostname == "git.example.com" {
			host = r.Description
		} else {
			catchAll = r.Description
		}
	}
	if host != "set by hand" {
		t.Errorf("declared rule description = %q, want it preserved", host)
	}
	if catchAll != "" {
		t.Errorf("catch-all description = %q, want it left unauthored", catchAll)
	}
}

func TestDeriveIngressPlanEmptyConfig(t *testing.T) {
	if plans := deriveIngressPlan(nil, ""); len(plans) != 0 {
		t.Fatalf("got %d plans for no hosts, want 0", len(plans))
	}
}

func TestMergeIngressPlanPreservesForeignRules(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "other.example.com", Service: "http://other:8080"},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}
	merged, changed, shadowed, _ := mergeIngressPlan(current, plan, nil)

	if !changed {
		t.Error("expected change reported")
	}
	if len(merged) != 3 {
		t.Fatalf("got %d rules, want foreign + host + catch-all: %#v", len(merged), merged)
	}
	if merged[0].Hostname != "other.example.com" || merged[0].Service != "http://other:8080" {
		t.Errorf("foreign rule not preserved verbatim: %#v", merged[0])
	}
	if merged[1].Hostname != "git.example.com" {
		t.Errorf("declared rule not appended after foreign: %#v", merged[1])
	}
	if merged[2].Hostname != "" || merged[2].Service != "https://caddy:443" {
		t.Errorf("catch-all not last: %#v", merged[2])
	}
	if len(shadowed) != 0 {
		t.Errorf("unexpected shadowing: %v", shadowed)
	}
}

func TestMergeIngressPlanReplacesDeclaredHost(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "git.example.com", Service: "http://stale:80"},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}
	merged, changed, _, _ := mergeIngressPlan(current, plan, nil)

	if !changed {
		t.Error("expected change reported when a declared host's service is stale")
	}
	if len(merged) != 2 {
		t.Fatalf("got %d rules, want host + catch-all: %#v", len(merged), merged)
	}
	if merged[0].Service != "https://caddy:443" {
		t.Errorf("stale service not replaced: %#v", merged[0])
	}
}

func TestMergeIngressPlanDoesNotDuplicateCatchAll(t *testing.T) {
	current := []cfIngressRule{
		{Service: "http_status:404"},
		{Service: "https://caddy:443"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}
	merged, _, _, _ := mergeIngressPlan(current, plan, nil)

	catchAlls := 0
	for _, r := range merged {
		if r.Hostname == "" {
			catchAlls++
		}
	}
	if catchAlls != 1 {
		t.Errorf("got %d catch-all rules, want exactly 1: %#v", catchAlls, merged)
	}
	if last := merged[len(merged)-1]; last.Hostname != "" {
		t.Errorf("catch-all is not last: %#v", merged)
	}
}

func TestMergeIngressPlanDetectsShadowingWildcard(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "*.example.com", Service: "http://legacy:80"},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "http_status:404"},
	}
	_, _, shadowed, _ := mergeIngressPlan(current, plan, nil)

	if len(shadowed) != 1 {
		t.Fatalf("got %d shadowing reports, want 1: %v", len(shadowed), shadowed)
	}
	if !strings.Contains(shadowed[0], "*.example.com") || !strings.Contains(shadowed[0], "git.example.com") {
		t.Errorf("shadowing report %q must name both the rule and the host", shadowed[0])
	}
}

func TestWildcardMatchesAnyDepth(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"*.example.com", "git.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "notexample.com", false},
		{"git.example.com", "git.example.com", false}, // not a wildcard pattern
	}
	for _, tc := range cases {
		if got := wildcardMatches(tc.pattern, tc.host); got != tc.want {
			t.Errorf("wildcardMatches(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
}

func TestIngressRulesEqualIgnoresUnmanagedFields(t *testing.T) {
	a := []cfIngressRule{{Hostname: "git.example.com", Service: "https://caddy:443"}}
	b := []cfIngressRule{{
		Hostname:      "git.example.com",
		Service:       "https://caddy:443",
		OriginRequest: json.RawMessage(`{}`), // server-side normalisation
	}}
	if !ingressRulesEqual(a, b) {
		t.Error("normalised originRequest must not count as drift")
	}

	c := []cfIngressRule{{Hostname: "git.example.com", Service: "http://other:80"}}
	if ingressRulesEqual(a, c) {
		t.Error("a different service must count as drift")
	}
}

// --- Group 5: reconciliation against the mock Tunnel API ---------------------

// newIngressTestApp builds an App wired to the tunnel mock for ingress tests.
// The account id is derived from the zone lookup in production, so tests pass
// it explicitly through accountByZone.
func newIngressTestApp(t *testing.T, m *mockTunnelAPI, defaultService string) *App {
	t.Helper()
	return &App{
		TunnelAPIToken:       "token-1",
		TunnelDefaultService: defaultService,
		TagPrefix:            "caddy-cf-dns",
		Instance:             "test-host",
		logger:               zap.NewNop(),
		apiBase:              m.server(t),
	}
}

// testAccounts is the account-by-zone map a successful DNS phase produces.
func testAccounts() map[string]string {
	return map[string]string{"example.com": testAccountID}
}

const testAccountID = "7886ae726a21ed3ed8f586b7e88dd409"

// runIngress drives the ingress phase the way Reconcile does, with the account
// id already discovered from the zone lookup.
func runIngress(app *App, hosts []HostConfig) error {
	return app.reconcileIngressPhase(context.Background(), hosts, testAccounts(), nil)
}

func TestIngressCreatesRulesWhenNoneExist(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA},
		{Host: "ha.example.com", TunnelID: testTunnelA},
	}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 1 {
		t.Fatalf("got %d writes, want 1", m.putCount())
	}
	rules := m.storedConfig().Ingress
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 2 hosts + catch-all: %#v", len(rules), rules)
	}
	if rules[len(rules)-1].Hostname != "" {
		t.Errorf("last rule must be the catch-all: %#v", rules[len(rules)-1])
	}
}

func TestIngressUpdatesOnDrift(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "git.example.com", Service: "http://stale:80"},
			{Service: "http_status:404"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 1 {
		t.Fatalf("got %d writes, want 1", m.putCount())
	}
	got := m.lastPut().Ingress
	if len(got) != 2 || got[0].Service != "https://caddy:443" {
		t.Errorf("stale service not corrected: %#v", got)
	}
}

func TestIngressIdempotentUnderServerNormalisation(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "git.example.com", Service: "https://caddy:443", Description: testTag},
			{Service: "https://caddy:443", Description: testTag},
		},
	})
	m.setNormalize(true)
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 0 {
		t.Fatalf("got %d writes, want 0: server normalisation must not look like drift", m.putCount())
	}
}

func TestIngressPreservesForeignRules(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "other.example.com", Service: "http://other:8080"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	got := m.lastPut().Ingress
	if len(got) != 3 {
		t.Fatalf("got %d rules, want foreign + host + catch-all: %#v", len(got), got)
	}
	if got[0].Hostname != "other.example.com" || got[0].Service != "http://other:8080" {
		t.Errorf("foreign rule not preserved: %#v", got[0])
	}
}

func TestIngressPreservesWarpRoutingAndOriginRequest(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	m.setIngress(nil)
	m.mu.Lock()
	m.config.OriginRequest = json.RawMessage(`{"connectTimeout":30}`)
	m.config.WarpRouting = json.RawMessage(`{"enabled":true}`)
	m.mu.Unlock()

	app := newIngressTestApp(t, m, "https://caddy:443")
	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	written := m.lastPut()
	if string(written.WarpRouting) != `{"enabled":true}` {
		t.Errorf("warp-routing not round-tripped: %s", written.WarpRouting)
	}
	if string(written.OriginRequest) != `{"connectTimeout":30}` {
		t.Errorf("top-level originRequest not round-tripped: %s", written.OriginRequest)
	}
}

func TestIngressNeverDeletesRules(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "removed.example.com", Service: "http://gone:80"},
			{Service: "http_status:404"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// The config declares no host for the rule above: it was removed.
	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.hasCall(http.MethodDelete, "") || m.callCount(http.MethodDelete) != 0 {
		t.Error("plugin must never issue a DELETE against ingress rules")
	}
	// The undeclared rule survives, proving no implicit deletion either.
	for _, r := range m.storedConfig().Ingress {
		if r.Hostname == "removed.example.com" {
			return
		}
	}
	t.Error("undeclared rule was dropped; it must be preserved")
}

func TestIngressRefusesLocallyManagedTunnel(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	m.setSource("local")
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	err := runIngress(app, hosts)

	if err == nil {
		t.Fatal("expected an error for a locally-managed tunnel")
	}
	if !strings.Contains(err.Error(), "locally managed") {
		t.Errorf("error must name the cause: %v", err)
	}
	if m.putCount() != 0 {
		t.Errorf("got %d writes, want 0 for a locally-managed tunnel", m.putCount())
	}
}

func TestIngressFailureDoesNotBlockDNS(t *testing.T) {
	// A zone with hosts to create, plus a failing Tunnel API.
	m := newMockCloudflare(t, "example.com", nil)
	ta := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	ta.setFailPut(true)

	app, _ := testApp(t, m, "203.0.113.10", true)
	app.AccountID = "acct-1"
	app.TunnelAPIToken = "token-1"
	app.TunnelDefaultService = "https://caddy:443"
	app.apiBase = combinedServer(t, m, ta)

	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
		{Host: "git.example.com", TunnelID: testTunnelA, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	err := app.Reconcile(hosts)

	if err == nil {
		t.Fatal("expected the ingress failure to be surfaced")
	}
	if rec := m.recordByName("foo"); rec == nil {
		t.Error("DNS reconciliation must still run when the Tunnel API fails")
	}
	if rec := m.recordByNameType("git", "CNAME"); rec == nil {
		t.Error("tunnel host CNAME must still be reconciled when the Tunnel API fails")
	}
}

func TestIngressSkippedWithoutTunnelToken(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	ta := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})

	app, _ := testApp(t, m, "203.0.113.10", true)
	// No TunnelAPIToken: the Tunnel API must not be called at all, even though
	// the DNS phase still resolves the account id.
	app.apiBase = combinedServer(t, m, ta)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if ta.callCount(http.MethodGet) != 0 || ta.putCount() != 0 {
		t.Error("no Tunnel API call may be made without the tunnel token")
	}
	if rec := m.recordByNameType("git", "CNAME"); rec == nil {
		t.Error("DNS must still be reconciled without the tunnel token")
	}
}

// TestIngressDerivesAccountFromZone covers the point of the token-only
// `account` option: the account id comes from the zone lookup the plugin
// already performs, so the user never configures it. Cloudflare guarantees a
// tunnel and its zone share an account.
func TestIngressDerivesAccountFromZone(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	ta := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})

	app, _ := testApp(t, m, "203.0.113.10", true)
	app.TunnelAPIToken = "tunnel-token"
	app.TunnelDefaultService = "https://caddy:443"
	app.apiBase = combinedServer(t, m, ta)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelA, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The zone mock's account must appear in the Tunnel API path, proving the
	// id was derived rather than configured.
	want := "/accounts/" + m.accountID + "/cfd_tunnel/" + testTunnelA + "/configurations"
	if !ta.hasCall(http.MethodGet, want) {
		t.Errorf("expected a GET to %s; recorded calls: %v", want, ta.calls)
	}
	if ta.putCount() != 1 {
		t.Errorf("got %d writes, want 1", ta.putCount())
	}
}

// TestIngressExplicitAccountIDOverridesDerivation covers the escape hatch.
func TestIngressExplicitAccountIDOverridesDerivation(t *testing.T) {
	ta := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	app := newIngressTestApp(t, ta, "https://caddy:443")
	app.AccountID = "explicit-account"

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if !ta.hasCall(http.MethodGet, "/accounts/explicit-account/cfd_tunnel/"+testTunnelA+"/configurations") {
		t.Errorf("explicit account id was not used; recorded calls: %v", ta.calls)
	}
}

// TestIngressSkipsWhenNoZoneResolved covers a failed DNS phase: without a
// resolved account the phase must skip rather than guess.
func TestIngressSkipsWhenNoZoneResolved(t *testing.T) {
	ta := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	app := newIngressTestApp(t, ta, "")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := app.reconcileIngressPhase(context.Background(), hosts, map[string]string{}, nil); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ta.callCount(http.MethodGet) != 0 || ta.putCount() != 0 {
		t.Error("no Tunnel API call may be made when the account id is unknown")
	}
}

// TestResolveAccountIDPrefersExplicit is a focused unit test of the precedence
// rule, including the deterministic tie-break across zones.
func TestResolveAccountIDPrefersExplicit(t *testing.T) {
	app := &App{}

	if got := app.resolveAccountID(map[string]string{}); got != "" {
		t.Errorf("empty derivation = %q, want empty", got)
	}
	if got := app.resolveAccountID(map[string]string{"z.com": "acct-z"}); got != "acct-z" {
		t.Errorf("single zone = %q, want acct-z", got)
	}

	// Several zones: the smallest zone name wins, so the result is stable.
	multi := map[string]string{"b.com": "acct-b", "a.com": "acct-a"}
	if got := app.resolveAccountID(multi); got != "acct-a" {
		t.Errorf("multi-zone derivation = %q, want the deterministic acct-a", got)
	}

	app.AccountID = "acct-explicit"
	if got := app.resolveAccountID(multi); got != "acct-explicit" {
		t.Errorf("explicit account = %q, want it to win over derivation", got)
	}
}

func TestIngressWarnsOnShadowingWildcard(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "*.example.com", Service: "http://legacy:80"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	// The wildcard is preserved (never deleted) and the declared host is added.
	got := m.storedConfig().Ingress
	var sawWildcard, sawHost bool
	for _, r := range got {
		if r.Hostname == "*.example.com" {
			sawWildcard = true
		}
		if r.Hostname == "git.example.com" {
			sawHost = true
		}
	}
	if !sawWildcard {
		t.Error("foreign wildcard rule must be preserved")
	}
	if !sawHost {
		t.Error("declared host rule must be written")
	}
}

func TestIngressPreservesForeignRuleDescription(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "other.example.com", Service: "http://other:8080", Description: "managed by hand"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	for _, r := range m.lastPut().Ingress {
		if r.Hostname == "other.example.com" {
			if r.Description != "managed by hand" {
				t.Errorf("foreign rule description = %q, want it preserved", r.Description)
			}
			return
		}
	}
	t.Fatal("foreign rule missing from the written configuration")
}

// TestIngressKeepsOperatorDescriptionOnRewrittenRule: the plugin does not
// author descriptions, so correcting a rule's service must carry the
// operator's description through rather than clearing it.
func TestIngressKeepsOperatorDescriptionOnRewrittenRule(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "git.example.com", Service: "http://stale:80", Description: "set in dashboard"},
			{Service: "http_status:404", Description: "catch-all note"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}
	if m.putCount() != 1 {
		t.Fatalf("got %d writes, want 1 (the stale service must be corrected)", m.putCount())
	}

	for _, r := range m.lastPut().Ingress {
		if r.Hostname == "git.example.com" {
			if r.Service != "https://caddy:443" {
				t.Errorf("service = %q, want it corrected", r.Service)
			}
			if r.Description != "set in dashboard" {
				t.Errorf("description = %q, want it preserved", r.Description)
			}
		}
	}
}

// TestIngressNoWriteWhenOnlyDescriptionDiffers: since the plugin does not
// manage the field, a description appearing or changing is not drift.
func TestIngressNoWriteWhenOnlyDescriptionDiffers(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "git.example.com", Service: "https://caddy:443", Description: "added later"},
			{Service: "https://caddy:443"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 0 {
		t.Errorf("got %d writes, want 0: description is not a managed field", m.putCount())
	}
}

// TestIngressForeignRuleKeepsDescription: an undeclared rule is written back
// verbatim, description included.
func TestIngressForeignRuleKeepsDescription(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "other.example.com", Service: "http://other:8080", Description: "someone else's note"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	for _, r := range m.lastPut().Ingress {
		if r.Hostname == "other.example.com" {
			if r.Description != "someone else's note" {
				t.Errorf("foreign description = %q, want it untouched", r.Description)
			}
			return
		}
	}
	t.Fatal("foreign rule missing from the written configuration")
}

// --- route prune, keyed off the DNS record ----------------------------------

// runIngressPruning drives the phase the way Reconcile does when the DNS pass
// pruned the given names.
func runIngressPruning(app *App, hosts []HostConfig, prunedNames ...string) error {
	pruned := map[string][]string{}
	if len(prunedNames) > 0 {
		pruned["example.com"] = prunedNames
	}
	return app.reconcileIngressPhase(context.Background(), hosts, testAccounts(), pruned)
}

// TestIngressPrunesRouteWhoseDNSWasPruned is the core case: the DNS record the
// route depended on was deleted in this run, so the route can no longer serve
// traffic and is deleted in the same reconcile.
func TestIngressPrunesRouteWhoseDNSWasPruned(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "gone.example.com", Service: "https://caddy:443"},
			{Hostname: "keep.example.com", Service: "http://keep:80"},
			{Service: "https://caddy:443"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// No tunnel host is declared anymore; only the DNS prune tells us the name
	// is dead. The declared set is what the config still asks for.
	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngressPruning(app, hosts, "gone.example.com"); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 1 {
		t.Fatalf("got %d writes, want 1", m.putCount())
	}
	var sawGone, sawKeep bool
	for _, r := range m.lastPut().Ingress {
		switch r.Hostname {
		case "gone.example.com":
			sawGone = true
		case "keep.example.com":
			sawKeep = true
		}
	}
	if sawGone {
		t.Error("route whose DNS was pruned must be deleted")
	}
	if !sawKeep {
		t.Error("unrelated foreign route must be preserved")
	}
}

// TestIngressKeepsRouteWhenNothingWasPruned: no DNS deletion in this run means
// no route deletion, however orphaned the route looks.
func TestIngressKeepsRouteWhenNothingWasPruned(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "undeclared.example.com", Service: "http://other:80"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngressPruning(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	for _, r := range m.lastPut().Ingress {
		if r.Hostname == "undeclared.example.com" {
			return
		}
	}
	t.Error("an undeclared route must be preserved when no DNS record was pruned")
}

// TestIngressDoesNotPruneDeclaredHost: a declared host is never a prune
// candidate even if its DNS record disappeared; its route is still wanted.
func TestIngressDoesNotPruneDeclaredHost(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "git.example.com", Service: "https://caddy:443"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	// The DNS phase reports it pruned git's record (e.g. reverted to a
	// different record type). The declared rule must survive regardless.
	if err := runIngressPruning(app, hosts, "git.example.com"); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	for _, r := range m.lastPut().Ingress {
		if r.Hostname == "git.example.com" {
			return
		}
	}
	t.Error("a declared host's route must never be pruned")
}

// TestIngressPruneNeverRemovesCatchAll guards the one rule the API requires.
func TestIngressPruneNeverRemovesCatchAll(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "gone.example.com", Service: "http://gone:80"},
			{Service: "http://stale-catch-all:80"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngressPruning(app, hosts, "gone.example.com"); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	rules := m.lastPut().Ingress
	catchAlls := 0
	for _, r := range rules {
		if r.Hostname == "" {
			catchAlls++
		}
	}
	if catchAlls != 1 {
		t.Errorf("got %d catch-all rules, want exactly 1: %#v", catchAlls, rules)
	}
	if last := rules[len(rules)-1]; last.Hostname != "" {
		t.Errorf("catch-all is not last: %#v", rules)
	}
}

// TestIngressCannotPruneWhenNoTunnelIsDeclared documents a deliberate limit:
// once the config declares no tunnel, the plugin cannot tell which tunnel an
// orphaned route belongs to, and enumerating every tunnel in the account to
// find out would mean rewriting tunnels the operator never declared to it. So
// it reports instead of guessing.
func TestIngressCannotPruneWhenNoTunnelIsDeclared(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "a.example.com", Service: "http://a:80"},
			{Service: "https://caddy:443"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// No tunnel hosts declared at all; the DNS pass still pruned a name.
	if err := runIngressPruning(app, nil, "a.example.com"); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	if m.putCount() != 0 {
		t.Errorf("got %d writes, want 0: the owning tunnel is unknown", m.putCount())
	}
	if m.callCount(http.MethodGet) != 0 {
		t.Errorf("got %d reads, want 0: no tunnel should be contacted", m.callCount(http.MethodGet))
	}
}

// TestIngressPrunesAllDeclaredRoutesLeavingCatchAll: when the tunnel is still
// declared but every one of its routes is dead, the write leaves only the
// catch-all.
func TestIngressPrunesAllDeclaredRoutesLeavingCatchAll(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "a.example.com", Service: "http://a:80"},
			{Hostname: "b.example.com", Service: "http://b:80"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")

	// Another host keeps the tunnel declared, while a and b are gone.
	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngressPruning(app, hosts, "a.example.com", "b.example.com"); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	rules := m.lastPut().Ingress
	// The declared host plus the catch-all survive; the two dead ones do not.
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want git + catch-all: %#v", len(rules), rules)
	}
	for _, r := range rules {
		if r.Hostname == "a.example.com" || r.Hostname == "b.example.com" {
			t.Errorf("dead route %q survived: %#v", r.Hostname, rules)
		}
	}
	if rules[len(rules)-1].Hostname != "" {
		t.Errorf("catch-all is not last: %#v", rules)
	}
}

// TestIngressSecondRunAfterPruneIsNoOp: once the orphan is gone, the next
// reconcile writes nothing.
func TestIngressSecondRunAfterPruneIsNoOp(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{
		Ingress: []cfIngressRule{
			{Hostname: "gone.example.com", Service: "http://gone:80"},
			{Service: "https://caddy:443"},
		},
	})
	app := newIngressTestApp(t, m, "https://caddy:443")
	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}

	if err := runIngressPruning(app, hosts, "gone.example.com"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if m.putCount() != 1 {
		t.Fatalf("first run: got %d writes, want 1", m.putCount())
	}

	// The pruned set is reported again (the DNS record is not coming back), but
	// the rule it referred to is already gone.
	if err := runIngressPruning(app, hosts, "gone.example.com"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if m.putCount() != 1 {
		t.Errorf("second run: got %d total writes, want still 1", m.putCount())
	}
}

// TestMergeIngressPlanReportsPruned verifies the pure helper directly, so the
// deletion rule is pinned without going through the API.
func TestMergeIngressPlanReportsPruned(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "gone.example.com", Service: "http://gone:80"},
		{Hostname: "keep.example.com", Service: "http://keep:80"},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}

	merged, changed, _, pruned := mergeIngressPlan(current, plan, map[string]bool{"gone.example.com": true})

	if !changed {
		t.Error("expected a change to be reported")
	}
	if len(pruned) != 1 || pruned[0] != "gone.example.com" {
		t.Errorf("pruned = %v, want just gone.example.com", pruned)
	}
	for _, r := range merged {
		if r.Hostname == "gone.example.com" {
			t.Error("pruned rule still present in the merged list")
		}
	}
}

// TestMergeIngressPlanPrunedSetIsExact: a name in the pruned set that does not
// appear in the current rules changes nothing.
func TestMergeIngressPlanPrunedSetIsExact(t *testing.T) {
	current := []cfIngressRule{
		{Hostname: "keep.example.com", Service: "http://keep:80"},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}

	merged, _, _, pruned := mergeIngressPlan(current, plan, map[string]bool{"never-declared.example.com": true})

	if len(pruned) != 0 {
		t.Errorf("pruned = %v, want none", pruned)
	}
	for _, r := range merged {
		if r.Hostname == "keep.example.com" {
			return
		}
	}
	t.Error("unrelated rule was dropped")
}

// TestInheritUnmanagedFieldsDirect exercises the helper's precedence rules.
// Description is not part of it: the plugin authors that field.
func TestInheritUnmanagedFieldsDirect(t *testing.T) {
	merged := []cfIngressRule{
		{Hostname: "a.example.com", Service: "s", Description: testTag},
		{Hostname: "b.example.com", Service: "s", Path: json.RawMessage(`"/derived"`)},
		{Service: "s", Description: testTag},
	}
	current := []cfIngressRule{
		{Hostname: "a.example.com", Service: "old", Description: "from current", OriginRequest: json.RawMessage(`{"x":1}`)},
		{Hostname: "b.example.com", Service: "old", Path: json.RawMessage(`"/current"`), OriginRequest: json.RawMessage(`{"y":2}`)},
		{Service: "old", Description: "catch-all from current", OriginRequest: json.RawMessage(`{"z":3}`)},
	}
	inheritUnmanagedFields(merged, current)

	if string(merged[0].OriginRequest) != `{"x":1}` {
		t.Errorf("originRequest not inherited: %s", merged[0].OriginRequest)
	}
	if merged[0].Description != testTag {
		t.Errorf("description must come from the plan, not be inherited: %q", merged[0].Description)
	}
	if string(merged[1].Path) != `"/derived"` {
		t.Errorf("existing path must win: %s", merged[1].Path)
	}
	if string(merged[1].OriginRequest) != `{"y":2}` {
		t.Errorf("originRequest not inherited for b: %s", merged[1].OriginRequest)
	}
	if string(merged[2].OriginRequest) != `{"z":3}` {
		t.Errorf("catch-all (empty hostname) not matched: %s", merged[2].OriginRequest)
	}

	// A rule with no counterpart keeps empty fields rather than inheriting
	// unrelated metadata.
	fresh := []cfIngressRule{{Hostname: "new.example.com", Service: "s"}}
	inheritUnmanagedFields(fresh, current)
	if len(fresh[0].OriginRequest) != 0 || len(fresh[0].Path) != 0 {
		t.Errorf("unmatched rule gained metadata: %#v", fresh[0])
	}
}

// TestMergePreservesOriginRequestOnRewrittenRule covers the metadata the
// plugin does not author but must not destroy: an originRequest such as
// matchSNItoHost, which a declared host's rule may carry because the operator
// set it in the dashboard. Rewriting the rule must keep it.
func TestMergePreservesOriginRequestOnRewrittenRule(t *testing.T) {
	sni := json.RawMessage(`{"matchSNItoHost":true}`)
	current := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443", OriginRequest: sni},
		{Service: "https://caddy:443", OriginRequest: sni},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "https://caddy:443"},
	}
	merged, _, _, _ := mergeIngressPlan(current, plan, nil)

	if len(merged) != 2 {
		t.Fatalf("got %d rules, want 2: %#v", len(merged), merged)
	}
	if string(merged[0].OriginRequest) != string(sni) {
		t.Errorf("rewritten host rule lost its originRequest: %s", merged[0].OriginRequest)
	}
	if string(merged[1].OriginRequest) != string(sni) {
		t.Errorf("re-emitted catch-all lost its originRequest: %s", merged[1].OriginRequest)
	}
}

// TestMergePreservesPathOnRewrittenRule is the same guarantee for path, which
// would otherwise silently degrade a path-scoped rule into a catch-all for the
// hostname.
func TestMergePreservesPathOnRewrittenRule(t *testing.T) {
	path := json.RawMessage(`"/api/.*"`)
	current := []cfIngressRule{
		{Hostname: "git.example.com", Service: "http://api:8081", Path: path},
		{Service: "http_status:404"},
	}
	plan := []cfIngressRule{
		{Hostname: "git.example.com", Service: "https://caddy:443"},
		{Service: "http_status:404"},
	}
	merged, _, _, _ := mergeIngressPlan(current, plan, nil)

	if string(merged[0].Path) != string(path) {
		t.Errorf("rewritten host rule lost its path: %s", merged[0].Path)
	}
}

func TestIngressGetFailureIsSurfaced(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	m.setFailGet(true)
	app := newIngressTestApp(t, m, "")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	err := runIngress(app, hosts)
	if err == nil {
		t.Fatal("expected a read failure to be surfaced")
	}
	// The Cloudflare error envelope's code and message must survive decoding,
	// so an operator can act on the API's own explanation.
	if !strings.Contains(err.Error(), "code=1000") {
		t.Errorf("error must carry the Cloudflare error code: %v", err)
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("error must carry the Cloudflare error message: %v", err)
	}
	if m.putCount() != 0 {
		t.Error("no write may be attempted after a failed read")
	}
}

func TestIngressPutFailureIsSurfaced(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	m.setFailPut(true)
	app := newIngressTestApp(t, m, "")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	err := runIngress(app, hosts)
	if err == nil {
		t.Fatal("expected a write failure to be surfaced")
	}
	if !strings.Contains(err.Error(), "code=1000") || !strings.Contains(err.Error(), "internal error") {
		t.Errorf("write error must carry the Cloudflare code and message: %v", err)
	}
}

func TestIngressEmptyConfigurationIsNotAnError(t *testing.T) {
	// A tunnel that has never been configured may report no ingress at all.
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{Ingress: nil})
	app := newIngressTestApp(t, m, "")

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	got := m.lastPut().Ingress
	if len(got) != 2 {
		t.Fatalf("got %d rules, want host + catch-all: %#v", len(got), got)
	}
	if got[len(got)-1].Service != defaultTunnelService {
		t.Errorf("catch-all = %q, want fail-closed default", got[len(got)-1].Service)
	}
}

// --- adapter-level tests for the new Caddyfile surface ----------------------

func TestAdaptAccountAndTunnelDefaultService(t *testing.T) {
	t.Setenv("CF_EXAMPLE", "zone-token")
	t.Setenv("CF_TUNNEL", "account-token")

	input := `
{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE}
		account {$CF_TUNNEL}
		account_id 023e105f4ecef8ad9ca31a8372d0c353
		tunnel_default_service https://caddy:443
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)

	appCfg, ok := cfg["apps"].(map[string]any)[appName].(map[string]any)
	if !ok {
		t.Fatalf("app config missing or unexpected shape: %#v", cfg["apps"])
	}
	if appCfg["tunnel_api_token"] != "account-token" {
		t.Errorf("tunnel_api_token = %v, want the declared token", appCfg["tunnel_api_token"])
	}
	if appCfg["account_id"] != "023e105f4ecef8ad9ca31a8372d0c353" {
		t.Errorf("account_id = %v, want the declared override", appCfg["account_id"])
	}
	if appCfg["tunnel_default_service"] != "https://caddy:443" {
		t.Errorf("tunnel_default_service = %v, want the declared service", appCfg["tunnel_default_service"])
	}
}

// TestAdaptAccountTokenOnly covers the normal form: the account id is derived
// from the zone lookup, so `account` carries only the token.
func TestAdaptAccountTokenOnly(t *testing.T) {
	t.Setenv("CF_TUNNEL", "account-token")

	cfg := adaptCaddyfile(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		account {$CF_TUNNEL}
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`)
	appCfg, ok := cfg["apps"].(map[string]any)[appName].(map[string]any)
	if !ok {
		t.Fatalf("app config missing or unexpected shape: %#v", cfg["apps"])
	}
	if appCfg["tunnel_api_token"] != "account-token" {
		t.Errorf("tunnel_api_token = %v, want the declared token", appCfg["tunnel_api_token"])
	}
	if _, present := appCfg["account_id"]; present {
		t.Errorf("account_id must stay unset when only the token is declared: %#v", appCfg)
	}
}

func TestAdaptRejectsAccountWithoutToken(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		account
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`, "account requires an API token")
}

func TestAdaptRejectsTunnelServiceWithoutTunnel(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel_service https://caddy:443
	}
	respond "ok"
}
`, "tunnel_service requires a tunnel")
}

func TestAdaptAcceptsTunnelServiceWithTunnel(t *testing.T) {
	cfg := adaptCaddyfile(t, `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
		tunnel_service https://caddy:443
	}
	respond "ok"
}
`)
	assertAppPresent(t, cfg)
}

func TestAdaptRejectsBadTunnelDefaultService(t *testing.T) {
	requireAdaptErr(t, `
{
	cf_dns_manager {
		zone example.com api_token x
		tunnel_default_service ftp://nope:21
	}
}

example.com {
	cf_dns_manager {
		host example.com
	}
	respond "ok"
}
`, "tunnel_default_service")
}

func TestParseTunnelServiceValidation(t *testing.T) {
	valid := []string{
		"https://caddy:443", "http://caddy:80", "ssh://caddy:22", "tcp://1.2.3.4:5432",
		"unix:///tmp/sock", "unix+tls:///tmp/sock", "rdp://host:3389", "smb://host:445",
		"http_status:404", "http_status:200",
	}
	for _, v := range valid {
		if err := parseTunnelService(v); err != nil {
			t.Errorf("parseTunnelService(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{
		"", "caddy:443", "ftp://caddy:21", "https://", "http_status:40", "http_status:abcd",
	}
	for _, v := range invalid {
		if err := parseTunnelService(v); err == nil {
			t.Errorf("parseTunnelService(%q) = nil, want an error", v)
		}
	}
}

// --- verification that the fail-closed default is not a routing guess -------

func TestIngressDefaultNeverRoutesToACaddyGuess(t *testing.T) {
	m := newMockTunnelAPI(t, testTunnelA, cfTunnelConfig{})
	app := newIngressTestApp(t, m, "") // unset default

	hosts := []HostConfig{{Host: "git.example.com", TunnelID: testTunnelA}}
	if err := runIngress(app, hosts); err != nil {
		t.Fatalf("reconcile ingress: %v", err)
	}

	for _, r := range m.storedConfig().Ingress {
		if r.Hostname == "" && strings.Contains(r.Service, "caddy") {
			t.Errorf("unconfigured default must not guess a caddy origin: %#v", r)
		}
	}
}
