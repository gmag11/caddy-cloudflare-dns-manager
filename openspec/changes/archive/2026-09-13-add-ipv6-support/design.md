## Context

The plugin reconciles Cloudflare A records for hosts declared in the Caddyfile. Everything assumes one family: `detectPublicIPv4` returns a single address, `HostConfig` has one `IP`, `reconcileOne` handles one record per host with `Type: "A"` hardcoded, `listRecords` queries `type=A`, prune walks A records only, and `effectiveProxied`/`isPrivateIP` treat any non-IPv4 address as private (latent bug: every IPv6 would be forced DNS-only).

The user surface discussed and agreed in exploration:

```
cf_dns_manager {
    host app.example.com
    ip 203.0.113.10      # optional, IPv4 → A record
    ip6 auto             # false (default) | auto | <literal IPv6>
    proxied yes
}
```

## Goals / Non-Goals

**Goals:**

- Per-host AAAA management: `ip6 false|auto|<IPv6>` (default `false`; absence of the subdirective is identical to `false`).
- Dual-family reconciliation under one `proxied` value, with per-family forced DNS-only for private addresses.
- Independent, parallel, soft-failing detection for each family.
- Ownership, adoption, and prune semantics that work per record type.
- No breaking change to existing Caddyfiles or to the meaning of existing records.

**Non-Goals:**

- Global (app-level) default `ip6` inherited by all hosts — rejected; `ip6` is per subdomain only.
- `proxied6` or any per-family proxy override.
- Wildcard AAAA records, or management of record types beyond A/AAAA.
- Detecting "host has IPv6" via interface scanning (see D4).

## Decisions

### D1: Single `ip6` knob; no separate `ip6` address field

`ip6` is one subdirective with three forms: `false` (default), `auto`, or a literal IPv6 address. An earlier idea of `ip6 <literal>` plus a separate on/off flag was collapsed into the single knob because the literal already implies "enabled" and two ways to say the same thing would drift.

### D2: Per-family detection with dedicated endpoints, run in parallel

- v4: existing `ip_url` (default Cloudflare trace).
- v6: new `ip6_url` global option, default a v6-only endpoint (e.g. `https://api6.ipify.org`). v6-only by design: a returned v4 address is then unambiguously a wrong/fallen-back endpoint and is rejected by the parser, instead of silently creating an invalid AAAA.
- Both detections run concurrently per reconcile; the v6 request gets a short dedicated timeout (3–5 s). Since they run in parallel this does not slow the reload in the common case, and it bounds the "interface has GUA but router is dead" blackhole case that no other check can catch cheaply.
- Failure of one family is independent: skip that family's record, warn, leave existing records untouched. Mirrors existing v4 semantics.

### D3: Reconciliation keyed by (name, type)

- `listRecords` drops the `type=A` filter: one paginated query per zone, filtered client-side by type. Same number of API calls as today, and both families arrive in one pass.
- The existing-records map becomes `map[nameKey]map[type][]cfDNSRecord`.
- `reconcileOne` runs once per enabled family. The owned/untagged search filters strictly by record type: the plugin never adopts or overwrites an A because it manages the AAAA of the same name, or vice versa (a cross-family PUT would be a type-mismatch API error anyway).
- Ownership tag is unchanged and identical on A and AAAA records: it identifies the instance, not the family.

### D4: No interface scan for IPv6 capability

Considered checking `net.Interfaces` for any non-loopback, non-link-local v6 as a fast-fail before detection. Rejected:

- A zero-v6 host already fails detection instantly (`ENETUNREACH` — no route, no timeout) or falls back to v4 and the parser rejects the wrong family. The scan adds a third source of truth without changing outcomes.
- False negative: kernels booted with `ipv6.disable=1` (common with Tailscale userspace networking) show no v6 interfaces, so `ip6 auto` would be permanently dead even though the tunnel works.
- Requirement: if a fast-path capability check is ever added, it MUST produce exactly the detection-failure semantics (skip + warn + spare), never a distinct state.

### D5: Prune eligibility follows declared configuration, not detection outcomes

The subtle trap of this change. Today, a skipped v4 host still marks its name as reconciled, so prune spares it. A naive per-type refactor loses that. Rule made explicit:

```
Tagged AAAA in a prune-enabled zone:
  host removed from Caddyfile        → PRUNE (config change)
  ip6 false (explicit or default)    → PRUNE (config change)
  ip6 auto, detection/capability skip→ SPARE (transient; NIC lost v6,
                                       router down — never destructive)
  ip6 auto, managed normally         → SPARE (in reconciled set)
  AAAA without this instance's tag   → untouched, always
```

In prune-enabled zones, a host with `ip6 false` whose tagged AAAA exists gets it deleted (config says: not managing v6). In non-prune zones the same record is left orphaned in place — prune remains the only destructor.

### D6: Proxied semantics for IPv6

- One `proxied` value per host block, applied to both families; no `proxied6`.
- `isPrivateIP` gains real IPv6 classification: ULA `fc00::/7`, link-local `fe80::/10`, loopback `::1`. Public v6 (2000::/3) is proxyable. This fixes the latent bug where every IPv6 was classified private.
- Forced DNS-only applies per family based on that family's effective IP — this is what makes the documented Tailscale case work (public A proxied + ULA AAAA DNS-only) and avoids Cloudflare API errors from proxying unroutable addresses.
- Mixed proxied state (one family proxied, the other DNS-only) is supported and warned at log level when detectable: at adapt time when both literals are declared, at reconcile time when both families resolve and their classifications differ. When a family is absent or skipped there is nothing to compare and only the normal skip warning appears.

### D7: Manual literal never blocked by capability

`ip6 fd7a:115c:a1e0::1` on a host with no v6 interface is allowed — the user asked for it explicitly, mirroring the existing `ip 100.64.x.x` Tailscale override for v4. Only `ip6 auto` depends on the host actually having working IPv6.

## Risks / Trade-offs

- [Refactor of reconcile/prune loses the "skip ⇒ spare" behavior] → D5 is written as an explicit rule; tests must cover: detection failure spares tagged AAAA in prune-enabled zones, `ip6 false` prunes it.
- [Unfiltered list query returns CNAME/MX/etc. noise] → client-side filter keeps only A/AAAA; name normalization logic is unchanged.
- [v6 detection endpoint unreachable on dual-stack hosts due to Happy Eyeballs fallback returning v4] → parser rejects non-v6 bodies for `ip6_url`; treated as detection failure (skip + warn), same as no route.
- [Mixed proxied states confuse users] → warning at adapt/reconcile time spells out per-family outcome.
- [Existing frozen configs (route JSON with old HostConfig)] → new fields are empty by default; `ip6` empty == `false`. No migration.
