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
				Hostname:      strings.ToLower(hc.Host),
				Service:       service,
				OriginRequest: ensureMatchSNIToHost(nil, service),
			})
		}
		// The default rule has no hostname, which is what makes it match all
		// traffic. It is always present: the API requires a terminating
		// catch-all, and it is also the "default route" the config asks for.
		rules = append(rules, cfIngressRule{
			Service:       def,
			OriginRequest: ensureMatchSNIToHost(nil, def),
		})
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
func mergeIngressPlan(current, plan []cfIngressRule, prunedHosts map[string]prunedName) (merged []cfIngressRule, changed bool, shadowed []string, pruned []string) {
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
		if _, ok := prunedHosts[host]; ok {
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

	// Replacing a rule's service must not erase options the plugin does not
	// manage. Path and the origin-request options other than matchSNItoHost are
	// only carried over, never authored, so this is not an ownership claim: it
	// is the same "don't destroy what you don't model" rule the raw-JSON fields
	// follow.
	inheritUnmanagedFields(merged, current)

	return merged, !ingressRulesEqual(current, merged), shadowed, pruned
}

// inheritUnmanagedFields copies the fields the plugin does not author — path,
// and any origin-request option other than matchSNItoHost — from the existing
// rule at the same hostname (the empty hostname matching the catch-all).
//
// Without this, a declared host's rule is regenerated from the derived plan and
// anything the operator set in the dashboard on that rule is lost — an option
// such as http2Origin or originServerName among it.
//
// matchSNItoHost is excluded from inheritance because the plugin authors it:
// for an HTTPS service it is always enabled, so a rule that lost it is
// corrected rather than left missing. The merge keeps every other option, so
// this adds one key instead of replacing the object.
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
			// Nothing to inherit, but the managed option still has to hold.
			merged[i].OriginRequest = ensureMatchSNIToHost(merged[i].OriginRequest, merged[i].Service)
			continue
		}
		if len(merged[i].Path) == 0 {
			merged[i].Path = prev.path
		}
		// Fold the operator's options under whatever the derived rule carries,
		// then re-assert the managed key on the result.
		base := merged[i].OriginRequest
		if len(prev.originRequest) > 0 {
			if len(base) == 0 {
				base = prev.originRequest
			} else if folded, ok := mergeOriginRequest(prev.originRequest, base); ok {
				base = folded
			}
		}
		merged[i].OriginRequest = ensureMatchSNIToHost(base, merged[i].Service)
	}
}

// mergeOriginRequest overlays over onto under, so keys present in over win and
// keys only in under survive. It reports false when either side is not a JSON
// object, in which case the caller keeps what it had rather than guessing.
func mergeOriginRequest(under, over json.RawMessage) (json.RawMessage, bool) {
	opts := map[string]json.RawMessage{}
	if err := json.Unmarshal(under, &opts); err != nil {
		return nil, false
	}
	var overOpts map[string]json.RawMessage
	if err := json.Unmarshal(over, &overOpts); err != nil {
		return nil, false
	}
	for k, v := range overOpts {
		opts[k] = v
	}
	merged, err := json.Marshal(opts)
	if err != nil {
		return nil, false
	}
	return merged, true
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
// fields the plugin manages: hostname, service, and whether matchSNItoHost is
// enabled for an HTTPS service.
//
// Comparing only managed fields is deliberate. Cloudflare normalises what it
// stores (it may reorder or fill in origin-request options) and the operator
// may set others the plugin does not model, so a deep equality check would
// report drift on every run and issue a write on every reload — an idempotence
// bug that a mock returning a fixed document would never reveal. Path and the
// remaining origin-request options are therefore excluded, while the option the
// plugin guarantees is compared explicitly, so a rule that lost it is repaired.
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
		if wantsMatchSNIToHost(a[i].Service) != hasMatchSNIToHost(a[i].OriginRequest) {
			return false
		}
		if wantsMatchSNIToHost(b[i].Service) != hasMatchSNIToHost(b[i].OriginRequest) {
			return false
		}
	}
	return true
}

