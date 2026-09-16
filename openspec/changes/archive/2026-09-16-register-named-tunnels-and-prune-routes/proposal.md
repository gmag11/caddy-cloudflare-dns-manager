# Register named tunnels and prune their routes

## Why

Switching every tunnel-backed host of a config to address records leaves the tunnel's routes
behind. Reprogramming one host already works: the plugin deletes the CNAME to make room for the
address record, and prunes the matching route in the same run. But when that host was the last
one declaring the tunnel, the ingress phase finds no tunnel to write to and gives up. Log,
verbatim, from a real run:

```
DNS records were pruned but no tunnel is declared, so their routes cannot be identified;
remove them manually in the dashboard   {"pruned_names": 2}
```

The root cause is where the tunnel is declared. Its UUID lives only in the per-host
`tunnel <uuid>` subdirective, so a hostname-to-tunnel mapping exists only as long as some host
keeps the tunnel alive by naming it. The moment the last such host changes, the plugin has no
way to know which tunnel held the route — even though it just deleted a CNAME whose content was
`13fc5d01-f96f-4f18-a0be-7adf03200f18.cfargotunnel.com`, with the UUID in hand.

The declaration surface is also weaker than it looks. `tunnel <uuid>` is validated for format
only, never for existence, so a typo with valid UUID syntax is accepted and silently creates a
CNAME pointing at a tunnel that does not exist — a failure the docs already describe. Nothing
ties the tunnels a config writes to the tunnels an operator actually owns.

Declaring tunnels once, by name, in the global block fixes both. The plugin then knows which
tunnels it is allowed to manage independently of whether any host currently uses them, and the
per-host directive references a name the adapter resolves and validates at adapt time. A route
and the DNS record that makes it reachable are one artifact in two halves; the plugin already
refuses to create one without the other, and should not refuse to delete the pair.

## What Changes

- **BREAKING**: add a tunnel registry to the global `cf_dns_manager` block:
  `tunnel <name> <uuid>`, repeatable. Each entry names a tunnel and gives its UUID; the UUID is
  validated for format, names for uniqueness.
- **BREAKING**: the per-host `tunnel` subdirective takes a registered **name** instead of a raw
  UUID: `tunnel edge` rather than `tunnel 13fc5d01-...`. The adapter resolves it to the UUID at
  adapt time, so a host naming an unregistered tunnel is a configuration error rather than a
  runtime surprise. A bare UUID is not accepted as an implicit registration — an unregistered
  value that parses as a UUID produces an error naming the migration explicitly.
- **Resolve names in the adapter, keep UUIDs downstream.** `HostConfig.TunnelID` continues to
  hold the resolved UUID, so the CNAME target, the ingress plan and the per-tunnel grouping are
  unchanged. Only the declaration surface moves.
- **Gate route pruning on the registry.** A pruned hostname's route is deleted only when the
  tunnel it belonged to is one the config registers. The tunnel is identified from the target of
  the CNAME this run deleted (`<uuid>.cfargotunnel.com`), never by enumerating the account's
  tunnels. A deleted record naming an unregistered tunnel is reported, not acted on.
- **Prune a registered tunnel's routes even with no host declaring it.** With the tunnel
  identified, the ingress phase reaches a tunnel that no host currently declares, subject to the
  same per-zone `prune` opt-in as every other route deletion.
- **A prune-only write is minimal.** On a tunnel reached only through a deleted record, only the
  dead routes are removed; everything else is left byte-identical — no derived plan is
  re-emitted, the catch-all is not rewritten, and other rules keep their position, service and
  options. This holds by construction rather than by care: a rule is a candidate only if its
  hostname is in the pruned set, and the catch-all has no hostname.
- **Narrow the residual report.** The "no tunnel is declared" log is replaced by two precise
  ones: a pruned name whose deleted record names no tunnel at all, and a pruned name whose
  deleted record names a tunnel outside the registry.
- **Fix a contradiction in the `tunnel-ingress-prune` spec.** Its requirement *Declared hosts
  are never pruned* carries a scenario citing a tunnel-to-address revert as an example of a
  route that is preserved, while the requirement *A route is eligible only when its DNS record
  was deleted in the same run* states that the same revert deletes the route. The second matches
  the implementation and the live end-to-end result; the first is a wording error, and is
  corrected rather than acted on.

## Capabilities

### New Capabilities

None. Every requirement this change needs belongs to capabilities that already exist.

### Modified Capabilities

- `caddyfile-config`: the global block gains a repeatable `tunnel <name> <uuid>` registry with
  its validation rules; the per-site `tunnel` requirement changes to take a registered name.
- `tunnel-dns-management`: the `tunnel` host declaration is re-specified in terms of a registry
  name, with resolution and its failure modes; the exclusivity scenarios move to the new
  spelling.
- `tunnel-ingress-prune`: prune reachability is restated in terms of registered tunnels rather
  than declared ones, gaining the registry-rejection case and a new requirement for what a
  prune-only write may and may not change. The requirement *Declared hosts are never pruned* has
  its revert scenario corrected.
- `tunnel-ingress-management`: the requirement *Undeclared ingress rules are not deleted* gains
  an explicit scenario for a route on a registered tunnel no host declares, which the current
  wording leaves to inference.

## Impact

- **Configuration**: **BREAKING** for every existing config — `tunnel <uuid>` becomes
  `tunnel <name>` plus one registry entry per tunnel. The adapter's error for an unregistered
  UUID-shaped value is the migration path, so the failure is self-explanatory.
- **Code**: `caddyfile.go` (global `tunnel` parsing and registry validation, host-side name
  resolution mirroring `assignZone`, `isUUID` moving to the registration point); `app.go`
  (`App` gains the tunnel registry; `HostConfig` keeps `TunnelID` as the resolved UUID);
  `reconcile.go` (`pruneZone` and `clearConflictingRecords` report the deleted CNAME's target
  alongside the hostname, the ingress phase carries it and checks it against the registry);
  `tunnelingress.go` (the tunnel set becomes registered tunnels, with a prune-only path that
  does not re-emit a plan).
- **Docs**: `README.md`, `docs/cloudflare-tunnel.md`, `docs/troubleshooting.md`,
  `docs/publishing-addresses.md`, `docs/docker-deployment.md` and the `testenv` files all show
  the `tunnel <uuid>` spelling or the last-tunnel-host limit, and must describe both the registry
  and the new deletion behaviour.
- **Tests**: registry parsing and validation, name resolution including the unknown-name and
  UUID-shaped-value failures, unit coverage for target parsing and for the prune-only write
  preserving surrounding rules and the catch-all, a negative case for a CNAME naming an
  unregistered tunnel, and the existing last-host end-to-end case now expecting deletion instead
  of a log line.
- **Not included**: a `tunnel`-to-`tunnel` switch. That updates the CNAME in place rather than
  deleting it, so no DNS deletion occurs and the old tunnel's route becomes dead with no signal
  to correlate against — the same shape of leftover, but it needs its own eligibility rule rather
  than this one. Also not included: a per-tunnel default service (the global
  `tunnel_default_service` continues to supply every tunnel's catch-all, a limitation that
  already exists for the multi-tunnel configs the current specs allow), deleting the tunnel
  itself, and any cleanup for records that predate this change and were already left stranded.
