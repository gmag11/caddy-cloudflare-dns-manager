# Design: Cloudflare Tunnel DNS management

## Context

The plugin reconciles A/AAAA records for hosts declared via per-site `cf_dns_manager { host ... }` directives. Reconciliation is per-(name, record type): ownership is tracked by an exact comment tag (`<tag_prefix>:<instance>`), untagged records require `force_adopt`, and prune deletes owned records whose (name, type) is not in the declared set.

For a Cloudflare Tunnel host the DNS plan changes: the name needs a **CNAME** to `<tunnel-id>.cfargotunnel.com`, always proxied, with no A/AAAA. The tunnel itself (cloudflared process, credentials, ingress rules) is managed outside this plugin; we manage only the DNS routing record, like we do today for IP-based records.

Current code paths affected:

- `HostConfig` (`app.go`) — no tunnel concept.
- `parseHostBlock` (`caddyfile.go`) — parses `host`/`ip`/`ip6`/`proxied`/`force_adopt`.
- `Reconcile`/`reconcileZone` (`reconcile.go`) — marks `A` always and `AAAA` conditionally in the `managed` map, resolves effective IPs via shared detection, computes `proxied` via `effectiveProxied`.
- `listRecords` (`cloudflare.go`) — **filters out everything except A/AAAA client-side**, so CNAMEs are currently invisible to the plugin (they would not be reconciled, adopted, or pruned).
- `effectiveProxied` — computes proxy mode from the record content IP. For a CNAME the content is a hostname; `net.ParseIP` fails → `isPrivateIP` returns true → it would force `proxied=false`, silently breaking the tunnel. This function must not be applied to CNAME records.

## Goals / Non-Goals

**Goals:**

- Per-host opt-in to tunnel DNS management via a `tunnel <uuid>` subdirective (per-subdomain identification, not global).
- Create/update CNAME records with correct ownership tagging, adoption, and prune semantics.
- Clean migration from an IP-managed host to a tunnel host on the same name.
- Reproducible test environment with a real `cloudflared` container.

**Non-Goals:**

- Managing tunnel ingress rules (belongs to cloudflared config / Zero Trust dashboard).
- Resolving tunnel *names* to UUIDs via the Cloudflare API (no Account-level API scopes, no `account_id` in config; may be a future change).
- Creating or deleting tunnels; managing tunnel health/monitoring.
- Provisioning tunnels from Caddy (evaluated, descoped for now): creating/configuring a tunnel via `POST /accounts/{id}/cfd_tunnel` + `PUT .../configurations`, emitting a run token for cloudflared. Technically possible, but adds Account-scoped `Cloudflare Tunnel:Edit` (equivalent in blast radius to the account `cert.pem`, able to reconfigure every tunnel in the account) and still needs external glue to (re)start cloudflared with the token — Caddy cannot supervise a process. Manual tunnel management preferred; may be revisited as a separate, explicitly opt-in capability.
- Managing non-tunnel CNAME records for non-tunnel hosts.

## Decisions

### D1: UUID direct, no name resolution

`tunnel` takes the tunnel UUID directly (`cloudflared tunnel list` / dashboard). Validated as a canonical UUID (8-4-4-4-12 hex) at adapt time.

*Alternative considered*: resolve tunnel names via `GET /accounts/{id}/cfd_tunnel`. Rejected for v1: requires Account-scoped token and `account_id` config, new API surface, and the UUID is trivially copyable. Name resolution can be layered later without breaking this surface.

### D2: `tunnel` is mutually exclusive with `ip`, `ip6`, `proxied` — adapt-time error

A tunnel host has no IP plan; `proxied` has no freedom (CNAME to cfargotunnel.com only resolves when proxied — it does not exist as a public DNS-only record). Hard errors, not warnings, make intent explicit and prevent silently broken configs.

`force_adopt` remains valid and is the expected way to adopt a CNAME pre-created by `cloudflared tunnel route dns`.

### D3: Tunnel hosts produce a CNAME plan; reconcile branches per host

In `reconcileZone`, per host:

```
tunnel host      → managed(key,"CNAME"); reconcileFamily(CNAME, <uuid>.cfargotunnel.com, proxied=true)
normal host      → unchanged (A always, AAAA when ip6 enabled)
```

`reconcileFamily` needs no structural change — it already takes (type, content, proxied, existing records) and handles create/update/adopt on drift. For tunnel hosts we pass `proxied=true` directly and never call `effectiveProxied` (see D5).

### D4: Migration A → CNAME: delete owned A/AAAA before creating the CNAME

Cloudflare rejects creating a CNAME at a name that already has A/AAAA records. Relying on prune is not enough: prune runs *after* reconcile in `reconcileZone`, and `managed` for a tunnel host no longer marks A/AAAA — so with prune the first run would still fail to create the CNAME.

Decision: when reconciling a tunnel host, before creating the CNAME, delete any A/AAAA records at that name **that carry this instance's ownership tag**. Untagged A/AAAA records are left alone (same conservative rule as everywhere else) and produce a warning telling the user to remove them or use `force_adopt`.

`force_adopt` on a tunnel host additionally authorizes deleting untagged A/AAAA at the name (adoption of the *name*), mirroring its meaning for untagged CNAMEs.

In prune-enabled zones this is self-healing afterward; in non-prune zones the explicit delete keeps a single reload from a broken half-migrated state.

