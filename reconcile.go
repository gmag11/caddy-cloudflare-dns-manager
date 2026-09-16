package cfdnsmanager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// Reconcile reconciles the declared hosts against Cloudflare. It is called
// once per config load from Start(). It performs one public-IP detection per
// address family (in parallel), shared by all auto hosts, reconciles each
// host's A and (when enabled) AAAA records, then runs per-zone prune for zones
// that opted in.
func (app *App) Reconcile(hosts []HostConfig) error {
	ctx := context.Background()

	zoneClients := make(map[string]*cloudflareClient)
	for _, z := range app.Zones {
		if app.apiBase != "" {
			zoneClients[strings.ToLower(z.Zone)] = newCloudflareClientWithBase(app.apiBase, z.APIToken)
		} else {
			zoneClients[strings.ToLower(z.Zone)] = newCloudflareClient(z.APIToken)
		}
	}

	// Detect public IPs once per family for all auto hosts. Failures are
	// non-blocking and independent between families.
	publicIP, publicIP6, ipDetectionFailed, ip6DetectionFailed := app.detectPublicIPs(ctx, hosts)

	// Group hosts by zone so each zone ID is resolved once. The zone is
	// recomputed here from the declared zones (HostConfig.ZoneConfig is not
	// serialized through the route handler config).
	byZone := make(map[string][]HostConfig)
	for _, hc := range hosts {
		if err := assignZone(app.Zones, &hc); err != nil {
			errs := []error{err}
			return fmt.Errorf("1 host(s) failed: %v", errs)
		}
		zkey := strings.ToLower(hc.ZoneConfig.Zone)
		byZone[zkey] = append(byZone[zkey], hc)
	}

	// Zones that opted in to prune must be visited even when no host is
	// declared, so removing the last declared host still cleans up that
	// instance's orphaned records. Without this, a config with zero hosts
	// would skip reconcile entirely and leave the final record behind.
	for _, z := range app.Zones {
		if !z.Prune {
			continue
		}
		zkey := strings.ToLower(z.Zone)
		if _, ok := byZone[zkey]; !ok {
			byZone[zkey] = nil
		}
	}

	var (
		wg    sync.WaitGroup
		errMu sync.Mutex
		errs  []error
	)

	// accountByZone captures the owning account id of each reconciled zone, as
	// reported by the zone lookup. The ingress phase needs it for the Tunnel
	// API path, and this avoids a second lookup.
	// prunedHosts collects, per zone, the hostnames whose DNS records this run
	// pruned: their routes can no longer receive traffic and are the only ones
	// the ingress phase is allowed to delete.
	var acctMu sync.Mutex
	accountByZone := make(map[string]string)
	prunedByZone := make(map[string][]prunedName)

	for zkey, zoneHosts := range byZone {
		cli, ok := zoneClients[zkey]
		if !ok {
			errs = append(errs, fmt.Errorf("internal error: hosts assigned to undeclared zone %q", zkey))
			continue
		}
		wg.Add(1)
		go func(zone string, cli *cloudflareClient, zoneHosts []HostConfig) {
			defer wg.Done()
			det := familyDetection{ipv4: publicIP, ipv6: publicIP6, ipv4Failed: ipDetectionFailed, ipv6Failed: ip6DetectionFailed}
			res, err := app.reconcileZone(ctx, cli, zone, zoneHosts, det)
			if err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
			acctMu.Lock()
			defer acctMu.Unlock()
			if res.accountID != "" {
				accountByZone[zone] = res.accountID
			}
			if len(res.prunedHosts) > 0 {
				prunedByZone[zone] = res.prunedHosts
			}
		}(zkey, cli, zoneHosts)
	}
	wg.Wait()

	// Tunnel ingress is reconciled after DNS, in its own phase: a tunnel's
	// hosts can span zones, so it cannot be folded into the per-zone fan-out
	// without fragmenting one tunnel's plan across concurrent writes. Failures
	// here are aggregated alongside zone failures, never fatal, so a Tunnel
	// API problem cannot leave DNS unreconciled.
	if err := app.reconcileIngressPhase(ctx, hosts, accountByZone, prunedByZone); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d reconcile operation(s) failed: %v", len(errs), errs)
	}
	return nil
}

