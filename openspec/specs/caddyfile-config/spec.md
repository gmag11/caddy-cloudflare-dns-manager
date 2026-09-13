# caddyfile-config Specification

## Purpose

Defines the Caddyfile surface of the DNS manager plugin: how zones and credentials are registered in the global options block and how individual hosts opt in to DNS management with per-site directives.

## Requirements

### Requirement: Global zone registration

The plugin SHALL provide a `cf_dns_manager` global options block in which each managed Cloudflare zone is declared with its API token. A zone used by any host MUST be declared in this block; referencing an undeclared zone SHALL be a Caddyfile configuration error.

#### Scenario: Zone declared with token

- **WHEN** a Caddyfile defines a global options block containing `cf_dns_manager { zone example.com api_token <TOKEN> }`
- **THEN** the plugin registers `example.com` as a managed zone associated with the given token

#### Scenario: Zone not declared

- **WHEN** a per-site directive references a host under a zone that was not declared in the global `cf_dns_manager` block
- **THEN** adapting the Caddyfile fails with an error identifying the zone and the offending site

### Requirement: Global IPv6 detection endpoint option

The plugin SHALL accept an optional `ip6_url` subdirective in the global `cf_dns_manager` block specifying the endpoint used to detect the host's public IPv6 address. When unset, a built-in IPv6-only default endpoint SHALL be used.

#### Scenario: Custom ip6_url

- **WHEN** the global block declares `ip6_url https://v6.example.net/ip`
- **THEN** IPv6 detection queries that endpoint

#### Scenario: ip6_url default

- **WHEN** the global block declares no `ip6_url`
- **THEN** IPv6 detection uses the built-in default endpoint

### Requirement: Per-site host opt-in directive

The plugin SHALL provide a `cf_dns_manager` directive usable inside a site block or a route/handle block to declare a single host for DNS reconciliation. The directive SHALL accept either a named matcher reference (`host @foo`) or a literal FQDN. A host is only reconciled when its `cf_dns_manager` directive is present; hosts without the directive are never touched.

#### Scenario: Host referenced by named matcher

- **WHEN** a site defines `@foo foo.example.com` and a `cf_dns_manager { host @foo }` directive
- **THEN** the plugin reconciles the DNS record for `foo.example.com`

#### Scenario: Host referenced by literal FQDN

- **WHEN** a `cf_dns_manager { host bar.example.com }` directive is present inside a site block
- **THEN** the plugin reconciles the DNS record for `bar.example.com`

#### Scenario: Host without directive is ignored

- **WHEN** a subdomain is routed but has no `cf_dns_manager` directive
- **THEN** the plugin takes no action for that host and leaves any existing Cloudflare record untouched

### Requirement: Matcher resolution to a single literal host

The plugin SHALL resolve a `host @ref` reference to exactly one literal hostname. If the referenced matcher does not exist, does not use a `host` matcher, or matches more than one host, adapting the Caddyfile SHALL fail with an error explaining the ambiguity.

#### Scenario: Matcher maps to multiple hosts

- **WHEN** a `cf_dns_manager { host @both }` directive references a matcher defined as `@both host a.example.com b.example.com`
- **THEN** adaptation fails with an error stating that the matcher must map to exactly one host

#### Scenario: Matcher without host rule

- **WHEN** a `cf_dns_manager { host @pathonly }` directive references a matcher that only uses a `path` matcher
- **THEN** adaptation fails with an error stating the matcher has no single host mapping

### Requirement: Per-host IP override

The plugin SHALL accept an optional `ip` subdirective inside a per-site `cf_dns_manager` directive. When present, that literal IPv4 is used for the host's A record; when absent, the plugin uses the detected public IPv4 (see public-ip-detection). The `ip` value MUST be an IPv4 address; IPv6 values are rejected with an adaptation error. A `cf_dns_manager` directive SHALL manage exactly one host; multiple hosts with different IPs are expressed by repeating the directive.

#### Scenario: IP override provided

- **WHEN** a directive declares `cf_dns_manager { host @foo ip 192.0.2.10 }`
- **THEN** the plugin reconciles `foo`'s A record to `192.0.2.10`

