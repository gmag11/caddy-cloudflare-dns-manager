package cfdnsmanager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// testApp builds an App with tag/instance defaults, pointing at the mock CF
// server and a local IP-detection server.
func testApp(t *testing.T, m *mockCloudflare, publicIP string, ipHosts bool) (*App, *httptest.Server) {
	t.Helper()

	cfSrv := m.server(t)

	// Local IP-detection endpoint returning publicIP (used only if ipHosts).
	detSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ipHosts {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ip=" + publicIP + "\n"))
	}))
	t.Cleanup(detSrv.Close)

	app := &App{
		Zones: []ZoneConfig{
			{Zone: m.zoneName, APIToken: "token", Prune: m.zonePrune},
		},
		IPURL:     detSrv.URL,
		TagPrefix: "caddy-cf-dns",
		Instance:  "test-host",
		logger:    zap.NewNop(),
		apiBase:   cfSrv.URL,
	}
	return app, detSrv
}

func TestReconcileCreatesMissing(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByName("foo")
	if rec == nil {
		t.Fatal("expected foo record created")
	}
	if rec.Content != "203.0.113.10" {
		t.Errorf("content = %q, want public ip", rec.Content)
	}
	if !rec.Proxied {
		t.Error("expected proxied default")
	}
	if rec.Comment != "caddy-cf-dns:test-host" {
		t.Errorf("comment = %q, want ownership tag", rec.Comment)
	}
}

func TestReconcileIdempotentNoWrite(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Error("expected no update when state already matches")
	}
}

// TestReconcileFQDNRecordName reproduces the real Cloudflare API shape: the
// list endpoint returns record names as FQDNs, not the plugin's relative form.
// A pre-existing owned record must be recognized (no duplicate create), and a
// drifted one must be updated rather than recreated.
func TestReconcileFQDNRecordName(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "test2.example.com", Content: "203.0.113.10", TTL: 1, Proxied: false, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	// Same IP: no write at all.
	hosts := []HostConfig{
		{
			Host: "test2.example.com", IP: "203.0.113.10",
			Proxied:    boolPtr(false),
			ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"},
		},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("POST /zones/zone-example.com/dns_records") {
		t.Error("existing FQDN-named record must not be recreated")
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Error("record already in sync must not be updated")
	}

	// IP drift: update the existing record, still no create.
	hosts[0].IP = "198.51.100.7"
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile (drift): %v", err)
	}
	if m.hasCall("POST /zones/zone-example.com/dns_records") {
		t.Error("must update, not recreate, an existing FQDN-named record")
	}
	if !m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Error("expected update of the existing FQDN-named record")
	}
	if rec := m.recordByName("test2"); rec == nil || rec.Content != "198.51.100.7" {
		t.Errorf("record not updated on drift: %+v", rec)
	}
}

func TestReconcileUpdatesOnDrift(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "1.2.3.4", TTL: 1, Proxied: false, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Fatal("expected update on drift")
	}
	rec := m.recordByName("foo")
	if rec.Content != "203.0.113.10" || !rec.Proxied {
		t.Errorf("record not updated: %+v", rec)
	}
}

func TestReconcilePrivateIPForcesDNSOnly(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{
			Host: "tail.example.com", IP: "100.64.10.5",
			Proxied:    boolPtr(true), // user asked proxied, but private forces off
			ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"},
		},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByName("tail")
	if rec == nil {
		t.Fatal("expected tail record")
	}
	if rec.Proxied {
		t.Error("private IP must force dns-only")
	}
	if rec.Content != "100.64.10.5" {
		t.Errorf("content = %q, want override", rec.Content)
	}
}

func TestReconcileConservativeUntaggedSkipped(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "9.9.9.9", TTL: 1, Proxied: false, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "foo.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Error("untagged record must not be updated without force_adopt")
	}
	rec := m.recordByName("foo")
	if rec.Content != "9.9.9.9" {
		t.Errorf("record content changed to %q", rec.Content)
	}
}

func TestReconcileForceAdopt(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "9.9.9.9", TTL: 1, Proxied: false, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "foo.example.com", ForceAdopt: true, Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByName("foo")
	if rec.Content != "203.0.113.10" {
		t.Errorf("force-adopted content = %q", rec.Content)
	}
	if rec.Comment != "caddy-cf-dns:test-host" {
		t.Errorf("force-adopted comment = %q", rec.Comment)
	}
}