The reverse transition (tunnel → address host, D4b) needs the same treatment in the opposite direction, or revert fails in one reload. Both directions share one helper, `clearConflictingRecords`, which deletes owned records of the blocking type(s) at the name (or untagged ones under `force_adopt`) and returns the updated snapshot so prune does not re-attempt the deletions.

### D4b: Migration CNAME → A/AAAA on revert

When a host is changed from tunnel-backed back to a normal host, `reconcileZone` creates its A/AAAA records *before* `pruneZone` removes the now-orphaned CNAME. Cloudflare rejects the A/AAAA creation with error code 81054 while the CNAME still exists, and because the following config loads are byte-identical ("config is unchanged"), Caddy does not re-run `Start`: the name is left with no record at all until a restart. Confirmed against the running test environment.

Decision: before reconciling a normal host's A/AAAA, clear owned CNAME records at the name (same `force_adopt` rules for untagged ones). This is symmetric to D4 and uses the same helper.

*Alternative considered*: run `pruneZone` before host reconciliation. Rejected: it changes global ordering, could delete other hosts' records before their reconcile attempt, and interacts poorly with the "spare owned records on detection failure" rule. A targeted per-host pre-clear is local and testable.

### D5: CNAME bypasses `effectiveProxied`

`effectiveProxied` interprets content as an IP; a CNAME target is not an IP and would be classified private → `proxied=false` → broken tunnel. Tunnel hosts always reconcile with `proxied=true`, computed by the tunnel branch, never through `effectiveProxied`. A guard comment/test pins this.

### D6: `listRecords` includes CNAME records

Change the client-side filter from `A|AAAA` to `A|AAAA|CNAME`. Consequences:

- Tunnel hosts see their CNAME in `byName` and get create/update/adopt behavior.
- **Prune safety**: prune deletes owned records whose (name,type) is unmanaged. For a normal host, a stray owned CNAME is now unmanaged → pruned. That is correct: the plugin owns it (tagged comment) and the declared config doesn't ask for it. Untagged CNAMEs are never touched.
- Mixed-family records at one name (e.g. owned A + declared tunnel host) are handled by D4, not by name-level logic.

### D7: Tunnel hosts do not trigger IP detection

`detectPublicIPs` computes `wantV4`/`wantV6` from hosts needing detection; tunnel hosts contribute to neither. A config with only tunnel hosts performs zero detection requests. Detection still runs if any normal host needs it (shared result), unaffected by tunnel hosts.

### D8: Test environment gets a locally-managed cloudflared

`testenv/docker-compose.yml` gains a `cloudflared` service:

```yaml
cloudflared:
  image: cloudflare/cloudflared:latest
  command: tunnel --no-autoupdate --config /etc/cloudflared/config.yml run
  volumes: ["./tunnel:/etc/cloudflared:ro"]
  networks: [default]
  restart: "no"
```

Locally-managed (config.yml + credentials.json mounted) rather than dashboard-token based: ingress rules stay versioned in the repo, the tunnel UUID is stable across container recreations, and no Zero Trust UI steps are needed to reproduce. `testenv/tunnel/config.yml` is versioned with `ingress: [hostname → http://caddy:80, 404]`; `credentials.json` is gitignored and provisioned once via `cloudflared tunnel create`. No ports are published — cloudflared connects outbound. The test Caddyfile declares a tunnel host with `tunnel <uuid>` (no `route dns` bootstrap), so the plugin creates the CNAME itself — that is the end-to-end behavior under test.

### D9: Validation summary (adapt time)

- `tunnel <uuid>` — exactly one arg, canonical UUID format.
- Error if `tunnel` coexists with `ip`, `ip6`, or `proxied`.
- `tunnel` + `force_adopt` allowed (D4 semantics).
- Zone assignment unchanged (host must resolve to a declared zone).

## Risks / Trade-offs

- **[Stray owned CNAMEs pruned] →** documented consequence of D6; same ownership rules as A/AAAA. Users keeping hand-made CNAMEs at managed names must not tag them.
- **[D4 deletes owned A/AAAA on first tunnel run] →** intentional migration path; untagged records are never deleted without `force_adopt`. Warning logged with record IDs.
- **[CNAME creation fails if untagged A/AAAA still present] →** clear error message naming the conflicting record and the `force_adopt` remedy.
- **[Tunnel UUID typo] →** CNAME is created but traffic black-holes (edge has no such tunnel). Mitigation: UUID format validation at adapt time; runtime existence check deferred (Non-Goal). Cloudflare's API rejects CNAME creation to nonexistent `<uuid>.cfargotunnel.com` targets in proxied mode in most cases, which surfaces as a reconcile error.
- **[testenv credentials leak] →** `testenv/tunnel/credentials.json` gitignored; compose fails fast with a clear message if missing (cloudflared exits with config error).
- **[Prune in a zone with only tunnel hosts] →** prune deletes owned A/AAAA leftovers from pre-tunnel era; required behavior, covered by tests.

## Migration Plan

No config migration: `tunnel` is additive; existing Caddyfiles parse identically. Record migration (A → CNAME) is handled by D4 at reconcile time. Rollback = revert config to the previous host form; the plugin re-creates the A record on the next reload (previous owned CNAME gets pruned if prune is enabled, else left and warned).

## Open Questions

- None blocking. Tunnel-name resolution (D1 alternative) and tunnel health checks are candidate future changes.
