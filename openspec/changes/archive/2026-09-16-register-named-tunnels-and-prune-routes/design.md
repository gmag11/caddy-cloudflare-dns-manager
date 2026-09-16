## Context

Tunnel-backed hosts are reconciled in two phases. The DNS phase deletes records and reports the
hostnames it deleted; the ingress phase, which runs afterwards, derives an ingress plan from the
hosts that declare a tunnel and merges it into each tunnel's configuration. Route eligibility for
deletion is the DNS deletion itself: a route whose hostname's record this run removed can no
longer receive traffic, so it is provably dead and safe to delete without an ownership marker.
That rule is implemented and end-to-end verified for two cases — an orphaned record and a
tunnel-to-address revert.

What is missing is the tunnel to write to. `deriveIngressPlan` keys off `HostConfig.TunnelID`, so
when no host declares a tunnel it returns an empty map and the ingress phase returns early. The
pruned hostnames were known; the UUID was not, because it was looked for only where
declarations live.

Two facts shape the design:

- **The UUID is available at deletion time.** A tunnel host's CNAME target is
  `<uuid>.cfargotunnel.com`, and the deletion sites already hold the record: `pruneZone` logs
  `r.Content`, and `clearConflictingRecords` deletes `r` after matching on type and name. Both
  discard the target today.
- **The existing merge cannot express "prune only".** `mergeIngressPlan` skips any current rule
  with no hostname and re-emits the catch-all from the plan. Handed an empty plan it would
  preserve the foreign rules, append nothing, and **write a configuration with no catch-all at
  all** — invalid per Cloudflare's requirement that the last rule be a catch-all. Its
  `inheritUnmanagedFields` call would also re-assert `matchSNItoHost` on foreign HTTPS rules,
  mutating rules the plugin does not own.

The declaration surface is the other half of the problem. `tunnel <uuid>` is validated for shape
only (`isUUID` at `caddyfile.go`), never for existence, so nothing connects the tunnels a config
writes to the tunnels an operator owns. A valid-UUID typo is accepted and produces a CNAME to a
tunnel that does not exist.

Constraints: no new credential (the account-scoped token already covers any tunnel in the
account); no account enumeration, which the archived change rejected and this one keeps rejected;
the per-zone `prune` opt-in remains the single switch for deleting routes.

## Goals / Non-Goals

**Goals:**

- Give a config an explicit, validated list of the tunnels it may write to, independent of
  whether any host currently uses them.
- Prune the route of a hostname whose record this run deleted, including for a tunnel no host
  declares any more, provided that tunnel is registered.
- Keep the write to a tunnel reached only through a deletion minimal: only the dead routes go,
  nothing else changes, and the catch-all is not touched.
- Preserve the existing guarantees: opt-in gate, declared hosts never pruned, undeclared routes
  with a live record preserved, at most one write per tunnel.

**Non-Goals:**

- A `tunnel`-to-`tunnel` switch. The CNAME is updated in place, not deleted, so no DNS deletion
  signals the change and the old tunnel's route goes stale with nothing to correlate against.
  Fixing that needs its own eligibility rule (a record whose target changed from tunnel A to
  tunnel B) and its own failure modes when that record is untagged.
- A per-tunnel default service. The registry is identity only; `tunnel_default_service` stays
  global, so several tunnels share one catch-all destination unless each host overrides it with
  `tunnel_service`. This limitation already exists for multi-tunnel configs today and is not
  worsened here.
- Deleting the tunnel itself, or any of its other rules.
- Retroactive cleanup: records left stranded by earlier builds are gone from the DNS side, so
  there is nothing left to derive a tunnel from. Those remain manual.
- Handling a deleted record whose target is not `cfargotunnel.com`, or a deleted A/AAAA. No
  tunnel is named, so the current report stands.

## Decisions

### A tunnel registry in the global block, by name

```caddyfile
cf_dns_manager {
    zone example.com api_token {$CF_API_TOKEN} prune
    account {$CF_ACCOUNT_TUNNEL_TOKEN}
    tunnel edge 13fc5d01-f96f-4f18-a0be-7adf03200f18
    tunnel lab  8a7f3c2e-1234-4567-89ab-cdef01234567
}
```