// reconcileTunnelIngress derives and writes the ingress plan for every tunnel
// the given hosts declare, and deletes routes whose DNS records this run pruned.
// It is a separate phase from DNS reconciliation: a tunnel's hosts can span
// zones, so grouping by zone would fragment a single tunnel's plan into
// competing writes.
//
// It runs even with no declared tunnel hosts when prunedHosts is non-empty, so
// a tunnel that just lost its last host still gets cleaned up.
//
// Two independent conditions gate every write, and both are required:
//
//   - Registration (registered) authorises the tunnel. A tunnel absent from the
//     registry is never contacted, whatever a deleted record says.
//   - The pruned set attributes a hostname to a tunnel, and includes only names
//     whose DNS record this run deleted, so every candidate route is provably
//     dead rather than merely undeclared.
//
// Failures are returned rather than fatal, so a Tunnel API problem never
// prevents DNS reconciliation (and vice versa). A partial failure leaves DNS
// correct and is retried on the next config load.
func reconcileTunnelIngress(
	ctx context.Context,
	cli *tunnelClient,
	hosts []HostConfig,
	defaultService string,
	prunedHosts map[string]prunedName,
	registered func(string) bool,
	logger *zap.Logger,
) error {
	plans := deriveIngressPlan(hosts, defaultService)

	// The declared hostnames per tunnel, computed from the host declarations
	// rather than from the plan, so the prune-only path can refuse to delete a
	// declared host without relying on the plan it never received.
	declaredByTunnel := make(map[string]map[string]bool)
	for _, hc := range hosts {
		if hc.TunnelID == "" {
			continue
		}
		if declaredByTunnel[hc.TunnelID] == nil {
			declaredByTunnel[hc.TunnelID] = make(map[string]bool)
		}
		declaredByTunnel[hc.TunnelID][strings.ToLower(hc.Host)] = true
	}

	// Tunnels to visit: those with a derived plan, plus those a deleted record
	// points at. The plan is keyed by UUID, so a tunnel that is both is visited
	// once, on the planned path — the pruned names are handled by the merge.
	tunnelIDs := make([]string, 0, len(plans))
	for id := range plans {
		tunnelIDs = append(tunnelIDs, id)
	}

	// Attribution is per hostname, so collect what each tunnel is owed and
	// report the names no tunnel can be attributed to.
	prunedByTunnel := make(map[string]map[string]prunedName)
	needsVisit := make(map[string]bool)
	var unregistered, unattributed []string
	for host, pn := range prunedHosts {
		if pn.TunnelID == "" {
			unattributed = append(unattributed, host)
			continue
		}
		if !registered(pn.TunnelID) {
			unregistered = append(unregistered, host)
			continue
		}
		if prunedByTunnel[pn.TunnelID] == nil {
			prunedByTunnel[pn.TunnelID] = make(map[string]prunedName)
		}
		prunedByTunnel[pn.TunnelID][host] = pn
		needsVisit[pn.TunnelID] = true
	}
	// A tunnel with no plan must be visited; one that already has a plan is
	// handled by the merge, which prunes the same names. Dedupe through the
	// set: several pruned names can name the same tunnel, and visiting it once
	// per name would issue competing writes and race with itself.
	for id := range needsVisit {
		if _, hasPlan := plans[id]; !hasPlan {
			tunnelIDs = append(tunnelIDs, id)
		}
	}
	sort.Strings(tunnelIDs)
	sort.Strings(unregistered)
	sort.Strings(unattributed)

	// A name whose deleted record named no tunnel cannot be cleaned up: the
	// plugin does not know which tunnel held its route, and it will not list
	// the account's tunnels to find out.
	if len(unattributed) > 0 {
		logger.Info("DNS records were pruned but their deleted records name no tunnel, so their routes cannot be identified; if any exist, remove them manually in the dashboard",
			zap.Int("pruned_names", len(unattributed)),
			zap.Strings("hostnames", unattributed))
	}
	// A name whose record named an unregistered tunnel is deliberately left
	// alone: the registry is what authorises a write, and it does not contain
	// this tunnel.
	if len(unregistered) > 0 {
		logger.Warn("DNS records were pruned but the tunnels their records named are not registered, so their routes are left untouched; add the tunnel to the global block to manage it",
			zap.Int("pruned_names", len(unregistered)),
			zap.Strings("hostnames", unregistered))
	}

	if len(tunnelIDs) == 0 {
		return nil
	}

	var (
		wg    sync.WaitGroup
		errMu sync.Mutex
		errs  []error
	)

	for _, tunnelID := range tunnelIDs {
		plan := plans[tunnelID]
		prunedNames := prunedByTunnel[tunnelID]
		declared := declaredByTunnel[tunnelID]
		wg.Add(1)
		go func(tunnelID string, plan []cfIngressRule, prunedNames map[string]prunedName, declared map[string]bool) {
			defer wg.Done()
			if err := reconcileOneTunnel(ctx, cli, tunnelID, plan, defaultService, prunedNames, declared, logger); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}(tunnelID, plan, prunedNames, declared)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("%d tunnel(s) failed: %v", len(errs), errs)
	}
	return nil
}

