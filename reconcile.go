package cfdnsmanager

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
			if err := app.reconcileZone(ctx, cli, zone, zoneHosts, det); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}(zkey, cli, zoneHosts)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("%d zone(s) failed: %v", len(errs), errs)
	}
	return nil
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

// reconcileZone reconciles all hosts in one zone, then prunes if enabled.
func (app *App) reconcileZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone string,
	hosts []HostConfig,
	det familyDetection,
) error {
	zoneID, err := cli.zoneIDByName(ctx, zone)
	if err != nil {
		return fmt.Errorf("zone %q: %v", zone, err)
	}

	records, err := cli.listRecords(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("zone %q: listing records: %v", zone, err)
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
	markManaged := func(key, recType string) {
		if managed[key] == nil {
			managed[key] = make(map[string]bool)
		}
		managed[key][recType] = true
	}

	tag := app.ownershipTag()

	for _, hc := range hosts {
		name := recordName(hc.Host, zone)
		key := canonicalNameKey(name, zone)

		// A is always managed for a declared host.
		markManaged(key, "A")
		ipv4, ipv4OK := resolveFamilyIP(hc.IP, det.ipv4, det.ipv4Failed)
		if err := app.reconcileFamily(ctx, cli, zone, zoneID, hc, name, "A", ipv4, ipv4OK, byName[key]["A"], tag); err != nil {
			app.logger.Error("reconcile host failed",
				zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", "A"), zap.Error(err))
		}

		// AAAA is managed only when IPv6 is enabled in config.
		if hc.IP6 != "" {
			markManaged(key, "AAAA")
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
			if err := app.reconcileFamily(ctx, cli, zone, zoneID, hc, name, "AAAA", ipv6, ipv6OK, byName[key]["AAAA"], tag); err != nil {
				app.logger.Error("reconcile host failed",
					zap.String("host", hc.Host), zap.String("zone", zone), zap.String("record_type", "AAAA"), zap.Error(err))
			}
		}
	}

	if app.zonePruneEnabled(zone) {
		if err := app.pruneZone(ctx, cli, zone, zoneID, records, managed, tag); err != nil {
			return fmt.Errorf("zone %q: prune: %v", zone, err)
		}
	}
	return nil
}

// reconcileFamily handles a single (host, record type) pair against its
// existing records of that type. available reports whether an effective IP was
// resolved; when false the family is skipped with a warning and existing
// records are left unchanged.
func (app *App) reconcileFamily(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	hc HostConfig,
	name, recType, ip string,
	available bool,
	existing []cfDNSRecord,
	tag string,
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

	proxied := effectiveProxied(hc, ip)

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
// not managed by the declared configuration.
func (app *App) pruneZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	records []cfDNSRecord,
	managed map[string]map[string]bool,
	tag string,
) error {
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
			return fmt.Errorf("deleting orphan %s (%s): %v", r.Name, r.ID, err)
		}
		app.logger.Info("pruned orphan record",
			zap.String("fqdn", fqdn(r.Name, zone)),
			zap.String("name", key),
			zap.String("record_type", r.Type),
			zap.String("zone", zone),
			zap.String("content", r.Content),
			zap.String("record_id", r.ID))
	}
	return nil
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
