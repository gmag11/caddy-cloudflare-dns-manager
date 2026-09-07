package cf_dns_manager

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
// once per config load from Start(). It performs a single public-IP detection
// shared by all auto hosts, reconciles each host's A record, then runs per-zone
// prune for zones that opted in.
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

	// Detect public IP once for all auto hosts. Failure is non-blocking.
	var publicIP string
	var ipDetectionFailed bool
	hasAutoHosts := false
	for _, hc := range hosts {
		if hc.IP == "" {
			hasAutoHosts = true
			break
		}
	}
	if hasAutoHosts {
		ip, err := detectPublicIPv4(ctx, &http.Client{Timeout: httpTimeout}, app.effectiveIPURL())
		if err != nil {
			ipDetectionFailed = true
			app.logger.Warn("could not detect public IPv4; leaving existing auto-IP records unchanged and skipping new auto-IP records",
				zap.Error(err))
		} else {
			publicIP = ip
		}
	}

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
			if err := app.reconcileZone(ctx, cli, zone, zoneHosts, publicIP, ipDetectionFailed); err != nil {
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

// reconcileZone reconciles all hosts in one zone, then prunes if enabled.
func (app *App) reconcileZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone string,
	hosts []HostConfig,
	publicIP string,
	ipDetectionFailed bool,
) error {
	zoneID, err := cli.zoneIDByName(ctx, zone)
	if err != nil {
		return fmt.Errorf("zone %q: %v", zone, err)
	}

	records, err := cli.listRecords(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("zone %q: listing records: %v", zone, err)
	}

	// Canonical map from record name ("@", "foo", "a.b") to A records.
	byName := make(map[string][]cfDNSRecord)
	for _, r := range records {
		key := canonicalNameKey(r.Name)
		byName[key] = append(byName[key], r)
	}

	reconciled := make(map[string]bool)
	tag := app.ownershipTag()

	for _, hc := range hosts {
		name := recordName(hc.Host, zone)
		existing := byName[canonicalNameKey(name)]
		if err := app.reconcileOne(ctx, cli, zone, zoneID, hc, name, existing, tag, publicIP, ipDetectionFailed); err != nil {
			app.logger.Error("reconcile host failed",
				zap.String("host", hc.Host), zap.String("zone", zone), zap.Error(err))
			continue
		}
		reconciled[canonicalNameKey(name)] = true
	}

	if app.zonePruneEnabled(zone) {
		if err := app.pruneZone(ctx, cli, zone, zoneID, records, reconciled, tag); err != nil {
			return fmt.Errorf("zone %q: prune: %v", zone, err)
		}
	}
	return nil
}

// reconcileOne handles a single host against its existing records.
func (app *App) reconcileOne(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	hc HostConfig,
	name string,
	existing []cfDNSRecord,
	tag string,
	publicIP string,
	ipDetectionFailed bool,
) error {
	log := app.logger.With(zap.String("host", hc.Host), zap.String("zone", zone))

	ip := hc.IP
	if ip == "" {
		if ipDetectionFailed {
			if len(existing) == 0 {
				log.Warn("public IPv4 unavailable and host has no existing record; skipping creation")
			} else {
				log.Warn("public IPv4 unavailable; leaving existing record unchanged")
			}
			return nil
		}
		ip = publicIP
	}
	if ip == "" {
		log.Warn("no effective IP for host; skipping")
		return nil
	}

	proxied := effectiveProxied(hc, ip)

	// Find an existing record owned by this instance for this name.
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
		Type:    "A",
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
			owned.Content = ip
			owned.Proxied = proxied
			owned.Comment = tag
			if err := cli.updateRecord(ctx, zoneID, owned.ID, *owned); err != nil {
				return fmt.Errorf("updating record for %s: %v", hc.Host, err)
			}
			log.Info("updated A record", zap.String("name", name), zap.String("content", ip), zap.Bool("proxied", proxied))
		} else {
			log.Debug("record already in sync")
		}

	case untagged != nil:
		if !hc.ForceAdopt {
			log.Info("existing untagged record not owned by this instance; leaving unchanged (add force_adopt to adopt)",
				zap.String("content", untagged.Content))
			return nil
		}
		// Force adopt: overwrite content/proxy mode and claim ownership.
		untagged.Content = ip
		untagged.Proxied = proxied
		untagged.Comment = tag
		if err := cli.updateRecord(ctx, zoneID, untagged.ID, *untagged); err != nil {
			return fmt.Errorf("force-adopting record for %s: %v", hc.Host, err)
		}
		log.Info("force-adopted existing untagged record", zap.String("name", name), zap.String("record_id", untagged.ID))

	default:
		// No existing record: create.
		if err := cli.createRecord(ctx, zoneID, desired); err != nil {
			return fmt.Errorf("creating record for %s: %v", hc.Host, err)
		}
		log.Info("created A record", zap.String("name", name), zap.String("content", ip), zap.Bool("proxied", proxied))
	}
	return nil
}

// pruneZone deletes A records tagged with this instance whose name is not in
// the reconciled set.
func (app *App) pruneZone(
	ctx context.Context,
	cli *cloudflareClient,
	zone, zoneID string,
	records []cfDNSRecord,
	reconciled map[string]bool,
	tag string,
) error {
	for i := range records {
		r := &records[i]
		if !isOwnedByInstance(r.Comment, tag) {
			continue
		}
		if reconciled[canonicalNameKey(r.Name)] {
			continue
		}
		if err := cli.deleteRecord(ctx, zoneID, r.ID); err != nil {
			return fmt.Errorf("deleting orphan %s (%s): %v", r.Name, r.ID, err)
		}
		app.logger.Info("pruned orphan record", zap.String("name", r.Name), zap.String("record_id", r.ID))
	}
	return nil
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
// Cloudflare cannot proxy.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return true
	}
	return parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() ||
		parsed.IsLinkLocalMulticast() || isCGNAT(parsed.To4())
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

func canonicalNameKey(name string) string {
	if name == "" {
		return "@"
	}
	return name
}
