package cfdnsmanager

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const testTunnelUUID = "11111111-2222-3333-4444-555555555555"

// --- Parsing / adapt validation (task 3.1) ---

func TestAdaptTunnelValid(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel ` + testTunnelUUID + `
		force_adopt
	}
	respond "x"
}
`
	cfg := adaptCaddyfile(t, input)
	assertAppPresent(t, cfg)
}

func TestAdaptTunnelMalformedUUIDFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel not-a-uuid
	}
	respond "x"
}
`
	requireAdaptErr(t, input, "tunnel must be a UUID")
}

func TestAdaptTunnelMissingArgFails(t *testing.T) {
	input := `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel
	}
	respond "x"
}
`
	requireAdaptErr(t, input, "wrong argument count or unexpected line ending after 'tunnel'")
}

func TestAdaptTunnelWithIPFails(t *testing.T) {
	input := tunnelAdaptInput("ip 1.2.3.4")
	requireAdaptErr(t, input, "tunnel cannot be combined with ip")
}

func TestAdaptTunnelWithIP6Fails(t *testing.T) {
	input := tunnelAdaptInput("ip6 auto")
	requireAdaptErr(t, input, "tunnel cannot be combined with ip6")
}

func TestAdaptTunnelWithProxiedFails(t *testing.T) {
	input := tunnelAdaptInput("proxied no")
	requireAdaptErr(t, input, "tunnel cannot be combined with proxied")
}

func tunnelAdaptInput(extra string) string {
	return `
{
	cf_dns_manager {
		zone example.com api_token x
	}
}

example.com {
	cf_dns_manager {
		host example.com
		tunnel ` + testTunnelUUID + `
		` + extra + `
	}
	respond "x"
}
`
}

func TestIsUUID(t *testing.T) {
	cases := map[string]bool{
		testTunnelUUID:                          true,
		"11111111-2222-3333-4444-555555555555":  true,
		"not-a-uuid":                            false,
		"11111111-2222-3333-4444-555555555555":      false,
		"11111111-2222-3333-4444-555555555555":   false,
		"11111111-2222-3333-4444-5555555555550": false,
		"11111111-2222-3333-4444-555555555555":  false,
		"":                                      false,
	}
	for in, want := range cases {
		if got := isUUID(in); got != want {
			t.Errorf("isUUID(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- CNAME create / update / adopt (task 3.2) ---

func TestTunnelCreatesCNAME(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", false) // detection must not be needed

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByNameType("git", "CNAME")
	if rec == nil {
		t.Fatal("expected git CNAME created")
	}
	if rec.Content != testTunnelUUID+".cfargotunnel.com" {
		t.Errorf("content = %q, want tunnel target", rec.Content)
	}
	if !rec.Proxied {
		t.Error("tunnel CNAME must always be proxied")
	}
	if rec.Comment != "caddy-cf-dns:test-host" {
		t.Errorf("comment = %q, want ownership tag", rec.Comment)
	}
	if m.recordByNameType("git", "A") != nil {
		t.Error("tunnel host must not create an A record")
	}
}

func TestTunnelInSyncNoWrite(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "git", Content: testTunnelUUID + ".cfargotunnel.com", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/c1") {
		t.Error("in-sync CNAME must not be updated")
	}
	if m.hasCall("POST /zones/zone-example.com/dns_records") {
		t.Error("in-sync CNAME must not be recreated")
	}
}

func TestTunnelUpdatesOnDrift(t *testing.T) {
	other := "11111111-2222-3333-4444-555555555555"
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "git", Content: other + ".cfargotunnel.com", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("PUT /zones/zone-example.com/dns_records/c1") {
		t.Fatal("expected CNAME update on drift")
	}
	rec := m.recordByNameType("git", "CNAME")
	if rec.Content != testTunnelUUID+".cfargotunnel.com" || !rec.Proxied {
		t.Errorf("CNAME not updated correctly: %+v", rec)
	}
}

func TestTunnelUntaggedCNAMESkippedWithoutForceAdopt(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "git", Content: "old.example.net", TTL: 1, Proxied: true, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("PUT /zones/zone-example.com/dns_records/c1") {
		t.Error("untagged CNAME must not be updated without force_adopt")
	}
	if rec := m.recordByNameType("git", "CNAME"); rec.Content != "old.example.net" {
		t.Errorf("untagged CNAME changed to %q", rec.Content)
	}
}

func TestTunnelUntaggedCNAMEForceAdopted(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "git", Content: "old.example.net", TTL: 1, Proxied: true, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ForceAdopt: true, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rec := m.recordByNameType("git", "CNAME")
	if rec.Content != testTunnelUUID+".cfargotunnel.com" {
		t.Errorf("force-adopted CNAME content = %q", rec.Content)
	}
	if !rec.Proxied || rec.Comment != "caddy-cf-dns:test-host" {
		t.Errorf("force-adopted CNAME not proxied/tagged: %+v", rec)
	}
}

// --- Migration A/AAAA -> CNAME (task 3.3) ---

func TestTunnelMigrationDeletesOwnedA(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "git", Content: "203.0.113.10", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("expected owned A deleted during tunnel migration")
	}
	if m.recordByNameType("git", "CNAME") == nil {
		t.Error("expected CNAME created after A deletion")
	}
}

func TestTunnelUntaggedABlocksWithoutForceAdopt(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "git", Content: "9.9.9.9", TTL: 1, Proxied: false, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile must not hard-error on a blocked tunnel host: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("untagged A must not be deleted without force_adopt")
	}
	if m.hasCall("POST /zones/zone-example.com/dns_records") {
		t.Error("CNAME must not be created while an untagged A blocks the name")
	}
	if rec := m.recordByNameType("git", "A"); rec == nil || rec.Content != "9.9.9.9" {
		t.Errorf("untagged A must be left unchanged: %+v", rec)
	}
}

func TestTunnelUntaggedAForceAdopted(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r1", Type: "A", Name: "git", Content: "9.9.9.9", TTL: 1, Proxied: false, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ForceAdopt: true, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r1") {
		t.Error("expected untagged A deleted with force_adopt")
	}
	if m.recordByNameType("git", "CNAME") == nil {
		t.Error("expected CNAME created after untagged A deletion")
	}
	if m.recordByNameType("git", "A") != nil {
		t.Error("A must be gone after force-adopt migration")
	}
}