`tunnel <name> <uuid>` is repeatable. The name is a stable identifier for the operator; the UUID
is what every API call needs. Names are validated for uniqueness and for a safe shape (non-empty,
no whitespace); UUIDs are validated with the existing `isUUID` at the registration point, which is
where format validation belongs now that a host names a tunnel rather than spelling one.

*Alternative considered:* one tunnel per account, made implicit by the credential. Rejected — the
plugin already groups the plan per tunnel UUID, and the archived `add-tunnel-ingress-management`
spec has a two-tunnel scenario, so multi-tunnel is a supported shape and the registry is what
makes it expressible.

### The per-host directive takes a name, resolved at adapt time

`tunnel edge` replaces `tunnel <uuid>`. Resolution mirrors the mechanism already used for zones:
`parseDirective` reads the global option (`zonesFromOptions`) and validates the host against it
(`assignZone`) before emitting anything. The tunnel lookup is the same pattern with a map lookup
instead of a suffix match, so an unknown name fails at adapt time, like an undeclared zone.

`HostConfig.TunnelID` keeps holding the **resolved UUID**. Everything downstream — the
`<uuid>.cfargotunnel.com` CNAME target, `deriveIngressPlan`'s grouping key, the Tunnel API path —
is untouched, so this is a surface change rather than a data-model change.

*Alternative considered:* accept either a name or a bare UUID on the host, for compatibility.
Rejected — it keeps the format-only validation hole open for the UUID spelling and doubles the
error surface. The breaking change is taken deliberately, and the migration is one line per
tunnel.

*Rejected:* treating an unregistered UUID as implicit registration. It would silently restore the
current hole (write to a tunnel never declared) and make the registry advisory rather than
authoritative.

### Derive the tunnel from the deleted record's target, at the deletion site

The deletion sites return what they know. `clearConflictingRecords` returns, for a deleted CNAME,
the tunnel UUID parsed from `r.Content`; `pruneZone` does the same per deleted record. Parsing
lives in one helper: strip a trailing dot, require a `.cfargotunnel.com` suffix, and validate the
remaining label with `isUUID`, case-insensitively.

The alternative considered was re-reading DNS after the deletions to see what is gone. That
doubles the API calls and cannot distinguish "deleted by this run" from "never existed", which is
exactly the signal eligibility rests on. Carrying the target from the deletion is cheaper and does
not weaken the rule.

*Rejected:* enumerating `/accounts/{id}/cfd_tunnel` and matching hostnames against each tunnel's
rules. It finds more cases (it would also cover the `tunnel`-to-`tunnel` switch) but reads and
potentially rewrites tunnels the operator never registered — the blast radius the archived change
refused, and the reason this change stays narrow.

### Two independent gates: registration authorises, the record attributes

These are different questions and the design keeps them separate.

- **Authorisation** — is this tunnel one the config manages? Answered by the registry, consulted
  at the moment of writing. A tunnel named by a deleted record that is not in the registry is
  reported and skipped.
- **Attribution** — which tunnel did this hostname belong to? Answered by the CNAME target the
  plugin itself wrote and just deleted. Neither the registry nor any host declaration can answer
  it, because on a revert the `HostConfig` in hand has no `TunnelID` at all — that is what makes
  it a revert.

Keeping both means a mis-derived UUID cannot reach an arbitrary tunnel: it must also be
registered. And a registered tunnel is still only touched for hostnames this run actually
deleted a record for.

### The pruned set carries an optional tunnel per hostname

The phase currently receives `map[string]bool` of pruned names. It becomes a set keyed by
lowercased hostname whose value carries the tunnel UUID when the deleted record identified one.
The existing behaviour (eligibility, declared-host check, catch-all exclusion) is unchanged and
still consults membership alone.

### Union the declared tunnels with the registered ones reached by a deletion

`reconcileTunnelIngress` builds one map keyed by tunnel UUID: tunnels with a derived plan, plus
registered tunnels named by a pruned record, which have no plan. A tunnel that is both keeps its
plan and is visited once, on the normal path — the pruned names are handled by the existing merge,
so nothing is pruned twice and no tunnel gets two concurrent writes.

This also means the early return for "no tunnel declared" stops firing whenever a deleted record
named a registered tunnel, without any change to the condition that triggers it.

### A dedicated prune-only path for a tunnel with no plan

For a tunnel with no plan the merge is not used. A separate function drops the rules whose
hostname is in the pruned set and returns everything else exactly as read — original rule values,
original order, catch-all included — writing only when a rule was actually dropped.

