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
func deriveIngressPlan(hosts []HostConfig, defaultService string) map[string][]cfIngressRule {
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
				Hostname: strings.ToLower(hc.Host),
				Service:  service,
			})
		}
		// The default rule has no hostname, which is what makes it match all
		// traffic. It is always present: the API requires a terminating
		// catch-all, and it is also the "default route" the config asks for.
		rules = append(rules, cfIngressRule{Service: def})
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
// A foreign rule is dropped only when its hostname is in prunedHosts, the set
// of names whose DNS records this run deleted. That is the one case where the
// route is provably unable to serve traffic, and it is the only deletion the
// plugin performs on routes it did not derive.
//
// It reports whether the write is needed, which rules were pruned, and which
// preserved rules shadow a declared host. A wildcard foreign rule can match a
// hostname the config also declares; since rules match top to bottom, the
// effective destination then depends on ordering the plugin does not own, so
// callers warn about it.
func mergeIngressPlan(current, plan []cfIngressRule, prunedHosts map[string]bool) (merged []cfIngressRule, changed bool, shadowed []string, pruned []string) {
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
		host := strings.ToLower(r.Hostname)
		if declared[host] {
			continue // replaced by the derived rule
		}
		if prunedHosts[host] {
			pruned = append(pruned, r.Hostname)
			continue
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
	sort.Strings(pruned)

	merged = make([]cfIngressRule, 0, len(preserved)+len(plan))
	merged = append(merged, preserved...)
	merged = append(merged, plan...)

	// Replacing a rule's service must not erase metadata the plugin does not
	// manage. Descriptions, paths and origin requests are only carried over,
	// never authored, so this is not an ownership claim: it is the same
	// "don't destroy what you don't model" rule the raw-JSON fields follow.
	inheritUnmanagedFields(merged, current)

	return merged, !ingressRulesEqual(current, merged), shadowed, pruned
}

// inheritUnmanagedFields copies the fields the plugin does not author —
// description, path and originRequest — from the existing rule at the same
// hostname (the empty hostname matching the catch-all).
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
		description   string
		path          json.RawMessage
		originRequest json.RawMessage
	}
	byHost := make(map[string]unmanaged, len(current))
	for _, r := range current {
		if r.Description == "" && len(r.Path) == 0 && len(r.OriginRequest) == 0 {
			continue
		}
		byHost[strings.ToLower(r.Hostname)] = unmanaged{
			description:   r.Description,
			path:          r.Path,
			originRequest: r.OriginRequest,
		}
	}
	for i := range merged {
		prev, ok := byHost[strings.ToLower(merged[i].Hostname)]
		if !ok {
			continue
		}
		if merged[i].Description == "" {
			merged[i].Description = prev.description
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
// fields the plugin manages: hostname and service.
//
// Comparing only managed fields is deliberate. Cloudflare normalises what it
// stores (it may add an empty originRequest object and it bumps version), so a
// deep equality check against the server's rendering would report drift on
// every run and issue a write on every reload — an idempotence bug that a
// mock returning a fixed document would never reveal. Description, path and
// originRequest are therefore all excluded.
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
	}
	return true
}

// reconcileTunnelIngress derives and writes the ingress plan for every tunnel
// declared by the given hosts, and deletes routes whose DNS records this run
// pruned. It is a separate phase from DNS reconciliation: a tunnel's hosts can
// span zones, so grouping by zone would fragment a single tunnel's plan into
// competing writes.
//
// It runs even with no declared tunnel hosts when prunedHosts is non-empty, so
// a tunnel that just lost its last declaration still gets cleaned up.
//
// Failures are returned rather than fatal, so a Tunnel API problem never
// prevents DNS reconciliation (and vice versa). A partial failure leaves DNS
// correct and is retried on the next config load.
func reconcileTunnelIngress(ctx context.Context, cli *tunnelClient, hosts []HostConfig, defaultService string, prunedHosts map[string]bool, logger *zap.Logger) error {
	plans := deriveIngressPlan(hosts, defaultService)

	// Tunnels that still need visiting: those with a derived plan, plus any
	// tunnel referenced by a pruned host that no longer appears in the plan.
	tunnelIDs := make([]string, 0, len(plans))
	for id := range plans {
		tunnelIDs = append(tunnelIDs, id)
	}
	sort.Strings(tunnelIDs)

	if len(tunnelIDs) == 0 {
		if len(prunedHosts) > 0 {
			// No tunnel is referenced by the config anymore, so the plugin does
			// not know which tunnel the orphaned route belongs to. Listing every
			// tunnel in the account to find it would mean rewriting tunnels the
			// operator never declared to this plugin, which is a far larger
			// blast radius than the cleanup is worth. Report and stop.
			logger.Info("DNS records were pruned but no tunnel is declared, so their routes cannot be identified; remove them manually in the dashboard",
				zap.Int("pruned_names", len(prunedHosts)))
		}
		return nil
	}

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
			if err := reconcileOneTunnel(ctx, cli, tunnelID, plan, defaultService, prunedHosts, logger); err != nil {
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

// reconcileOneTunnel reads one tunnel's configuration, merges the derived plan,
// drops routes whose DNS records this run pruned, and writes back only when
// something changed.
func reconcileOneTunnel(ctx context.Context, cli *tunnelClient, tunnelID string, plan []cfIngressRule, defaultService string, prunedHosts map[string]bool, logger *zap.Logger) error {
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

	merged, changed, shadowed, pruned := mergeIngressPlan(current.Config.Ingress, plan, prunedHosts)
	for _, s := range shadowed {
		log.Warn("preserved wildcard ingress rule shadows a declared host; the effective destination depends on rule order",
			zap.String("shadowing", s))
	}
	for _, host := range pruned {
		log.Info("deleted route whose DNS record was pruned; the name no longer resolves so the route was unreachable",
			zap.String("hostname", host))
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
		zap.Int("pruned_routes", len(pruned)),
		zap.String("catch_all_origin", origin))
	return nil
}
