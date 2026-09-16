# Design: Prune orphaned tunnel routes

## Context

The plugin manages both halves of a tunnel host: a DNS CNAME and an ingress rule. DNS has had an ownership model and a prune pass for a while — `pruneZone` deletes records tagged with this instance whose `(name, type)` is no longer declared. Ingress rules gained the same tag in `add-tunnel-ingress-management` (`description = <tag_prefix>:<instance>`), but deletion was explicitly excluded from that change: the tag is user-editable, so it is not proof of authorship, and "delete everything undeclared" would destroy hand-made configuration on the first run.

That leaves a real gap. A host removed from the Caddyfile keeps its route on the tunnel indefinitely, and the Routes list accumulates entries nobody can attribute. The tag exists; nothing acts on it.

Verified facts this design rests on:

- `PUT .../configurations` accepts and persists `description`, but the field is **absent from the API's documented model** (`ingress[]: { hostname, service, originRequest, path }`) and the dashboard's Routes table does **not** render it (the column shows "-"). So the tag is invisible in the UI.
- The dashboard does expose the route's service and its "Additional settings" state, which is enough to identify a suspicious entry but not to confirm authorship.
- The plugin already reads every DNS record in a zone during reconciliation, so knowing whether a hostname resolves costs no extra API call.

Relevant existing code:

- `pruneZone` (`reconcile.go`) — the DNS prune pass and the ownership test it relies on.
- `mergeIngressPlan` / `inheritUnmanagedFields` (`tunnelingress.go`) — where foreign rules are preserved today.
- `reconcileIngressPhase` (`reconcile.go`) — the phase that would host the prune pass.

## Goals / Non-Goals

**Goals:**

- Let an operator clean up dead routes automatically, without hand-deleting entries in a UI that cannot show who made them.
- Make every deletion **provable** rather than presumed: never delete a route that can still receive traffic.
- Keep the default posture unchanged: with no opt-in, nothing is ever deleted.

**Non-Goals:**

- Pruning rules with no tag, or with another instance's tag.
- Making the tag authoritative on its own.
- Pruning across zones, or for tunnels this config does not declare.
- Fixing the dashboard's failure to display `description` (not in our control).

## Decisions

### D1: Opt-in per zone, reusing the existing `prune` flag

Eligibility to prune requires the zone that owns the hostname to have declared `prune`. No new directive is introduced: the operator already opted into "this plugin may clean up my orphans here", and adding a second switch for the same intent invites the two to disagree.

*Rationale*: a separate flag would need its own documentation, its own failure mode (set one, not the other), and would not add safety — the destructive precondition is D2, not the switch.

*Alternative rejected*: always-on pruning of provably dead rules. Even a provably dead route may be documentation of intent, and deletion should stay something the operator asked for.

### D2: Three conditions, of which DNS-liveness is the load-bearing one

A rule is deleted only when all hold:

```
1. description == this instance's tag        (attribution)
2. hostname not declared by this config      (orphanhood)
3. hostname resolves to no DNS record        (liveness)
```

Condition 3 is the reason this can ship at all. Condition 1 alone is weak: a user can edit a description, copy one, or type it by accident, and the field is not even visible in the dashboard. Condition 2 alone is exactly the rule that was rejected as unsafe. Together with 3, the plugin deletes only routes that **cannot** receive traffic, because it owns the DNS half and can see that nothing points at the tunnel anymore.

The asymmetry is deliberate and worth stating plainly: this is strictly more conservative than the DNS prune, which deletes on tags alone. DNS records are auditable through a documented, dashboard-visible comment; ingress rules are not.

*Consequence*: a route whose hostname still resolves is never pruned, even if the config no longer declares it. That case means either the DNS record belongs to another instance or it was hand-made — both are reasons to leave the route alone. The plugin logs it as "left in place" so the operator knows it exists.

*Alternative rejected*: mirroring DNS prune exactly (tag + undeclared). It would delete routes that still resolve, and with the tag invisible in the UI a mistaken deletion would be undiagnosable.

### D3: The catch-all is never a candidate