func TestTunnelMigrationDeletesOwnedAAAA(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "r6", Type: "AAAA", Name: "git", Content: "2001:db8::1", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/r6") {
		t.Error("expected owned AAAA deleted during tunnel migration")
	}
	if m.recordByNameType("git", "CNAME") == nil {
		t.Error("expected CNAME created after AAAA deletion")
	}
}

// --- Prune participation (task 3.4) ---

func TestPruneRemovesOwnedTunnelCNAME(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "gone", Content: testTunnelUUID + ".cfargotunnel.com", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	m.zonePrune = true
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "keep.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("expected orphaned owned CNAME to be pruned")
	}
}

func TestPruneNeverDeletesUntaggedCNAME(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "manual", Content: "somewhere.example.net", TTL: 1, Proxied: true, Comment: ""},
	})
	m.zonePrune = true
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "keep.example.com", ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("untagged CNAME must never be pruned")
	}
}

func TestPruneKeepsDeclaredTunnelCNAME(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "git", Content: testTunnelUUID + ".cfargotunnel.com", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	m.zonePrune = true
	app, _ := testApp(t, m, "203.0.113.10", false)

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("still-declared tunnel CNAME must not be pruned")
	}
}

// --- Revert CNAME -> A (task 3.6) ---

func TestRevertTunnelDeletesOwnedCNAME(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "web", Content: testTunnelUUID + ".cfargotunnel.com", TTL: 1, Proxied: true, Comment: "caddy-cf-dns:test-host"},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "web.example.com", IP: "203.0.113.10", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("expected owned CNAME deleted on revert to address host")
	}
	if m.recordByNameType("web", "CNAME") != nil {
		t.Error("CNAME must be gone after revert")
	}
	rec := m.recordByNameType("web", "A")
	if rec == nil || rec.Content != "203.0.113.10" {
		t.Errorf("A record not created on revert (same run): %+v", rec)
	}
}

func TestRevertUntaggedCNAMEBlocksWithoutForceAdopt(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "web", Content: "somewhere.example.net", TTL: 1, Proxied: true, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "web.example.com", IP: "203.0.113.10", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile must not hard-error on a blocked revert: %v", err)
	}
	if m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("untagged CNAME must not be deleted without force_adopt")
	}
	if m.recordByNameType("web", "A") != nil {
		t.Error("A must not be created while an untagged CNAME blocks the name")
	}
	if rec := m.recordByNameType("web", "CNAME"); rec == nil || rec.Content != "somewhere.example.net" {
		t.Errorf("untagged CNAME must be left unchanged: %+v", rec)
	}
}

func TestRevertUntaggedCNAMEForceAdopted(t *testing.T) {
	m := newMockCloudflare(t, "example.com", []cfDNSRecord{
		{ID: "c1", Type: "CNAME", Name: "web", Content: "somewhere.example.net", TTL: 1, Proxied: true, Comment: ""},
	})
	app, _ := testApp(t, m, "203.0.113.10", true)

	hosts := []HostConfig{
		{Host: "web.example.com", IP: "203.0.113.10", Proxied: boolPtr(true), ForceAdopt: true, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !m.hasCall("DELETE /zones/zone-example.com/dns_records/c1") {
		t.Error("expected untagged CNAME deleted with force_adopt")
	}
	rec := m.recordByNameType("web", "A")
	if rec == nil || rec.Content != "203.0.113.10" {
		t.Errorf("A record not created after force-adopt revert: %+v", rec)
	}
}

// --- Detection (task 3.5) ---

// countingDetectionServer returns a detection server counting requests.
func countingDetectionServer(t *testing.T, publicIP string) (*httptest.Server, *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Write([]byte("ip=" + publicIP + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestTunnelOnlyConfigSkipsDetection(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", true)

	det, hits := countingDetectionServer(t, "203.0.113.10")
	app.IPURL = det.URL

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("tunnel-only config made %d detection request(s), want 0", n)
	}
}

func TestMixedConfigStillDetectsForNormalHosts(t *testing.T) {
	m := newMockCloudflare(t, "example.com", nil)
	app, _ := testApp(t, m, "203.0.113.10", true)

	det, hits := countingDetectionServer(t, "203.0.113.10")
	app.IPURL = det.URL

	hosts := []HostConfig{
		{Host: "git.example.com", TunnelID: testTunnelUUID, ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
		{Host: "foo.example.com", Proxied: boolPtr(true), ZoneConfig: ZoneConfig{Zone: "example.com", APIToken: "token"}},
	}
	if err := app.Reconcile(hosts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n := atomic.LoadInt32(hits); n == 0 {
		t.Error("mixed config must still detect for normal hosts")
	}
	if rec := m.recordByNameType("git", "CNAME"); rec == nil {
		t.Error("tunnel CNAME must still be created in mixed config")
	}
	if rec := m.recordByNameType("foo", "A"); rec == nil || rec.Content != "203.0.113.10" {
		t.Errorf("normal host must still reconcile: %+v", rec)
	}
}