Three details are deliberate:

- **The catch-all is not re-emitted**, because there is no plan to re-emit it from, and rewriting
  it would repoint the tunnel's default traffic at this config's `tunnel_default_service`. Under
  the requirement that a prune-only write change nothing else, the correct catch-all is the one
  already there.
- **`inheritUnmanagedFields` and `ensureMatchSNItoHost` are not applied.** Both exist to
  reconcile a rule the plugin is authoring. Applying them here would add `matchSNItoHost` to a
  foreign HTTPS rule that lacked it, which is no longer a prune-only write.
- **A rule whose hostname matches a declared host is still skipped**, so the "declared hosts are
  never pruned" guarantee holds identically on both paths. This is belt-and-braces — a tunnel with
  declared hosts is on the normal path by construction — but it keeps the two paths from diverging
  if that construction ever changes.

The existing single-write property follows: one read, then at most one write per tunnel.

### Correct the `Declared hosts are never pruned` revert scenario

The spec has two requirements in direct conflict on the tunnel-to-address revert: one says the
route is deleted, the other cites the same revert as an example of a route that is preserved. The
implementation deletes it (`deriveIngressPlan` omits hosts without a `TunnelID`, so a reverted host
is not "declared" for the merge) and a live end-to-end run confirmed the deletion. The wording is
corrected in this change rather than the behaviour, and the requirement is made explicit: a
hostname is declared for this purpose only by a host the current config declares with a tunnel.

## Risks / Trade-offs

- **[BREAKING configuration change for existing users]** → `tunnel <uuid>` stops adapting. The
  error for a UUID-shaped unregistered value names the migration explicitly, so the failure
  teaches the fix rather than leaving a mystery. The change ships with the docs and the `testenv`
  updated, and the migration is one registry line plus replacing a UUID with a name per host.
- **[A prune-only write to a tunnel no host declares is a widened reach]** → Bounded by three
  independent gates: the tunnel must be **registered**, the owning zone must have declared
  `prune`, and the write touches only rules whose hostname is in the pruned set. The tunnel is
  reached only when the plugin has just deleted a record pointing at it, so it is one the operator
  had already put under this plugin's management.
- **[The registry must stay authoritative or the guarantee is cosmetic]** → Host-side resolution
  rejects an unregistered name at adapt time, and the ingress phase re-checks registration before
  writing. An unregistered UUID can therefore neither be declared nor reached at runtime.
- **[A hand-managed CNAME to a registered tunnel gets its route deleted]** → Only if the plugin
  deleted that record in this run, which for an unowned record requires `force_adopt`. Without
  `force_adopt` the plugin leaves untagged records alone, so it never deletes the record and never
  derives the tunnel.
- **[Two tunnels touched in one run, one with a plan]** → Handled by keying the map on UUID: the
  planned tunnel keeps the normal path, the other gets prune-only, and neither is visited twice.
- **[The docs and the existing end-to-end expectation both encode the old limit]** → Both are in
  scope. The end-to-end case that asserted the "no tunnel is declared" log line and no write must
  now assert the deletion.

## Migration Plan

No data migration; the change is a config rewrite per deployment.

1. Add one `tunnel <name> <uuid>` line per tunnel to the global block, taking the UUID from the
   existing per-host `tunnel <uuid>` declarations.
2. Replace each host's `tunnel <uuid>` with `tunnel <name>`.
3. Reload. A leftover UUID-shaped value produces an error naming the migration.

Rollback is a revert: routes already pruned stay pruned (they were dead by construction), and
later runs stop pruning unregistered tunnels' routes. Nothing is persisted by the plugin, so there
is no state to unwind. Records stranded by earlier builds have no DNS signal left and remain a
manual cleanup in either direction.

## Open Questions

- Should the residual report for a pruned name whose record named no tunnel also say that no route
  may exist at all? A deleted A record never implied a tunnel route, so the report could read as
  an alarm when there is nothing to clean. The current wording keeps the existing manual-removal
  phrasing; worth revisiting if it proves noisy on address-only configs.
- Should the registry entry carry the tunnel's account, for a config spanning accounts? Not now:
  the account is derived from the zone, and a tunnel and its zone must share an account. A second
  account would need a different credential, which is a separate change.