// reconcileIngressPhase runs the tunnel ingress reconciliation when a tunnel
// token and at least one tunnel host are configured. Without the token the
// plugin must not call the Tunnel API at all, so the phase is skipped with an
// explanatory log line rather than failing.
//
// accountByZone carries the owning account id discovered during the DNS phase.
// Cloudflare requires a tunnel and its zone to share an account — a
// cfargotunnel.com CNAME only proxies records in the same account — so the
// zone's account is the tunnel's account, and the user does not have to
// configure it.
//
// prunedByZone carries the hostnames whose DNS records the DNS phase deleted,
// each with the tunnel its deleted record pointed at. Their routes are the only
// ones this phase may delete: the DNS record the route depended on is gone, so
// the route can no longer receive traffic. Nothing is inferred from the route
// itself. The tunnel is carried because a host that reverted to an address no
// longer declares one, so the deleted record is the only remaining link.
func (app *App) reconcileIngressPhase(ctx context.Context, hosts []HostConfig, accountByZone map[string]string, prunedByZone map[string][]prunedName) error {
	tunnelHosts := 0
	for _, hc := range hosts {
		if hc.TunnelID != "" {
			tunnelHosts++
		}
	}
	// The phase also runs when there is nothing declared, so a tunnel that just
	// lost its last host still gets its orphaned route cleaned up.
	if tunnelHosts == 0 && len(prunedByZone) == 0 {
		return nil
	}
	if app.TunnelAPIToken == "" {
		if tunnelHosts > 0 {
			app.logger.Info("tunnel hosts declared but no tunnel API token configured; skipping tunnel ingress management",
				zap.Int("tunnel_hosts", tunnelHosts),
				zap.String("hint", "add `account <token>` to the global cf_dns_manager block (the token needs account-scoped Cloudflare Tunnel Write)"))
		}
		return nil
	}

	accountID := app.resolveAccountID(accountByZone)
	if accountID == "" {
		app.logger.Warn("could not determine the Cloudflare account id; skipping tunnel ingress management",
			zap.Int("tunnel_hosts", tunnelHosts),
			zap.String("cause", "no managed zone resolved successfully in this run"))
		return nil
	}

	// Flatten the per-zone pruned names into one map keyed by lowercased
	// hostname. A route may point at a hostname in any declared zone, and
	// membership alone decides eligibility; the tunnel each name carries is
	// what lets the phase reach a tunnel no host declares.
	pruned := make(map[string]prunedName, len(prunedByZone))
	for _, names := range prunedByZone {
		for _, n := range names {
			pruned[strings.ToLower(n.Host)] = n
		}
	}

	cli := newTunnelClientWithBase(app.apiBase, accountID, app.TunnelAPIToken)
	return reconcileTunnelIngress(ctx, cli, hosts, app.TunnelDefaultService, pruned, app.tunnelRegistered, app.logger)
}

// resolveAccountID returns the account id for tunnel API calls. An explicit
// AccountID always wins; otherwise the account of any zone that resolved in
// this run is used, since a tunnel necessarily lives in its zone's account.
// When several zones resolved, the lexicographically smallest key is taken so
// the choice is deterministic.
func (app *App) resolveAccountID(accountByZone map[string]string) string {
	if app.AccountID != "" {
		return app.AccountID
	}
	zones := make([]string, 0, len(accountByZone))
	for z := range accountByZone {
		zones = append(zones, z)
	}
	if len(zones) == 0 {
		return ""
	}
	sort.Strings(zones)
	return accountByZone[zones[0]]
}

// familyDetection carries the shared public-IP detection results for one
// reconcile run, one entry per family.
type familyDetection struct {
	ipv4       string
	ipv6       string
	ipv4Failed bool
	ipv6Failed bool
}