#### Scenario: IP override is IPv6

- **WHEN** a directive declares `ip 2001:db8::1`
- **THEN** adapting the Caddyfile fails with an error stating `ip` requires an IPv4 address

### Requirement: Per-host proxy mode

The plugin SHALL accept an optional `proxied yes|no` subdirective inside a per-site `cf_dns_manager` directive, defaulting to `yes`. The value SHALL apply to both the A and the AAAA record of the host. Cloudflare's inability to proxy private addresses still forces DNS-only per family (see dns-record-reconciliation).

#### Scenario: Proxied applies to both families

- **WHEN** a host declares `proxied yes` with both A and AAAA managed and both effective IPs public
- **THEN** both records are created or updated proxied

#### Scenario: Proxied no applies to both families

- **WHEN** a host declares `proxied no` with both A and AAAA managed
- **THEN** both records are created or updated DNS-only

### Requirement: Per-host IPv6 mode subdirective

The plugin SHALL accept an optional `ip6` subdirective inside a per-site `cf_dns_manager` directive with exactly one of: `false` (or `no`/`off`), `auto`, or a literal IPv6 address. The default SHALL be `false` and the absence of the subdirective SHALL be identical to `ip6 false`. When set to a literal address, the plugin SHALL manage an AAAA record with that address regardless of the host's own IPv6 capability. When set to `auto`, the plugin SHALL manage an AAAA record using the detected public IPv6. When set to `false`, the plugin SHALL NOT create, update, or adopt any AAAA record for the host.

#### Scenario: ip6 auto declared

- **WHEN** a directive declares `cf_dns_manager { host @foo ip6 auto }`
- **THEN** the plugin manages an AAAA record for `foo` using the detected public IPv6

#### Scenario: ip6 literal declared

- **WHEN** a directive declares `cf_dns_manager { host @foo ip6 fd7a:115c:a1e0::1 }`
- **THEN** the plugin manages an AAAA record for `foo` with content `fd7a:115c:a1e0::1`

#### Scenario: ip6 absent or false

- **WHEN** a directive declares no `ip6` subdirective or `ip6 false`
- **THEN** the plugin takes no action on any AAAA record for that host

#### Scenario: ip6 invalid value

- **WHEN** the `ip6` subdirective has a value that is neither false/no/off, `auto`, nor a valid IPv6 address
- **THEN** adapting the Caddyfile fails with an error naming the offending value

### Requirement: Adopt override directive

The plugin SHALL accept an optional `force_adopt` subdirective inside a per-site `cf_dns_manager` directive. When present, the plugin MAY update or tag a Cloudflare record that lacks this plugin's ownership tag even though the conservative default would leave it untouched.

#### Scenario: Force adopt an untagged record

- **WHEN** a site declares `cf_dns_manager { host @foo force_adopt }` and `foo.example.com` already exists in Cloudflare without the plugin's tag and with a different IP
- **THEN** the plugin updates the record to the configured IP and tags it as owned

#### Scenario: Untagged record without force_adopt

- **WHEN** a site declares `cf_dns_manager { host @foo }` and the existing record lacks the plugin's tag
- **THEN** the plugin leaves the record unchanged

### Requirement: Scope of the per-site directive

The per-site `cf_dns_manager` directive SHALL be usable both in dedicated site blocks (including apex domains) and inside `handle` blocks under a wildcard site. Zone assignment for a declared host SHALL use the longest matching declared zone suffix.

#### Scenario: Apex site block

- **WHEN** a Caddyfile has a dedicated site block `example.com` containing `cf_dns_manager`
- **THEN** the plugin reconciles the apex record for `example.com`

#### Scenario: Subdomain inside a wildcard handle

- **WHEN** a `handle` block under `*.example.com` contains `cf_dns_manager { host @foo }`
- **THEN** the plugin reconciles `foo.example.com` within the `example.com` zone

#### Scenario: Longest zone suffix match

- **WHEN** a host `a.app.example.com` is declared and both `example.com` and `app.example.com` are declared zones
- **THEN** the record is reconciled in the `app.example.com` zone
