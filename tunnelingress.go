package cfdnsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// defaultTunnelService is the catch-all destination used when the config does
// not declare tunnel_default_service. It fails closed: the plugin cannot know
// the origin's service URL, and guessing one (e.g. https://caddy:443) would
// republish every hostname resolving through the tunnel, including hostnames
// the user never declared.
const defaultTunnelService = "http_status:404"

// normalizeDefaultService returns the configured default, or the fail-closed
// default when unset.
func normalizeDefaultService(configured string) string {
	if configured == "" {
		return defaultTunnelService
	}
	return configured
}

// deriveIngressPlan groups the tunnel-backed hosts by tunnel UUID and derives
// the ingress plan for each. Rules are ordered deterministically: declared
// hostnames sorted by FQDN, then the default catch-all last.
//
// Sorting is required, not cosmetic. hosts accumulates from handler Provision
// calls that Caddy may run concurrently, so the slice order is not stable
// between reloads; without sorting, an unchanged config would produce a
// reordered plan and the drift check would report a change on every reload.
//
// Every rule carries the instance ownership tag in its description, the
// ingress counterpart of the comment the plugin writes on DNS records. The tag
// is written but not yet acted on: no rule is ever deleted, because a rule's
// description cannot be trusted as proof of authorship when a user can edit it.
func deriveIngressPlan(hosts []HostConfig, defaultService, tag string) map[string][]cfIngressRule {
	def := normalizeDefaultService(defaultService)

	byTunnel := make(map[string][]HostConfig)
	for _, hc := range hosts {
		if hc.TunnelID == "" {
			continue
		}
		byTunnel[hc.TunnelID] = append(byTunnel[hc.TunnelID], hc)
	}

	plans := make(map[string][]cfIngressRule, len(byTunnel))
	for tunnelID, tunnelHosts := range byTunnel {
		sorted := make([]HostConfig, len(tunnelHosts))
		copy(sorted, tunnelHosts)
		sort.SliceStable(sorted, func(i, j int) bool {
			return strings.ToLower(sorted[i].Host) < strings.ToLower(sorted[j].Host)
		})

		rules := make([]cfIngressRule, 0, len(sorted)+1)
		for _, hc := range sorted {
			service := hc.TunnelService
			if service == "" {
				service = def
			}
			rules = append(rules, cfIngressRule{
				Hostname:    strings.ToLower(hc.Host),
				Service:     service,
				Description: tag,
			})
		}
		// The default rule has no hostname, which is what makes it match all
		// traffic. It is always present: the API requires a terminating
		// catch-all, and it is also the "default route" the config asks for.
		rules = append(rules, cfIngressRule{Service: def, Description: tag})
		plans[tunnelID] = rules
	}
	return plans
}

// mergeIngressPlan combines the current rules with the derived plan.
//
// Foreign rules — those whose hostname the config does not declare — are
// preserved in their existing relative order, because the plugin cannot prove
// it authored them and destroying them would be worse than leaving them. Rules
// whose hostname is declared are replaced by the derived rule. Any existing
// catch-all is dropped and re-emitted from the plan, so it is never duplicated.
//
// It reports whether the write is needed and which preserved rules shadow a
// declared host. A wildcard foreign rule can match a hostname the config also
// declares; since rules match top to bottom, the effective destination then
// depends on ordering the plugin does not own, so callers warn about it.
func mergeIngressPlan(current, plan []cfIngressRule) (merged []cfIngressRule, changed bool, shadowed []string) {
	declared := make(map[string]bool, len(plan))
	for _, r := range plan {
		if r.Hostname != "" {
			declared[strings.ToLower(r.Hostname)] = true
		}
	}

	var preserved []cfIngressRule
	for _, r := range current {
		if r.Hostname == "" {
			continue // the catch-all is always re-emitted from the plan
		}
		if declared[strings.ToLower(r.Hostname)] {
			continue // replaced by the derived rule
		}
		preserved = append(preserved, r)

		if strings.Contains(r.Hostname, "*") {
			for host := range declared {
				if wildcardMatches(r.Hostname, host) {
					shadowed = append(shadowed, r.Hostname+" shadows "+host)
				}
			}
		}
	}
	sort.Strings(shadowed)

	merged = make([]cfIngressRule, 0, len(preserved)+len(plan))
	merged = append(merged, preserved...)
	merged = append(merged, plan...)

	// Replacing a rule's service must not erase metadata the plugin does not
	// manage. Descriptions, paths and origin requests are only carried over,
	// never authored, so this is not an ownership claim: it is the same
	// "don't destroy what you don't model" rule the raw-JSON fields follow.
	inheritUnmanagedFields(merged, current)

	return merged, !ingressRulesEqual(current, merged), shadowed
}

// inheritUnmanagedFields copies the fields the plugin does not author — path
// and originRequest — from the existing rule at the same hostname (the empty
// hostname matching the catch-all). Description is deliberately NOT inherited:
// the plugin authors it, so it comes from the derived plan.
//
// Without this, a declared host's rule is regenerated from the derived plan
// and anything the operator set in the dashboard on that rule is lost. An
// originRequest such as matchSNItoHost is the sharpest example: dropping it
// makes cloudflared send the service URL's hostname as SNI, which Caddy
// refuses for a wildcard-certificate site, turning every request into a 502.
//
// A value already present on the derived rule wins, and nothing is ever
// cleared: a rule with no counterpart keeps its empty fields.
func inheritUnmanagedFields(merged, current []cfIngressRule) {
	type unmanaged struct {
		path          json.RawMessage
		originRequest json.RawMessage
	}
	byHost := make(map[string]unmanaged, len(current))
	for _, r := range current {
		if len(r.Path) == 0 && len(r.OriginRequest) == 0 {
			continue
		}
		byHost[strings.ToLower(r.Hostname)] = unmanaged{
			path:          r.Path,
			originRequest: r.OriginRequest,
		}
	}
	for i := range merged {
		prev, ok := byHost[strings.ToLower(merged[i].Hostname)]
		if !ok {
			continue
		}
		if len(merged[i].Path) == 0 {
			merged[i].Path = prev.path
		}
		if len(merged[i].OriginRequest) == 0 {
			merged[i].OriginRequest = prev.originRequest
		}
	}
}