// detectPublicIPs runs the required family detections in parallel and returns
// the detected addresses and per-family failure flags.
func (app *App) detectPublicIPs(ctx context.Context, hosts []HostConfig) (ipv4, ipv6 string, ipv4Failed, ipv6Failed bool) {
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wantV4 bool
		wantV6 bool
	)
	for _, hc := range hosts {
		if hc.TunnelID != "" {
			continue // tunnel hosts never need detection
		}
		if hc.IP == "" {
			wantV4 = true
		}
		if hc.IP6 == IP6Auto {
			wantV6 = true
		}
	}

	if wantV4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, err := detectPublicIPv4(ctx, &http.Client{Timeout: httpTimeout}, app.effectiveIPURL())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				ipv4Failed = true
				app.logger.Warn("could not detect public IPv4; leaving existing auto-IP records unchanged and skipping new auto-IP records",
					zap.Error(err))
			} else {
				ipv4 = ip
			}
		}()
	}
	if wantV6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, err := detectPublicIPv6(ctx, &http.Client{Timeout: ip6DetectTimeout}, app.effectiveIP6URL())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				ipv6Failed = true
				app.logger.Warn("could not detect public IPv6; leaving existing auto-IP AAAA records unchanged and skipping new AAAA records",
					zap.Error(err))
			} else {
				ipv6 = ip
			}
		}()
	}
	wg.Wait()
	return ipv4, ipv6, ipv4Failed, ipv6Failed
}

// reconcileZoneResult carries what the per-zone pass learned that later phases
// need: the zone's owning account (for the Tunnel API path) and the hostnames
// whose DNS records this pass pruned together with the tunnel each one pointed
// at (so the ingress phase can clean up the matching now-unreachable routes).
type reconcileZoneResult struct {
	accountID   string
	prunedHosts []prunedName
}