func TestReconcileDetectionFailure(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	// ipHosts=false -> detection endpoint returns 500.
	app, _ := testApp(t, m, "203.0.113.10", false)

	// auto-IP host (no explicit ip): detection fails -> existing record left unchanged.
	autoHost := []HostConfig{
		{Host: "foo.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(autoHost); err != nil {
		t.Fatalf("reconcile must not error on detection failure: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r1") {
		t.Error("existing auto record must not be updated when detection fails")
	}
	// no existing record + detection fails => no create call.
	if m.hasCall("POST /zones/zone-example.com/dns_records") {
		t.Error("must not create auto record when detection fails")
	}

	// explicit-IP host still reconciles normally.
	explicit := []HostConfig{
		{Host: "fixed.example.com", IP: "100.64.10.5", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	m2 := newMockCloudflare(t, "example.com", nil)
	app2, _ := testApp(t, m2, "203.0.113.10", false)
	if err := app2.Reconcile(explicit); err != nil {
		t.Fatalf("explicit-ip reconcile failed: %v", err)
	}
	if rec := m2.recordByName("fixed"); rec == nil || rec.Content != "100.64.10.5" {
		t.Errorf("explicit-ip record not reconciled: %+v", rec)
	}
}

// testApp6 extends testApp with a local IPv6 detection endpoint. ip6Hosts
// controls whether v6 detection succeeds; publicIP6 is the returned address.
func testApp6(t *testing.T, m *mockCloudflare, publicIP, publicIP6 string, ipHosts, ip6Hosts bool) *App {
	t.Helper()
	app, _ := testApp(t, m, publicIP, ipHosts)
	det6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ip6Hosts {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ip=" + publicIP6 + "\n"))
	}))
	t.Cleanup(det6.Close)
	app.IP6URL = det6.URL
	return app
}

func TestReconcileCreatesBothFamilies(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec := m.recordByNameType("foo", "A"); rec == nil || rec.Content != "203.0.113.10" {
		t.Errorf("A record wrong: %+v", rec)
	}
	rec6 := m.recordByNameType("foo", "AAAA")
	if rec6 == nil {
		t.Fatal("expected foo AAAA created")
	}
	if rec6.Content != "2001:db8::10" {
		t.Errorf("AAAA content = %q", rec6.Content)
	}
	if !rec6.Proxied {
		t.Error("public AAAA should be proxied")
	}
	if rec6.Comment != "caddy-cf-dns:test-host" {
		t.Errorf("AAAA comment = %q", rec6.Comment)
	}
}

func TestReconcileIPv6DisabledByDefault(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	hosts := []HostConfig{
		{Host: "foo.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec := m.recordByNameType("foo", "AAAA"); rec != nil {
		t.Errorf("no AAAA expected when ip6 disabled: %+v", rec)
	}
}

func TestReconcileIPv6Literal(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app := testApp6(t, m, "203.0.113.10", "", false, false) // detection off: literal must still work

	hosts := []HostConfig{
		{
			Host: "tail.example.com", IP6: "fd7a:115c:a1e0::1",
			Proxied:    boolPtr(true),
			ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"},
		},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByNameType("tail", "AAAA")
	if rec == nil || rec.Content != "fd7a:115c:a1e0::1" {
		t.Fatalf("AAAA not reconciled: %+v", rec)
	}
	if rec.Proxied {
		t.Error("ULA AAAA must be dns-only")
	}
}

func TestReconcileIPv6OnlyWhenV4Fails(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	// v4 detection fails, v6 succeeds.
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", false, true)

	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec := m.recordByNameType("foo", "A"); rec != nil {
		t.Errorf("A must be skipped when v4 detection fails: %+v", rec)
	}
	if rec := m.recordByNameType("foo", "AAAA"); rec == nil || rec.Content != "2001:db8::10" {
		t.Errorf("AAAA must be created despite v4 failure: %+v", rec)
	}
}

func TestReconcileIPv6DetectionFailureSparesExisting(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app := testApp6(t, m, "203.0.113.10", "2001:db8::dead", true, false)

	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r6") {
		t.Error("existing AAAA must be left unchanged on v6 detection failure")
	}
}

func TestReconcileCrossFamilyNotAdopted(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::1", TTL: 1, Proxied: false, Comment: ""},
	})
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	// Host manages only A; the untagged AAAA must be untouched.
	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec := m.recordByNameType("foo", "AAAA"); rec == nil || rec.Content != "2001:db8::1" {
		t.Errorf("untagged AAAA must be untouched: %+v", rec)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r6") {
		t.Error("untagged AAAA must not be updated via A management")
	}
}

func TestReconcileNoAAAAWithoutAutoHosts(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", true)
	// Point IP6URL at an endpoint that would fail if ever called.
	app.IP6URL = "http://127.0.0.1:1/"
	hosts := []HostConfig{
		{Host: "foo.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestReconcileAAAAIdempotentNoWrite(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)
	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/r1") || m.hasCall("PUT /zones/zone-example.com/dns_records/r6") {
		t.Error("expected no writes when both families already in sync")
	}
}

func TestReconcileAAAAUpdatesOnDrift(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::dead", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)
	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("PUT /zones/zone-example.com/dns_records/r6") {
		t.Fatal("expected AAAA update on drift")
	}
	if rec := m.recordByNameType("foo", "AAAA"); rec == nil || rec.Content != "2001:db8::10" {
		t.Errorf("AAAA not updated: %+v", rec)
	}
}

func TestReconcileAAAANestedName(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)
	hosts := []HostConfig{
		{Host: "a.app.example.com", IP6: IP6Auto, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec := m.recordByNameType("a.app", "AAAA"); rec == nil || rec.Content != "2001:db8::10" {
		t.Errorf("nested AAAA not created with relative name: %+v", rec)
	}
}

func TestPruneRemovesAAAAOnHostRemoval(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "gone", Content: "2001:db8::1", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	m.zonePrune = true
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	hosts := []HostConfig{
		{Host: "keep.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r6") {
		t.Error("expected orphaned AAAA to be pruned")
	}
}

func TestPruneAAAAWhenIPv6Disabled(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "foo", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::1", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	m.zonePrune = true
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	// Host still declared, but ip6 disabled -> AAAA is config-retired.
	hosts := []HostConfig{
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r6") {
		t.Error("expected AAAA pruned when ip6 disabled")
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("A record must not be pruned")
	}
}

func TestPruneAAAAWhenIPv6DisabledWithoutPrune(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::1", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	// no prune
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, true)

	hosts := []HostConfig{
		{Host: "foo.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r6") {
		t.Error("AAAA must be left orphaned without prune")
	}
}

func TestPruneSparesAAAADuringDetectionFailure(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "foo", Content: "2001:db8::1", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	m.zonePrune = true
	app := testApp6(t, m, "203.0.113.10", "2001:db8::10", true, false) // v6 detection fails

	hosts := []HostConfig{
		{Host: "foo.example.com", IP6: IP6Auto, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r6") {
		t.Error("AAAA must be spared from prune on transient detection failure")
	}
}

func TestPruneRemovesOwnOrphans(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "gone", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{ID: "r2", Type: "A", Name: "keep", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{ID: "r3", Type: "A", Name: "manual", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: ""},
		{ID: "r4", Type: "A", Name: "other", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:other-host"},
	})
	// zone opts in to prune
	m.zonePrune = true
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "keep.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r2") {
		t.Error("still-declared record must not be pruned")
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r3") {
		t.Error("manual (untagged) record must never be pruned")
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r4") {
		t.Error("record of another instance must never be pruned")
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("expected own orphan to be pruned")
	}
}

func TestNoPruneWithoutOptIn(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "gone", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	// zone does NOT opt in to prune
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "keep.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("orphans must be left in place when prune is not enabled")
	}
}

// TestPruneWithNoDeclaredHosts covers removing the last declared host: the
// zone opted in to prune, so reconcile must still run and delete this
// instance's now-orphaned record even though there are zero hosts.
func TestPruneWithNoDeclaredHosts(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "last", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
		{ID: "r2", Type: "A", Name: "manual", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: ""},
	})
	m.zonePrune = true
	app, _ := testApp(t, m, "203.0.113.10", true)

	if err := app.Reconcile(nil); err != nil {
		t.Fatalf("reconcile with no hosts: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("last host's own record must be pruned when no hosts remain declared")
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r2") {
		t.Error("untagged record must never be pruned")
	}
}