// wildcardMatches reports whether a wildcard hostname pattern (only "*." is
// supported by cloudflared) matches a concrete hostname. A wildcard matches at
// any depth, so "*.example.com" matches "a.b.example.com" too.
func wildcardMatches(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}
	suffix := pattern[1:] // ".example.com"
	return strings.HasSuffix(host, suffix)
}

// ingressRulesEqual reports whether two rule slices are equivalent on the
// fields the plugin manages: hostname, service and the ownership description.
//
// Comparing only managed fields is deliberate. Cloudflare normalises what it
// stores (it may add an empty originRequest object and it bumps version), so a
// deep equality check against the server's rendering would report drift on
// every run and issue a write on every reload — an idempotence bug that a
// mock returning a fixed document would never reveal. Path and originRequest
// are therefore excluded, while description is included because the plugin
// authors it and must correct a rule that lost or changed its tag.
func ingressRulesEqual(a, b []cfIngressRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i].Hostname, b[i].Hostname) {
			return false
		}
		if a[i].Service != b[i].Service {
			return false
		}
		if a[i].Description != b[i].Description {
			return false
		}
	}
	return true
}

// reconcileTunnelIngress derives and writes the ingress plan for every tunnel
// declared by the given hosts. It is a separate phase from DNS reconciliation:
// a tunnel's hosts can span zones, so grouping by zone would fragment a single
// tunnel's plan into competing writes.
//
// Failures are returned rather than fatal, so a Tunnel API problem never
// prevents DNS reconciliation (and vice versa). A partial failure leaves DNS
// correct and is retried on the next config load.
func reconcileTunnelIngress(ctx context.Context, cli *tunnelClient, hosts []HostConfig, defaultService, tag string, logger *zap.Logger) error {
	plans := deriveIngressPlan(hosts, defaultService, tag)
	if len(plans) == 0 {
		return nil
	}

	// Deterministic order so logs and error aggregation are stable.
	tunnelIDs := make([]string, 0, len(plans))
	for id := range plans {
		tunnelIDs = append(tunnelIDs, id)
	}
	sort.Strings(tunnelIDs)

	var (
		wg    sync.WaitGroup
		errMu sync.Mutex
		errs  []error
	)

	for _, tunnelID := range tunnelIDs {
		plan := plans[tunnelID]
		wg.Add(1)
		go func(tunnelID string, plan []cfIngressRule) {
			defer wg.Done()
			if err := reconcileOneTunnel(ctx, cli, tunnelID, plan, defaultService, logger); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}(tunnelID, plan)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("%d tunnel(s) failed: %v", len(errs), errs)
	}
	return nil
}

// reconcileOneTunnel reads one tunnel's configuration, merges the derived plan
// and writes it back only when something changed.
func reconcileOneTunnel(ctx context.Context, cli *tunnelClient, tunnelID string, plan []cfIngressRule, defaultService string, logger *zap.Logger) error {
	log := logger.With(zap.String("tunnel_id", tunnelID))

	current, err := cli.getConfiguration(ctx, tunnelID)
	if err != nil {
		return fmt.Errorf("tunnel %s: reading configuration: %v", tunnelID, err)
	}

	// A locally-managed tunnel keeps its ingress on the host running
	// cloudflared. Writing through the API would report success and change
	// nothing, which is the worst possible outcome, so refuse explicitly.
	if current.Source == "local" {
		log.Error("refusing to manage tunnel ingress: it is locally managed",
			zap.String("source", "local"),
			zap.String("remedy", "recreate the tunnel as remotely-managed, or stop declaring its hosts with `tunnel <uuid>`"))
		return fmt.Errorf("tunnel %s: ingress is locally managed (source: local); "+
			"recreate the tunnel as remotely-managed, or remove its hosts from the plugin", tunnelID)
	}

	merged, changed, shadowed := mergeIngressPlan(current.Config.Ingress, plan)
	for _, s := range shadowed {
		log.Warn("preserved wildcard ingress rule shadows a declared host; the effective destination depends on rule order",
			zap.String("shadowing", s))
	}

	if !changed {
		log.Debug("tunnel ingress already in sync", zap.Int("rules", len(merged)))
		return nil
	}

	cfg := *current.Config
	cfg.Ingress = merged
	if err := cli.putConfiguration(ctx, tunnelID, cfg); err != nil {
		return fmt.Errorf("tunnel %s: writing configuration: %v", tunnelID, err)
	}

	origin := "configured default"
	if defaultService == "" {
		origin = "fail-closed default"
	}
	log.Info("wrote tunnel ingress plan",
		zap.Int("rules", len(merged)),
		zap.Int("preserved_foreign_rules", len(merged)-len(plan)),
		zap.String("catch_all_origin", origin))
	return nil
}