// reconcileOneTunnel reads one tunnel's configuration, applies the derived plan
// when there is one, drops routes whose DNS records this run pruned, and writes
// back only when something changed. A tunnel reached only through a deleted
// record has no plan and takes the prune-only path, which must not invent one.
func reconcileOneTunnel(ctx context.Context, cli *tunnelClient, tunnelID string, plan []cfIngressRule, defaultService string, prunedNames map[string]prunedName, declaredHosts map[string]bool, logger *zap.Logger) error {
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
			zap.String("remedy", "recreate the tunnel as remotely-managed, or stop declaring its hosts with `tunnel <name>`"))
		return fmt.Errorf("tunnel %s: ingress is locally managed (source: local); "+
			"recreate the tunnel as remotely-managed, or remove its hosts from the plugin", tunnelID)
	}

	var (
		merged    []cfIngressRule
		changed   bool
		shadowed  []string
		pruned    []string
		pruneOnly = len(plan) == 0
	)
	if pruneOnly {
		merged, changed, pruned = pruneOnlyIngress(current.Config.Ingress, prunedNames, declaredHosts)
	} else {
		merged, changed, shadowed, pruned = mergeIngressPlan(current.Config.Ingress, plan, prunedNames)
	}
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

	if pruneOnly {
		log.Info("pruned routes from a tunnel this configuration no longer assigns hosts to",
			zap.Int("rules", len(merged)),
			zap.Int("pruned_routes", len(pruned)))
		return nil
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

// pruneOnlyIngress removes the routes whose hostnames this run pruned and
// changes nothing else. It exists because mergeIngressPlan cannot express this:
// that function drops every rule without a hostname and re-emits the catch-all
// from the derived plan, so an empty plan would produce a configuration with no
// catch-all at all — invalid, and a rewrite of a default route the plugin was
// never asked to manage.
//
// Every other rule is returned exactly as read, in place, including its
// service, its path and its origin request. In particular matchSNItoHost is
// neither added nor repaired here: that is the plugin authoring a rule, which
// is not what a prune-only write is.
//
// A rule whose hostname is in declaredHosts is skipped, mirroring the guarantee
// on the planned path. This cannot happen today — a tunnel with declared hosts
// has a plan, so it never reaches here — but the two paths must agree on what
// they refuse to delete if that ever changes.
func pruneOnlyIngress(current []cfIngressRule, prunedNames map[string]prunedName, declaredHosts map[string]bool) (merged []cfIngressRule, changed bool, pruned []string) {
	if len(prunedNames) == 0 {
		return current, false, nil
	}

	merged = make([]cfIngressRule, 0, len(current))
	for _, r := range current {
		// The catch-all has no hostname and is structurally ineligible: it can
		// never appear in a set of hostnames.
		if r.Hostname == "" {
			merged = append(merged, r)
			continue
		}
		host := strings.ToLower(r.Hostname)
		if declaredHosts[host] {
			merged = append(merged, r)
			continue
		}
		if _, ok := prunedNames[host]; !ok {
			merged = append(merged, r)
			continue
		}
		pruned = append(pruned, r.Hostname)
	}
	sort.Strings(pruned)

	if len(pruned) == 0 {
		return current, false, nil
	}
	return merged, true, pruned
}