The default rule has an empty hostname, so conditions 2 and 3 cannot be expressed for it, and it is always re-emitted by the reconciler. It is excluded structurally, not by a special case: eligibility requires a non-empty hostname that is not declared and does not resolve, and the catch-all has none.

This also guarantees the written configuration can never become invalid — the API requires a terminating catch-all and one is always present.

### D4: Prune after the plan write, in the same read-modify-write

The order inside one reconcile is: read the configuration → merge the derived plan (which preserves foreign rules) → delete eligible foreign rules → write once.

*Rationale*: doing it in one pass keeps the operation idempotent and avoids a second `PUT` that could race with `cloudflared`'s config sync. Deletions are applied to the already-merged rule list, so the write carries both the new plan and the removals.

*Alternative rejected*: a separate delete request after writing. Two writes per reconcile, a larger window for a partial failure, and the API offers no per-rule delete for this resource.

### D5: DNS-liveness is computed from the DNS phase, not re-read

The ingress prune needs the set of names that had no DNS record after the DNS phase. Two options: have the zone reconciliation return the names it left unpopulated, or re-read the zone's records in the ingress phase.

Chosen: the DNS phase already lists every record per zone; the ingress phase receives a set of "names with at least one record" per zone, so liveness is a map lookup rather than a second API call.

*Rationale*: an extra `GET /dns_records` per zone per reconcile doubles the read traffic for a cleanup that runs rarely. The data is already in hand.

*Consequence*: if a zone's DNS reconciliation failed entirely, its names are unknown. In that case the ingress prune for that zone is **skipped** rather than treating unknown as "does not resolve" — failing closed, consistent with the rest of the plugin.

### D6: Every deletion is logged with its evidence

Each deletion logs the rule's hostname, service, the tag that authorised it, and the fact that no DNS record existed. Each rule that looked like a candidate but was spared logs why (still resolving / no tag). This is the audit trail that replaces the missing UI column.

*Rationale*: with the tag invisible in the dashboard, the log is the operator's only way to reconstruct what the plugin decided and why. A silent delete would be unreviewable.

## Risks / Trade-offs

- **[The tag is invisible, so deletions are hard to audit after the fact]** → D6 logs every deletion and every near-miss with its reason; the docs point at the log line. Accepted, because the alternative (no prune) is what created the accumulation problem.
- **[A user copies the tag onto a hand-made route, and its DNS record is also gone]** → The route is deleted. This is the residual risk of any tag-based scheme; D2's liveness check means the route was unreachable anyway, so the blast radius is a configuration entry nobody could reach.
- **[The dashboard may not round-trip `description` when a rule is edited in the UI]** → Unverified, and it fails safe in one direction only: a lost tag means the rule is never pruned (condition 1 fails), not that an unrelated rule is deleted. If this turns out to be true, prune silently stops working for edited rules; the log line for "candidate without tag" makes it visible.
- **[A zone whose DNS reconciliation failed skips pruning]** → Intentional (D5). The zone already reports a DNS error, and the next reload retries.
- **[Deleting a route whose DNS record is temporarily absent]** → A DNS record can be deleted by hand while the route is still wanted. The route becomes unreachable at that moment regardless, so pruning it changes nothing for traffic; recreating the DNS record recreates the route too, since both come from the same declaration. Documented in troubleshooting.
- **[Prune is not reversible]** → The remedy is to re-declare the host, which recreates both halves. Documented as the recovery path.

## Migration Plan

1. Landing the code changes nothing: without the existing `prune` opt-in, the pass is a no-op.
2. Operators who already use `prune` for DNS gain route cleanup for free, limited to routes that are provably unreachable.
3. Rollback: turn off `prune` for the zone; deletions already performed are not undone, but no further pruning happens.

## Open Questions

- Whether the dashboard round-trips an unknown `description` when a rule is edited in the UI. Unverifiable without a browser session; the failure mode is safe (a lost tag disables prune for that rule) and the log makes it visible. Worth a manual check during implementation.
- Whether Cloudflare rejects a configuration where every non-catch-all rule was removed — i.e. a plan consisting only of the catch-all. Expected to be accepted (the API requires a catch-all, not a minimum rule count), but the implementation should assert it rather than assume.
