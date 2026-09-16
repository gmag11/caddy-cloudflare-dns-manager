# Prune orphaned tunnel routes

## Why

The plugin writes a route for every tunnel-backed host, but never removes one: deleting a host from the Caddyfile prunes its DNS record and leaves its route on the tunnel forever, so the Routes list accumulates entries that can only be cleaned up by hand.

A first attempt keyed cleanup off an ownership marker written into the rule's `description`. That is not viable: `description` is absent from Cloudflare's documented ingress model, the dashboard does not display it, and — verified live — the dashboard **clears it** whenever a rule is edited there. Any scheme built on it would silently stop working the first time someone touched a route in the UI.

The DNS side already has the answer. When `prune` deletes a CNAME that pointed at the tunnel, the plugin has just removed the only thing making that hostname reachable. The corresponding route is provably dead at that exact moment — no inference, no marker needed.

## What Changes

- **Remove** the ownership-tag mechanism from ingress rules: stop writing `description`, stop treating it as drift, and inherit it from the operator like any other unmanaged field.
- Prune a tunnel route when **this run's DNS prune deleted the record that made its hostname reachable**. The route deletion and the DNS deletion happen in one reconcile.
- A route is never pruned for any other reason: an undeclared route whose DNS record still exists is preserved, however stale it looks.
- A declared host's route is never a prune candidate, even if its DNS record changed type in the same run.
- The catch-all is never removed; the reconciler always re-emits it, so a written configuration stays valid.
- Prune runs under the existing per-zone `prune` opt-in — no new directive.
- Prune only reaches tunnels the configuration declares. When the last host of a tunnel is removed, prune drops its DNS record but cannot know which tunnel the route belonged to; it reports that rather than enumerating the account's tunnels.

## Capabilities

### New Capabilities

- `tunnel-ingress-prune`: The opt-in, the DNS-correlation rule that makes a route eligible, what is never eligible (declared hosts, the catch-all, routes of undeclared tunnels), single-write behaviour, and the audit logging of deletions.

### Modified Capabilities

- `tunnel-ingress-management`: The "Undeclared ingress rules are not deleted" requirement becomes conditional on the DNS-correlation rule. The requirement that rules carry an ownership tag is **removed** — the mechanism is being reverted — and `description` returns to being an unmanaged field preserved like `path` and `originRequest`.
- `record-ownership`: The `prune` opt-in's scope is clarified: it covers this instance's orphaned artifacts in that zone, and for tunnel routes it additionally requires the DNS record to have been deleted in the same run.

## Impact

- **Code**: `tunnelingress.go` (drop the tag from the plan, drift and derive signature; `mergeIngressPlan` gains the pruned-name set and reports what it dropped), `reconcile.go` (`pruneZone` returns the hostnames it deleted, `reconcileZone` propagates them, the ingress phase applies them).
- **Removed**: the `description` write path and its drift check. Rules tagged by an earlier build keep their tag until a rewrite, at which point the field is inherited like any other unmanaged field; nothing is actively cleaned up and nothing breaks.
- **Tests**: the tag-semantics tests are replaced by DNS-correlation tests, including the negative cases (nothing pruned → nothing deleted, declared host → never deleted, catch-all → never removed).
- **Docs**: `docs/cloudflare-tunnel.md`, `docs/troubleshooting.md`, `docs/architecture.md`, `README.md`.
- **Not included**: pruning routes for tunnels the config no longer declares; pruning on any signal other than a DNS deletion in the same run.
