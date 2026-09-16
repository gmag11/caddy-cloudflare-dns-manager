# Design: Prune orphaned tunnel routes

## Context

A tunnel host has two halves: a DNS record and an ingress route. Removing the host from the Caddyfile prunes the record (in a `prune`-enabled zone) and leaves the route, so routes accumulate and only a human can clear them.

The first design for this keyed cleanup off an ownership tag written into the route's `description`, mirroring the DNS comment. Two discoveries killed it:

- `description` is **not in Cloudflare's documented ingress model** (`ingress[]: { hostname, service, originRequest, path }`), and the dashboard does not render the column.
- Verified live: **editing a route in the dashboard clears its description.** A tagged route came back untagged after a UI edit, while untouched routes kept theirs.

So the marker is invisible, undocumented, and erased by normal use. Keying destructive behaviour off it would produce a prune that silently stops working — and, worse, one whose decisions could not be audited.

The replacement needs no marker at all. `pruneZone` deletes a record because this instance owns it and the config no longer declares its host. When that record was the CNAME pointing at the tunnel, the route's hostname stops resolving at that moment. The route is not *presumed* dead; it is made dead by an action the plugin just took.

Relevant existing code:

- `pruneZone` (`reconcile.go`) — deletes owned orphaned records; now also reports which hostnames it deleted.
- `reconcileZone` (`reconcile.go`) — returns a `reconcileZoneResult` carrying the account id and the pruned hostnames.
- `mergeIngressPlan` / `inheritedUnmanagedFields` (`tunnelingress.go`) — where foreign rules are preserved and metadata is carried over.

## Goals / Non-Goals

**Goals:**

- Clean up routes whose DNS record this plugin just pruned, in the same reconcile.
- Never delete a route on the basis of a marker the operator cannot see.
- Keep the default posture: no opt-in, no deletions.

**Non-Goals:**

- Pruning routes for tunnels the configuration no longer declares (see D4).
- Any signal other than a DNS deletion performed in the same run.
- Replacing the tag mechanism with another tag mechanism.

## Decisions

### D1: Eligibility is a DNS event, not an inferred property

A route is deleted only when its hostname is in the set of names `pruneZone` deleted during this same run, and the route is not declared by the configuration.

```
DNS phase                          ingress phase
─────────                          ─────────────
pruneZone deletes                  route for that hostname?
  CNAME foo → tunnel                 ├─ declared by config  → keep (and reconcile)
  returns ["foo.example.com"]        ├─ in the pruned set   → delete
                                     └─ otherwise           → keep
```

*Rationale*: this is strictly stronger than the tag approach. It needs no ownership claim about the route, so it cannot misfire on a route the operator wrote by hand — if their DNS record was not pruned, their route is untouched. It also cannot drift out of sync with reality, because the signal is produced by the plugin's own action moments earlier rather than read back from a field the UI may have cleared.

*Alternative rejected*: tag + undeclared (mirroring DNS prune). It depends on the field described in Context, and it deletes routes that may still resolve.

*Alternative rejected*: tag + undeclared + no DNS record. This is what the first draft proposed. It is safe, but it still hinges on the tag, so a route edited in the dashboard becomes permanently ineligible — the plugin could never clean up the very routes most likely to be stale.

### D2: A declared host is never a prune candidate

`mergeIngressPlan` replaces a declared host's rule with the derived one before the prune decision, so declaration is checked first and wins.

This matters because a host can legitimately appear in the pruned set while still being declared: reverting a tunnel host to an address host deletes its CNAME in the same run (the migration path), yet the route may be wanted again the moment it is re-declared with `tunnel`. Declaration is the operator's current intent, so it takes precedence over a deletion that happened moments earlier.

*Consequence*: reverting a host to an address host leaves its route behind. That is the existing, documented behaviour for undeclared routes, and it is inert — the hostname now resolves to an address, not through the tunnel.

### D3: The catch-all is structurally ineligible

The default rule has no hostname, so it cannot match a pruned name. It is also always re-emitted by the reconciler, and the API requires a terminating catch-all. Eligibility therefore cannot select it, and the written configuration can never become invalid.

### D4: Only declared tunnels are touched

The pruned-name set says *which names* died, not *which tunnel* hosted their routes. Routes live inside a tunnel's configuration, so pruning needs the tunnel UUID, which comes from the configuration.

When at least one host still declares a tunnel UUID, that tunnel is visited and the set applies. When the configuration declares no tunnel at all, the plugin cannot tell which tunnel holds the route, and instead of enumerating every tunnel in the account — which would mean rewriting configurations the operator never declared to this plugin — it logs that manual removal is needed.

*Rationale*: the account-wide enumeration is a far larger blast radius than the cleanup is worth, and it would contradict the plugin's rule of only touching what was declared to it.

*Consequence, documented*: removing the **last** tunnel host of a config prunes its DNS record and leaves its route, with a log line saying so. The common case — removing one host among several — is fully automated.

### D5: One write per tunnel, in the same read-modify-write

The order in `reconcileOneTunnel` is: read → merge the derived plan (preserving foreign rules) → drop routes whose names were pruned → write once.

*Rationale*: a second request would double the write traffic and widen the window for a partial failure, and the API offers no per-rule delete for this resource. Deletions are applied to the already-merged list, so one `PUT` carries both the new plan and the removals.

### D6: Every deletion is logged with its cause

Each pruned route logs its hostname and states that its DNS record was deleted in the same run, which is why it was unreachable. The plan-write line carries the pruned count.

*Rationale*: the signal is a plugin action, not a user-visible field, so the log is how an operator reconstructs what happened. A silent delete would be unreviewable.

## Risks / Trade-offs

- **[Removing the tag leaves previously tagged rules with a `description`]** → Harmless: the field is inherited like `path` and `originRequest`, so it survives but is never read. Nothing is cleaned up, nothing breaks, and no migration is needed.
- **[The last tunnel host cannot be pruned (D4)]** → Reported in the log with the remedy. Accepted rather than widening the blast radius to the whole account.
- **[A route is deleted while a user still wants it]** → Only if its DNS record was pruned in the same run, meaning it stopped resolving anyway. Re-declaring the host recreates both halves. Documented as the recovery path.
- **[A zone whose DNS reconcile failed]** → Its prune did not run, so its pruned set is empty and nothing is deleted. Failing closed falls out of the design rather than needing a special case.
- **[The pruned set is hostname-keyed but routes are tunnel-scoped]** → A name could, in principle, exist in two zones. Prune only ever deletes the record for names it owns, and the route matches by hostname; a collision would require the same hostname in two managed zones, which zone assignment already prevents.
- **[Reverting a tunnel host to an address host leaves its route]** → D2, inert and pre-existing.

## Migration Plan

1. Landing the code changes nothing for configs without the `prune` opt-in.
2. Configs that already prune gain route cleanup automatically, limited to routes whose DNS records this plugin deletes.
3. Rules tagged by an earlier build keep their description; it is inherited from then on and never acted upon.
4. Rollback: disable `prune` for the zone. Deletions already performed are not undone, but no further pruning happens.

## Open Questions

- Whether Cloudflare accepts a configuration whose ingress list contains only the catch-all. Expected to be accepted (the API requires a catch-all, not a minimum rule count) and covered by a test, but worth confirming once against the live API.
- Whether `pruneZone` should also prune routes for tunnels referenced only by hosts that have been removed — the D4 case. Deferred deliberately; it would need the tunnel UUID from somewhere other than the configuration.
