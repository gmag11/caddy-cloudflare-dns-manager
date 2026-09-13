## Why

Managing the DNS records of a zone currently requires manual, out-of-band edits at Cloudflare every time a site's subdomain or its target IP changes. A Caddy server that serves many subdomains of a domain (often behind a `*.domain` wildcard with per-host routing) needs a plugin that guarantees each declared public subdomain resolves to the intended IP, without a separate DNS admin step and without touching records that belong to other instances or were managed by hand.

## What Changes

- Introduce a Caddy module that adds a `cf_dns_manager` directive usable in two contexts: a global options block (zone registry + global settings) and per site/handle (a host declaration to reconcile).
- Support multiple Cloudflare zones; each zone must be declared together with its API token. Using `cf_dns_manager` with a host whose zone is not declared is a config error.
- Reconcile A records for each declared host on every config load: create missing records, update records owned by this plugin whose IP or proxy mode changed. IPv4 only; no AAAA handling.
- Determine a host's IP as: an explicit `ip` override when present, otherwise the server's public IPv4 detected from a configurable endpoint (default Cloudflare's own trace endpoint), detected once per reload and shared by all auto hosts.
- Treat IP detection failure as non-blocking: warn, do not change existing auto-host records, skip new auto-host records, but still reconcile hosts that have an explicit IP.
- Default to Cloudflare "proxied" (orange cloud) records; force DNS-only when the effective IP is private/reserved; allow an explicit per-host `proxied` override subject to that rule.
- Resolve host declarations via a named matcher reference (`host @foo`) resolved at Caddyfile adapt time, or a literal FQDN. A matcher that does not map to exactly one literal host is an error.
- Tag every record this plugin creates with a configurable prefix + instance identifier. Conservatively leave records without the plugin's tag untouched unless an explicit per-site `force_adopt` is set.
- Support optional per-zone orphan cleanup (`prune`) that only deletes records carrying this instance's tag and no longer declared, never records of other instances or manual ones.
- Manage apex names, first-level subdomains, and deeper sub-subdomains within declared zones.

## Capabilities

### New Capabilities

- `caddyfile-config`: Caddyfile surface of the plugin — the global `cf_dns_manager` options block (zone registry with per-zone API tokens, `prune`, `ip_url`, `tag_prefix`, `instance`), the per-site/handle `cf_dns_manager` directive (`host @ref`/FQDN, `ip`, `proxied`, `force_adopt`), placement rules, and adapt-time validation errors.
- `public-ip-detection`: how the server's public IPv4 is obtained on each reload — configurable endpoint defaulting to Cloudflare's trace endpoint, single detection per load, and non-blocking failure semantics.
- `dns-record-reconciliation`: the per-host reconcile behavior against Cloudflare zones — effective IP selection, zone assignment by longest-suffix match against declared zones, create/update of A records, proxied vs DNS-only rules (private IP forces DNS-only), and apex/sub-domain naming.
- `record-ownership`: ownership model for Cloudflare records — tag prefix + instance id, conservative default that never modifies untagged records, per-site `force_adopt`, and per-zone `prune` that only removes this instance's orphaned records.

### Modified Capabilities

None. This is a greenfield plugin; no existing specs change.

## Impact

- **New code**: a Caddy plugin module (Go) implementing a Caddyfile adapter directive pair, an HTTP handler or app hook triggered on config load, and a Cloudflare REST API client for zone/record operations.
- **Dependencies**: Caddy module API (`caddy`/`caddyconfig`), Cloudflare API; standard `net/http` for IP detection.
- **Configuration**: new Caddyfile global options and per-site directive shape; credentials supplied via env-var placeholders in the Caddyfile.
- **Systems**: Cloudflare zones managed via the provided API tokens. The plugin only ever acts on records it can attribute to itself (or that `force_adopt` claims), and never on TXT records used by ACME DNS-01 challenges.
