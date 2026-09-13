package cfdnsmanager

import (
	"fmt"
	"sort"
	"strings"
)

// addHost records a per-site host declaration into the app (called from the
// no-op handler's Provision). It is safe for concurrent calls because Caddy
// may provision handlers across goroutines.
func (app *App) addHost(hc HostConfig) {
	app.hostsMu.Lock()
	defer app.hostsMu.Unlock()
	app.hosts = append(app.hosts, hc)
}

// hostsSnapshot returns a copy of the accumulated host declarations.
func (app *App) hostsSnapshot() []HostConfig {
	app.hostsMu.Lock()
	defer app.hostsMu.Unlock()
	out := make([]HostConfig, len(app.hosts))
	copy(out, app.hosts)
	return out
}

// assignZone determines the managed zone for a host from the declared zones
// and fills in ZoneConfig on the HostConfig. Zone assignment uses the longest
// matching declared zone suffix. A host that is not under any declared zone is
// an error (adapt-time, per spec).
func assignZone(zones []ZoneConfig, hc *HostConfig) error {
	// Sort candidate zones longest-first so the first suffix match wins.
	byLen := make([]ZoneConfig, len(zones))
	copy(byLen, zones)
	sort.SliceStable(byLen, func(i, j int) bool {
		return len(byLen[i].Zone) > len(byLen[j].Zone)
	})
	host := strings.ToLower(hc.Host)
	for _, z := range byLen {
		zone := strings.ToLower(z.Zone)
		if host == zone || strings.HasSuffix(host, "."+zone) {
			hc.ZoneConfig = z
			return nil
		}
	}
	return fmt.Errorf("host %q is not under any declared cf_dns_manager zone; add a `zone <zone> api_token <token>` entry to the global cf_dns_manager block", hc.Host)
}

// recordName returns the DNS record name relative to the host's zone:
// apex hosts map to the zone root ("@"), subdomains to their relative label.
func recordName(host, zone string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	if host == zone {
		return "@"
	}
	// host is guaranteed to end with "." + zone.
	rel := strings.TrimSuffix(host[:len(host)-len(zone)], ".")
	if rel == "" {
		return "@"
	}
	return rel
}
