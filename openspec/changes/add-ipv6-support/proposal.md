## Why

The plugin is IPv4-only: it manages A records and detects only the server's public IPv4. Servers with IPv6 connectivity cannot publish AAAA records through the plugin, so IPv6-only clients reach those hosts over IPv4 or not at all. Adding per-host IPv6 support brings the plugin to parity with what Cloudflare and dual-stack clients expect.

## What Changes

- New `ip6` subdirective in the per-site `cf_dns_manager` directive, per host:
  - `ip6 false` (default): no AAAA is configured for the host; behavior identical to today.
  - `ip6 auto`: the plugin detects the host's public IPv6 and manages an AAAA record for it.
  - `ip6 <IPv6>`: the plugin manages an AAAA record with the literal address (manual override, mirrors `ip`).
- Independent public IPv6 detection in parallel with IPv4 detection, via a configurable `ip6_url` global option (default: a v6-only endpoint). Failure of either family is soft and independent: no record created, existing record left unchanged, warning logged.
- Reconciliation becomes per (record name, type): a host with both families active manages an A and an AAAA record together. One `proxied` setting applies to both families.
- Mixed-family proxy states (e.g. public IPv4 + private IPv6) are supported; when one family's effective IP is private that record is forced DNS-only and the plugin logs a warning about the mixed proxied state.
- Disabling IPv6 for a host (`ip6 false` or removing the `ip6` line) marks the AAAA eligible for prune: zones with `prune` delete the tagged AAAA, zones without leave it orphaned. Transitive detection failure never triggers deletion.
- Adoption, ownership tagging, and force-adopt operate per record type: an untagged AAAA is never adopted because the plugin owns the A of the same name, and vice versa.
- **BREAKING (internal)**: record listing changes from a single `type=A` query to an unfiltered paginated query filtered client-side by type; the reconciliation map keys become (name, type). No Caddyfile breaking change: existing configs adapt identically (empty `ip6` behaves as `ip6 false`).
- Fixes a latent bug: `isPrivateIP` currently classifies every IPv6 address as private; IPv6 classification (ULA fc00::/7, link-local fe80::/10, loopback) is implemented properly.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `caddyfile-config`: new `ip6` per-host subdirective (`false|auto|<IPv6>`) and new `ip6_url` global option; the `ip` subdirective keeps IPv4-only validation.
- `dns-record-reconciliation`: reconciliation now covers A and AAAA records per host, keyed by (name, type); proxied semantics extended to IPv6 with forced DNS-only for private v6 and a mixed-family warning.
- `public-ip-detection`: parallel IPv6 detection with independent soft-failure semantics; the existing IPv4-only detection requirement is superseded by family-specific detection.
- `record-ownership`: ownership tagging, untagged adoption, and per-zone prune extended to AAAA records, with prune eligibility driven by declared configuration (explicit `ip6 false`), never by transitive detection failure.

## Impact

- Code: `caddyfile.go` (parsing/validation), `app.go` (HostConfig fields, App config), `reconcile.go` (per-type reconciliation, prune, proxied logic, isPrivateIP), `cloudflare.go` (record listing), `publicip.go` (family-aware detection), tests across all of these, README.
- Cloudflare API: same endpoints; one unfiltered list query per zone instead of a type=A query (no call-count increase).
- Compatibility: existing Caddyfiles keep working unchanged; existing plugin-tagged A records keep their comment tag. No state migration needed.
