## Context

The repo is greenfield (no code yet, no `openspec/specs` beyond scaffolding). The plugin must integrate with Caddy's module system: a Caddyfile adapter (`caddyconfig/httpcaddyfile`) providing two `dns_manage` spellings (global options + per-site route directive), plus a module that runs reconciliation on config load. Target zone provider is Cloudflare via its REST API. See proposal.md - Why and the delta specs for the behavioral contract.

Key Caddy constraints that shape the design:

- The Caddyfile adapter has two separate registration namespaces. A directive can be registered both as a global option (`RegisterGlobalOption`) and as a route directive (`RegisterDirective`) under the same name without collision, because dispatch depends on where the token appears (global options block vs. site block).
- Named matchers (`@foo`) exist only during Caddyfile adaptation. Once compiled to JSON the names are gone, so name-to-host resolution must happen in the adapter.
- A wildcard site (`*.example.com`) is a valid route host; per-host routing is expressed with named matchers + `handle`. DNS management is opt-in per host via an explicit directive (never inferred from site addresses, never a zone sweep).

## Goals / Non-Goals

**Goals:**
- Define two adapter entry points under one directive name (`dns_manage`) with the per-host spelling usable both in dedicated site blocks and inside `handle` blocks.
- Resolve every declared host to a zone + relative record name at adapt time, failing loudly on undeclared zones, ambiguous matchers, or non-IPv4 overrides.
- Reconcile Cloudflare A records on every config load with ownership tagging, conservative default, per-site `force_adopt`, and per-zone `prune`.
- Keep the failure semantics for public-IP detection non-blocking (see specs/public-ip-detection).

**Non-Goals:**
- No IPv6/AAAA record handling (IPv4-only, per decision).
- No provider other than Cloudflare.
- No TXT/ACME record management; ACME DNS-01 challenges via the built-in `tls_dns` remain a separate concern.
- No automatic zone sweep / reconciliation of hosts without an explicit directive.
- No deletion of records not tagged with this instance.

## Decisions

### D1. Two adapter registrations under one name, no conflict

Register `dns_manage` twice: as a global option (namespace `caddyconfig/httpcaddyfile` global options) and as an HTTP route directive. Context decides which parser runs. The route-directive form carries an ordered, low-priority placement that does not interfere with request handling (the directive only needs its `Provision`/config-load hook).

*Alternatives considered:* a distinct global-only directive name plus a per-site one. Rejected: user explicitly wanted the same name in both contexts and Caddy supports it cleanly.

### D2. Host resolution at adapt time; single-host contracts

The per-site directive accepts either `@ref` or a literal FQDN. Because named matchers vanish after adaptation, `@ref` is resolved in the adapter against the matcher definitions visible in scope. Each `dns_manage` manages exactly one host: a matcher that maps to multiple hosts, or to no `host` rule, is an adapt-time error (specs/caddyfile-config). Multiple hosts are managed by repeating the directive.

*Risk flagged for spike:* whether a `dns_manage` nested inside a `handle` can see the site block's named matchers in the adapter's scope model. Fallback is requiring the directive at a scope where its `@ref` is visible, or using a literal FQDN.

### D3. Ownership via record comment tag

Each created/adopted record gets a Cloudflare record `comment` of `<tag_prefix>:<instance>`. `tag_prefix` (default e.g. `caddy-dns`) and `instance` (default hostname) are global options. The tag is the single source of truth for "is this ours?"; prune deletes only records whose tag carries this instance. This makes multi-instance coexistence on one zone safe by construction.

*Alternatives considered:* Cloudflare record `tags`. Rejected: not supported on all record/zone configurations as uniformly as comment; comment is a stable scalar.

### D4. Conservative default + explicit `force_adopt`

Default policy per existing untagged record: skip (log). `force_adopt` on a site upgrades it to update-and-claim. Deleting is reserved for own-tagged orphans under `prune`. This preserves the user's cross-instance safety requirement.

### D5. Prune is per-zone, opt-in, own-instance only

`prune` is a zone-level marker in the global block. When set, after the forward reconciliation the plugin lists that zone's A records and deletes any whose tag is `<prefix>:<this instance>` and whose host is not in the current declared set. Records without a tag or with a different instance are never candidates.

### D6. Effective IP and proxy-mode resolution

For each declared host: use `ip` override if present, else the shared detected public IPv4. Proxy mode: `proxied` defaults to yes; a private/reserved effective IP forces DNS-only regardless of the directive. This mirrors what Cloudflare accepts (a proxied record cannot target a private IP).

### D7. Single public-IP detection per load, non-blocking

Detection runs once per config load and the value is shared by all auto-IP hosts. Configurable `ip_url`; default Cloudflare's own `https://cloudflare.com/cdn-cgi/trace`. On failure/non-IPv4: log warning, leave existing auto records, skip creating new auto records, still reconcile explicit-IP hosts. No local state of "last known IP" is persisted; Cloudflare remains the source of truth and is simply left untouched on failure.

### D8. Zone assignment by longest-suffix match, apex via root record

Declared zones form the managed set; each host maps to the longest matching declared zone suffix; host names are converted to relative record names (`foo` for `foo.example.com`, `@`/root for the apex, `a.b` for nested). Wildcard site addresses are never turned into records.

## Risks / Trade-offs

- [Matcher visibility from nested `handle` at adapt time may differ across Caddy versions] -> Spike before implementation; if `@ref` is not visible, require the directive where its matcher is in scope or use literal FQDN; error message guides the user.
- [Detection endpoint is an external dependency; outage would otherwise wedge reloads] -> Non-blocking semantics + warning; config still applies; hosts with explicit IPs still reconcile. Trade-off: auto-IP hosts silently keep stale records during an outage.
- [`force_adopt` + later `prune` on another instance can race] -> Prune only deletes own-instance tags, so a cross-instance window only exists if an admin force-adopts a record that another instance still declares; documented as operational, no code guard.
- [Private-IP proxied requests get forced to DNS-only silently] -> Deliberate, matches Cloudflare API constraints; surfaced in logs and specs.
- [Deleting orphans can briefly drop DNS for a host being moved between instances] -> Acceptable; prune is opt-in per zone and own-instance only.

## Migration Plan

- Greenfield: no existing deployment to migrate. First release introduces the directive shape; users adopt hosts gradually because behavior is opt-in and conservative by default.
- Rollback: removing `dns_manage` directives (and any `prune` markers) stops all reconciliation; previously created records remain in Cloudflare unless the user removes them manually, so DNS is never broken by rollback alone.

## Open Questions

- Whether to back `dns_manage`'s route directive with a no-op middleware vs. a tiny dedicated HTTP app whose `Start`/config hook performs reconciliation. Resolvable during implementation without changing specs.
- Exact Cloudflare endpoint/rate assumptions for the default IP trace endpoint. Resolvable at implementation time; the endpoint is configurable regardless.