// reconcileZone reconciles all hosts in one zone, then prunes if enabled. It
// returns the zone's owning account id (empty when the lookup failed) and the
// hostnames pruned in this pass, so the ingress phase can address the Tunnel
// API without a second lookup and clean up routes that just became unreachable.
func (app *App) reconcileZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone string,
	hosts []HostConfig,
	det familyDetection,
) (reconcileZoneResult, error) {
	zoneID, accountID, err := cli.zoneByName(ctx, zone)
	if err != nil {
		return reconcileZoneResult{}, fmt.Errorf("zone %q: %v", zone, err)
	}

	records, err := cli.listRecords(ctx, zoneID)
	if err != nil {
		return reconcileZoneResult{accountID: accountID}, fmt.Errorf("zone %q: listing records: %v", zone, err)
	}

	// Canonical map from zone-relative record name ("@", "foo", "a.b") and
	// record type to existing records. Cloudflare returns record names as
	// FQDNs, so they must be normalized to the same relative form the plugin
	// computes for hosts.
	byName := make(map[string]map[string][]cfDNSRecord)
	for _, r := range records {
		key := canonicalNameKey(r.Name, zone)
		if byName[key] == nil {
			byName[key] = make(map[string][]cfDNSRecord)
		}
		byName[key][r.Type] = append(byName[key][r.Type], r)
	}

	// managed marks the (name, type) pairs the declared configuration asks the
	// plugin to manage. Prune eligibility is derived from this, never from
	// whether a reconcile attempt succeeded: a family enabled in config but
	// skipped by a transient detection failure stays managed and is spared.
	managed := make(map[string]map[string]bool)

	tag := app.ownershipTag()

	// clearedNames collects hostnames whose CNAME this pass deleted to make
	// room for address records (a tunnel-to-address revert), each with the
	// tunnel that CNAME pointed at. Those routes can no longer receive traffic,
	// so they join the prune set the ingress phase consumes.
	var clearedNames []prunedName

	for _, hc := range hosts {
		name := recordName(hc.Host, zone)
		key := canonicalNameKey(name, zone)

		// Tunnel hosts reconcile a single proxied CNAME and never touch
		// detected IPs; address records at the name are cleared first (a
		// CNAME cannot coexist with A/AAAA in Cloudflare).
		if hc.TunnelID != "" {
			var err error
			records, err = app.reconcileTunnelHost(ctx, cli, zone, zoneID, hc, name, key, records, managed, tag)
			if err != nil {
				app.logger.Error("reconcile tunnel host failed",
					zap.String("host", hc.Host), zap.String("zone", zone), zap.Error(err))
			}
			continue
		}

		// A normal host manages address records; a leftover owned CNAME from a
		// previous tunnel declaration at the same name must be cleared first,
		// because Cloudflare forbids a CNAME coexisting with A/AAAA. Without
		// this, revert tunnel -> IP fails in the same reload (creation is
		// rejected with code 81054) and prune only removes the CNAME
		// afterwards, leaving the name with no record.
		var clearErr error
		var clearedCNAME bool
		var clearedTunnelID string
		records, clearedCNAME, clearedTunnelID, clearErr = app.clearConflictingRecords(ctx, cli, hc, zone, zoneID, name, key,
			"address records", map[string]bool{"CNAME": true}, records, tag)
		if clearErr != nil {
			app.logger.Error("reconcile host failed",
				zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", "CNAME"), zap.Error(clearErr))
			continue
		}
		// Reverting a host from tunnel-backed to an address host deletes the
		// CNAME here rather than in the prune pass. The route it served is now
		// unreachable, so report the name the same way prune does and let the
		// ingress phase clean it up. The tunnel comes from the deleted record's
		// target: this host is an address host now, so it carries no TunnelID.
		if clearedCNAME {
			clearedNames = append(clearedNames, prunedName{Host: hc.Host, TunnelID: clearedTunnelID})
		}

		// A is always managed for a declared host.
		markManaged(managed, key, "A")
		ipv4, ipv4OK := resolveFamilyIP(hc.IP, det.ipv4, det.ipv4Failed)
		if err := app.reconcileFamily(ctx, cli, zone, zoneID, hc, name, "A", ipv4, ipv4OK, byName[key]["A"], tag, effectiveProxied(hc, ipv4)); err != nil {
			app.logger.Error("reconcile host failed",
				zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", "A"), zap.Error(err))
		}

		// AAAA is managed only when IPv6 is enabled in config.
		if hc.IP6 != "" {
			markManaged(managed, key, "AAAA")
			literal := ""
			if hc.IP6 != IP6Auto {
				literal = hc.IP6
			}
			ipv6, ipv6OK := resolveFamilyIP(literal, det.ipv6, det.ipv6Failed)
			if ipv4OK && ipv6OK && isPrivateIP(ipv4) != isPrivateIP(ipv6) {
				app.logger.Warn("mixed address families: proxied state differs per record",
					zap.String("host", hc.Host), zap.String("zone", zone),
					zap.String("ipv4", ipv4), zap.Bool("ipv4_proxied", !isPrivateIP(ipv4)),
					zap.String("ipv6", ipv6), zap.Bool("ipv6_proxied", !isPrivateIP(ipv6)))
			}
			if err := app.reconcileFamily(ctx, cli, zone, zoneID, hc, name, "AAAA", ipv6, ipv6OK, byName[key]["AAAA"], tag, effectiveProxied(hc, ipv6)); err != nil {
				app.logger.Error("reconcile host failed",
					zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", "AAAA"), zap.Error(err))
			}
		}
	}

	// Names whose CNAME was deleted to make room for address records are only
	// reported when the zone opted into prune. Deleting the *record* is not
	// optional — Cloudflare forbids a CNAME coexisting with A/AAAA, so a revert
	// must remove it — but deleting the *route* is a cleanup decision, and
	// `prune` is the operator's single switch for "this plugin may delete my
	// routes". Without it the route is left in place and goes stale, which is
	// documented in troubleshooting.
	var prunedNames []prunedName
	if app.zonePruneEnabled(zone) {
		names, err := app.pruneZone(ctx, cli, zone, zoneID, records, managed, tag)
		// Report whatever was pruned before a failure, plus the cleared names,
		// so the ingress phase still cleans up routes whose records are gone.
		prunedNames = append(names, clearedNames...)
		if err != nil {
			return reconcileZoneResult{accountID: accountID, prunedHosts: prunedNames},
				fmt.Errorf("zone %q: prune: %v", zone, err)
		}
	}
	return reconcileZoneResult{accountID: accountID, prunedHosts: prunedNames}, nil
}

// markManaged records that the declared config asks the plugin to manage a
// (name, record type) pair.
func markManaged(managed map[string]map[string]bool, key, recType string) {
	if managed[key] == nil {
		managed[key] = make(map[string]bool)
	}
	managed[key][recType] = true
}

// reconcileTunnelHost reconciles a Cloudflare Tunnel host: it clears any
// A/AAAA records at the name (Cloudflare forbids a CNAME coexisting with
// address records) and reconciles a single proxied CNAME to
// <tunnel-id>.cfargotunnel.com. It returns the records snapshot with deleted
// entries removed so a later prune does not retry those deletions.
func (app *App) reconcileTunnelHost(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	hc HostConfig,
	name, key string,
	records []cfDNSRecord,
	managed map[string]map[string]bool,
	tag string,
) ([]cfDNSRecord, error) {
	markManaged(managed, key, "CNAME")

	// Clear address records that would block CNAME creation. Owned records are
	// deleted unconditionally; untagged ones require force_adopt (adoption of
	// the name), otherwise the CNAME cannot be created and the user is told.
	// Neither the CNAME flag nor the tunnel id can be set here: CNAME is not in
	// the blocking set, so nothing a tunnel route could be attributed to is
	// deleted.
	kept, _, _, err := app.clearConflictingRecords(ctx, cli, hc, zone, zoneID, name, key,
		"tunnel CNAME", map[string]bool{"A": true, "AAAA": true}, records, tag)
	if err != nil {
		return kept, err
	}

	target := hc.TunnelID + ".cfargotunnel.com"
	// Always proxied: a CNAME to cfargotunnel.com is not resolvable DNS-only.
	// effectiveProxied must NOT be applied here (it would classify the
	// hostname content as a private IP and force proxied=false).
	var existing []cfDNSRecord
	for i := range kept {
		if kept[i].Type == "CNAME" && canonicalNameKey(kept[i].Name, zone) == key {
			existing = append(existing, kept[i])
		}
	}
	if err := app.reconcileFamily(ctx, cli, zone, zoneID, hc, name, "CNAME", target, true, existing, tag, true); err != nil {
		return kept, err
	}
	return kept, nil
}

// cfargotunnelSuffix is the domain every tunnel CNAME target lives under. A
// CNAME whose target ends in it names a tunnel; anything else does not.
const cfargotunnelSuffix = ".cfargotunnel.com"

// prunedName is a hostname whose DNS record this run deleted, together with the
// tunnel that record pointed at. TunnelID is empty when the deleted record
// named no tunnel: an address record, or a CNAME pointing somewhere other than
// cfargotunnel.com. Such a name cannot be attributed to a tunnel, so no route
// of it can be cleaned up.
type prunedName struct {
	Host     string
	TunnelID string
}

// tunnelIDFromCNAMETarget extracts the tunnel UUID from a CNAME target of the
// form <uuid>.cfargotunnel.com, returning empty for any other target.
//
// This is the only way the ingress phase can learn which tunnel a hostname
// belonged to once no host declares it: a tunnel-to-address revert leaves the
// host config with no TunnelID at all (that is what makes it a revert), so the
// record being deleted is the sole surviving link. Registration in the global
// block is a separate question — it authorises the write, it does not identify
// the tunnel — and both are checked before anything is written.
func tunnelIDFromCNAMETarget(target string) string {
	target = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	if !strings.HasSuffix(target, cfargotunnelSuffix) {
		return ""
	}
	label := strings.TrimSuffix(target, cfargotunnelSuffix)
	// The UUID must be the whole label: a deeper name such as
	// "x.y.cfargotunnel.com" is not a tunnel target.
	if strings.Contains(label, ".") || !isUUID(label) {
		return ""
	}
	return label
}

// clearConflictingRecords deletes records at a host's name whose type is in
// blockingTypes, which would otherwise make the desired record type
// uncreatable (Cloudflare forbids a CNAME coexisting with A/AAAA). Owned
// records are deleted unconditionally; untagged ones are only deleted when
// force_adopt is set, otherwise an error is returned (and the named records
// are left untouched). desiredType only labels log messages. It returns the
// records snapshot with deleted entries removed (so a later prune does not
// retry those deletions), whether it deleted a CNAME at that name, and the
// tunnel UUID that CNAME pointed at (empty when it pointed at no tunnel).
//
// The CNAME flag is what tells the ingress phase that the route for this
// hostname can no longer receive traffic: switching a host from tunnel to
// address records removes the CNAME here, not in the prune pass, so the
// correlation has to travel back the same way the pruned-name set does. The
// UUID travels with it because at this point the host's TunnelID is empty by
// definition, so nothing else can say which tunnel the route belongs to.
func (app *App) clearConflictingRecords(
	ctx context.Context,
	cli *cloudflareClient,
	hc HostConfig,
	zone, zoneID, name, key string,
	desiredType string,
	blockingTypes map[string]bool,
	records []cfDNSRecord,
	tag string,
) (kept []cfDNSRecord, deletedCNAME bool, cnameTunnelID string, err error) {
	log := app.logger.With(zap.String("host", hc.Host), zap.String("zone", zone))

	kept = make([]cfDNSRecord, 0, len(records))
	var blocked error
	for i := range records {
		r := &records[i]
		if !blockingTypes[r.Type] || canonicalNameKey(r.Name, zone) != key {
			kept = append(kept, *r)
			continue
		}
		owned := isOwnedByInstance(r.Comment, tag)
		if !owned && !hc.ForceAdopt {
			log.Error("host name has an untagged record that blocks the desired type; a CNAME cannot coexist with A/AAAA",
				zap.String("record_id", r.ID), zap.String("record_type", r.Type),
				zap.String("content", r.Content), zap.String("desired", desiredType),
				zap.String("remedy", "remove the record or add force_adopt"))
			blocked = fmt.Errorf("untagged %s record %s at %s blocks %s", r.Type, r.ID, name, desiredType)
			kept = append(kept, *r)
			continue
		}
		if err := cli.deleteRecord(ctx, zoneID, r.ID); err != nil {
			log.Error("could not delete conflicting record",
				zap.String("record_id", r.ID), zap.String("record_type", r.Type),
				zap.String("desired", desiredType), zap.Error(err))
			blocked = fmt.Errorf("deleting %s record %s at %s: %w", r.Type, r.ID, name, err)
			kept = append(kept, *r)
			continue
		}
		if r.Type == "CNAME" {
			deletedCNAME = true
			// Remember which tunnel this CNAME pointed at, so a revert can
			// still name the tunnel whose route just became unreachable.
			if id := tunnelIDFromCNAMETarget(r.Content); id != "" {
				cnameTunnelID = id
			}
		}
		log.Info("deleted conflicting record to make room for the desired type",
			zap.String("record_id", r.ID), zap.String("record_type", r.Type),
			zap.String("content", r.Content), zap.String("desired", desiredType),
			zap.String("fqdn", hc.Host))
	}
	return kept, deletedCNAME, cnameTunnelID, blocked
}

// reconcileFamily handles a single (host, record type) pair against its
// existing records of that type. available reports whether an effective value
// was resolved; when false the family is skipped with a warning and existing
// records are left unchanged. proxied is the desired proxy mode, computed by
// the caller: address records use effectiveProxied, tunnel CNAMEs always true.
func (app *App) reconcileFamily(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	hc HostConfig,
	name, recType, ip string,
	available bool,
	existing []cfDNSRecord,
	tag string,
	proxied bool,
) error {
	log := app.logger.With(zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", recType))

	if !available {
		if len(existing) == 0 {
			log.Warn("public IP unavailable and host has no existing record; skipping creation")
		} else {
			log.Warn("public IP unavailable; leaving existing record unchanged")
		}
		return nil
	}
	if ip == "" {
		log.Warn("no effective IP for host; skipping")
		return nil
	}

	// Find an existing record of this type owned by this instance for this
	// name. Matching is strictly per type: an untagged record of the other
	// family is never adopted here.
	var owned *cfDNSRecord
	var untagged *cfDNSRecord
	for i := range existing {
		r := &existing[i]
		if isOwnedByInstance(r.Comment, tag) {
			owned = r
			break
		}
		if untagged == nil {
			untagged = r
		}
	}

	desired := cfDNSRecord{
		Type:    recType,
		Name:    name,
		Content: ip,
		TTL:     1, // 1 = Auto
		Proxied: proxied,
		Comment: tag,
	}

	switch {
	case owned != nil:
		// Update only on drift.
		if owned.Content != ip || owned.Proxied != proxied {
			oldContent, oldProxied := owned.Content, owned.Proxied
			owned.Content = ip
			owned.Proxied = proxied
			owned.Comment = tag
			if err := cli.updateRecord(ctx, zoneID, owned.ID, *owned); err != nil {
				return fmt.Errorf("updating record for %s: %v", hc.Host, err)
			}
			log.Info("updated record",
				zap.String("fqdn", hc.Host),
				zap.String("name", name),
				zap.String("old_content", oldContent),
				zap.String("content", ip),
				zap.Bool("old_proxied", oldProxied),
				zap.Bool("proxied", proxied))
		} else {
			log.Debug("record already in sync", zap.String("fqdn", hc.Host), zap.String("name", name), zap.String("content", ip), zap.Bool("proxied", proxied))
		}

	case untagged != nil:
		if !hc.ForceAdopt {
			log.Info("existing untagged record not owned by this instance; leaving unchanged (add force_adopt to adopt)",
				zap.String("fqdn", hc.Host),
				zap.String("name", name),
				zap.String("content", untagged.Content))
			return nil
		}
		// Force adopt: overwrite content/proxy mode and claim ownership.
		oldContent := untagged.Content
		oldProxied := untagged.Proxied
		untagged.Content = ip
		untagged.Proxied = proxied
		untagged.Comment = tag
		if err := cli.updateRecord(ctx, zoneID, untagged.ID, *untagged); err != nil {
			return fmt.Errorf("force-adopting record for %s: %v", hc.Host, err)
		}
		log.Info("force-adopted existing untagged record",
			zap.String("fqdn", hc.Host),
			zap.String("name", name),
			zap.String("record_id", untagged.ID),
			zap.String("old_content", oldContent),
			zap.String("content", ip),
			zap.Bool("old_proxied", oldProxied),
			zap.Bool("proxied", proxied))

	default:
		// No existing record: create.
		if err := cli.createRecord(ctx, zoneID, desired); err != nil {
			return fmt.Errorf("creating record for %s: %v", hc.Host, err)
		}
		log.Info("created record",
			zap.String("fqdn", hc.Host),
			zap.String("name", name),
			zap.String("content", ip),
			zap.Bool("proxied", proxied))
	}
	return nil
}

// resolveFamilyIP returns the effective IP for one address family and whether
// it is available. A literal override always wins; otherwise the detected
// value is used when detection succeeded.
func resolveFamilyIP(literal, detected string, detectionFailed bool) (string, bool) {
	if literal != "" {
		return literal, true
	}
	if detectionFailed || detected == "" {
		return "", false
	}
	return detected, true
}

// pruneZone deletes records tagged with this instance whose (name, type) is
// not managed by the declared configuration. It returns the hostnames it
// deleted together with the tunnel each deleted record pointed at (empty for
// records pointing at no tunnel), so the tunnel ingress pass can delete the
// matching route: a route whose DNS record this plugin just removed can no
// longer receive traffic, and that makes it safe to delete without needing an
// ownership marker on the route itself.
func (app *App) pruneZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	records []cfDNSRecord,
	managed map[string]map[string]bool,
	tag string,
) ([]prunedName, error) {
	var deleted []prunedName
	for i := range records {
		r := &records[i]
		if !isOwnedByInstance(r.Comment, tag) {
			continue
		}
		key := canonicalNameKey(r.Name, zone)
		if managed[key][r.Type] {
			continue
		}
		if err := cli.deleteRecord(ctx, zoneID, r.ID); err != nil {
			return deleted, fmt.Errorf("deleting orphan %s (%s): %v", r.Name, r.ID, err)
		}
		deleted = append(deleted, prunedName{
			Host:     fqdn(r.Name, zone),
			TunnelID: tunnelIDFromCNAMETarget(r.Content),
		})
		app.logger.Info("pruned orphan record",
			zap.String("fqdn", fqdn(r.Name, zone)),
			zap.String("name", key),
			zap.String("record_type", r.Type),
			zap.String("zone", zone),
			zap.String("content", r.Content),
			zap.String("record_id", r.ID))
	}
	return deleted, nil
}

// fqdn returns the fully-qualified form of a record name relative to zone.
// A name that is already fully-qualified (or the zone apex) passes through.
func fqdn(name, zone string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	if name == "" || name == "@" {
		return zone
	}
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}

// effectiveProxied returns whether a record should be proxied, forcing
// DNS-only for private/reserved IPs regardless of the declared proxied value.
func effectiveProxied(hc HostConfig, ip string) bool {
	proxied := true
	if hc.Proxied != nil {
		proxied = *hc.Proxied
	}
	if isPrivateIP(ip) {
		return false
	}
	return proxied
}

// isPrivateIP reports whether ip is in a private or reserved range that
// Cloudflare cannot proxy. IPv4: private, loopback, link-local, CGNAT.
// IPv6: ULA (fc00::/7), link-local (fe80::/10), loopback, multicast. A public
// IPv6 is proxyable even though it is not IPv4.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	if v4 := parsed.To4(); v4 != nil {
		return parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() ||
			parsed.IsLinkLocalMulticast() || isCGNAT(v4)
	}
	if parsed.IsLoopback() || parsed.IsLinkLocalUnicast() ||
		parsed.IsLinkLocalMulticast() || parsed.IsMulticast() || isULA(parsed) {
		return true
	}
	return false
}

// isULA reports whether ip is in fc00::/7 (unique local addresses), which
// includes the Tailscale IPv6 range fd7a:115c:a1e0::/48.
func isULA(ip net.IP) bool {
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}

// isCGNAT reports whether ip is in 100.64.0.0/10 (RFC 6598; Tailscale range).
func isCGNAT(ip4 net.IP) bool {
	if ip4 == nil {
		return false
	}
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// ownershipTag returns the ownership comment value: "<tag_prefix>:<instance>".
func (app *App) ownershipTag() string {
	return app.TagPrefix + ":" + app.Instance
}

func (app *App) effectiveIPURL() string {
	if app.IPURL != "" {
		return app.IPURL
	}
	return DefaultIPURL
}

func (app *App) effectiveIP6URL() string {
	if app.IP6URL != "" {
		return app.IP6URL
	}
	return DefaultIP6URL
}

func (app *App) zonePruneEnabled(zone string) bool {
	for _, z := range app.Zones {
		if strings.EqualFold(z.Zone, zone) {
			return z.Prune
		}
	}
	return false
}

// isOwnedByInstance reports whether the record comment equals this instance's
// ownership tag exactly. Records with the same prefix but a different instance
// are treated as not owned (never modified or deleted).
func isOwnedByInstance(comment, ownTag string) bool {
	return comment == ownTag
}

// canonicalNameKey normalizes a record name to its zone-relative form used as
// the reconciliation map key. Cloudflare returns record names as FQDNs (e.g.
// "foo.example.com", or the zone apex "example.com"), while the plugin computes
// relative names ("foo", "@"); both must map to the same key. Names that are
// already relative (not ending in the zone suffix) pass through unchanged.
func canonicalNameKey(name, zone string) string {
	if name == "" {
		return "@"
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	if name == zone {
		return "@"
	}
	if strings.HasSuffix(name, "."+zone) {
		rel := strings.TrimSuffix(name[:len(name)-len(zone)], ".")
		if rel == "" {
			return "@"
		}
		return rel
	}
	return name
}
