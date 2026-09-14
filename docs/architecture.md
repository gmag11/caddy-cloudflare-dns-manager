# Architecture

How the plugin works internally: the module layout, the config flow, and the
reconciliation model. The normative behavior lives in `openspec/specs/`; this
document explains *how* it is implemented.

## Module layout

Two registered Caddy modules plus two registered Caddyfile hooks
(`app.go:init()`):

```
┌────────────────────────────────────────────────────────────────────┐
│ Caddyfile                                                          │
│   cf_dns_manager { zone ... }      (global option)                 │
│   cf_dns_manager { host ... }      (per-site directive, N times)   │
└──────────────┬─────────────────────────────┬───────────────────────┘
               │ parseGlobalOption           │ parseDirective
               ▼                             ▼
     App (apps.cf_dns_manager)     hostHandler (http.handlers.cf_dns_manager_host)
     zones, tokens, settings       no-op middleware carrying one HostConfig
               │                             │ Provision(): app.addHost(hc)
               │                             ▼
               │                    host registered on the app
               ▼
     App.Start() → Reconcile(all hosts) → per-zone goroutines
```

### Why two modules

- **The app** (`cf_dns_manager`, empty namespace) holds zones, tokens and
  settings, and owns the reconcile lifecycle. It is a `caddy.App`: `Start()` runs
  on every config load, `Stop`/`Cleanup` are no-ops.
- **The handler** (`http.handlers.cf_dns_manager_host`) exists so the directive
  is a valid route member. Per-site directives must live inside site/handle
  blocks to resolve matchers (`@foo`) in scope — including matchers inherited
  from the enclosing site. Its `ServeHTTP` passes through untouched; its real
  job is `Provision()`: registering its `HostConfig` with the app.

The handler carries its `HostConfig` through the frozen JSON config (that is why
`HostConfig` has JSON tags and why unknown fields must not break old configs).
The app's `hosts` slice accumulates registrations during provisioning
(`addHost`, mutex-guarded because Caddy may provision across goroutines) and
`hostsSnapshot()` copies them out for `Start()`.

### Config flow recap

1. Adapt time: global option produces the app JSON; each directive resolves its
   host (matcher or literal) to an FQDN, assigns the zone (longest-suffix match
   over declared zones — an undeclared zone is an adapt error) and validates
   (`tunnel` exclusivity, UUID shape, IPv4/IPv6 shapes).
2. Provision: the app is created; handlers register hosts on it.
3. `Validate()` checks the global config (zones have names/tokens, tag prefix
   and instance non-empty).
4. `Start()` snapshots hosts and reconciles once per config load. It runs even
   with zero hosts so prune-only zones still clean up.

## Reconciliation model

Everything hangs off one primitive: **a (name, record type) pair, owned if the
record's comment equals `<tag_prefix>:<instance>` exactly.**

`Reconcile` (reconcile.go):

```
Reconcile(hosts)
├─ detectPublicIPs()            once per family, parallel, failures are soft
├─ group hosts by zone          zone assignment recomputed from declared zones
│                               (HostConfig.ZoneConfig is not serialized)
└─ per zone (goroutine):
   reconcileZone(zone, hosts)
   ├─ zoneIDByName              cached per run, per zone
   ├─ listRecords               A/AAAA/CNAME only, client-side filtered,
   │                            paginated (100/page)
   ├─ byName[type][key]         index by canonical zone-relative name
   ├─ per host:
   │   ├─ tunnel?  → reconcileTunnelHost()
   │   │             clear conflicting A/AAAA (clearConflictingRecords)
   │   │             managed[key]["CNAME"]
   │   │             reconcileFamily(CNAME, <uuid>.cfargotunnel.com, proxied=true)
   │   └─ normal:  → clear conflicting CNAME
   │                managed[key]["A"] (+ ["AAAA"] if ip6)
   │                reconcileFamily(A, ...), reconcileFamily(AAAA, ...)
   └─ pruneZone()               if the zone opted in
```

### Per-host record plan

The declared config defines, per host, which (name, type) pairs are *managed*.
Tunnel hosts manage exactly CNAME; normal hosts manage A always and AAAA when
IPv6 is enabled. The managed set drives both writes and prune eligibility — it
is derived from config, never from whether a write succeeded, so a transient
detection failure cannot cause a prune.

### reconcileFamily: the write path

Given (host, type, desired content, proxied, existing records of that type):

```
existing?
  ├─ owned by me   → update only on drift (content or proxied); idempotent
  ├─ untagged      → force_adopt? adopt (overwrite + retag) : leave + log
  └─ other tag     → never touched
none → create (tagged)
```

Proxy mode is computed per family by `effectiveProxied`: the declared value,
forced DNS-only for private/reserved addresses (RFC1918, CGNAT, ULA, link-local,
loopback). CNAME records bypass this entirely — the tunnel branch passes
`proxied=true` directly, because `effectiveProxied` would classify the
`<uuid>.cfargotunnel.com` content as an unparseable ("private") address and
force DNS-only, silently breaking the tunnel.

### clearConflictingRecords: the migration primitive

Cloudflare forbids a CNAME coexisting with A/AAAA at one name (creation is
rejected with error 81054). Switching a host between tunnel and address
therefore requires deleting the other kind *before* creating the new one —
prune runs too late in the same run, and Caddy does not re-run an identical
config ("config is unchanged"), so a failed transition would leave the name
empty until a restart.

The helper deletes records at the name whose type is in the blocking set:
owned ones unconditionally, untagged ones only under `force_adopt` (otherwise a
descriptive error and nothing is deleted). Deleted entries are removed from the
records snapshot so the subsequent prune does not retry them.

### Prune

`pruneZone` walks the listed records and deletes those that are (a) tagged with
this instance and (b) whose (name, type) is not in the managed set. Untagged
records and other instances' records are never candidates. It runs after host
reconciliation, per zone, only when that zone declared `prune`.

## The Cloudflare client

`cloudflareClient` is a minimal REST client: bearer token, JSON envelope
parsing, 15s timeout, 429 surfaced as a typed error, defensive pagination.
No retries — reconcile is idempotent and re-run on the next reload. `apiBase`
is a private test seam; production code always uses the real endpoint.

The ownership tag travels in the record `comment` field (Cloudflare allows one
comment per record), which is why every write sets `Comment: tag`.

## Concurrency notes

- One goroutine per zone; zones are independent (separate tokens, separate
  records). Failures are collected and joined into one error at the end, but
  Caddy logs per-host failures and still starts.
- `addHost`/`hostsSnapshot` are mutex-guarded: provisioning can be concurrent.
- No long-lived state: every reload re-lists everything. There is no cache to
  invalidate and no background loop — the config *is* the desired state.

## Where to change what

| You want to... | Touch |
| --- | --- |
| add/validate a subdirective | `caddyfile.go` (`parseHostBlock`), spec deltas |
| change write/adopt/prune behavior | `reconcile.go` (+ `mockcf_test.go` first) |
| change what gets listed | `cloudflare.go` filter + tests |
| add config fields | `app.go` `HostConfig`/`App` with JSON tags (back-compat!) |
| new user-facing behavior | OpenSpec change first (`openspec/`), then code |
