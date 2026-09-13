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

The plugin SHALL accept an optional `ip` subdirective inside a per-site `cf_dns_manager` directive. When present, that literal IPv4 is used for the host's record; when absent, the plugin uses the detected public IPv4 (see public-ip-detection). A `cf_dns_manager` directive SHALL manage exactly one host; multiple hosts with different IPs are expressed by repeating the directive.

#### Scenario: IP override provided

- **WHEN** a site declares `cf_dns_manager { host @tail ip 100.64.10.5 }`
- **THEN** the plugin reconciles that host's record to the literal IP `100.64.10.5`

#### Scenario: No IP override provided

- **WHEN** a site declares `cf_dns_manager { host @foo }` with no `ip`
- **THEN** the plugin reconciles that host to the current detected public IPv4

### Requirement: Per-host proxy mode

The plugin SHALL accept an optional `proxied` subdirective with values `yes`/`no`, defaulting to proxied. A `proxied yes` requests a Cloudflare proxied (orange-cloud) record; `proxied no` requests DNS-only. Records whose effective IP is private or reserved SHALL always be created DNS-only regardless of the directive.

#### Scenario: Proxied default

- **WHEN** a site declares `cf_dns_manager { host @foo }` with a public IP and no `proxied`
- **THEN** the record is created or updated as proxied

#### Scenario: Explicit DNS-only

- **WHEN** a site declares `cf_dns_manager { host @foo proxied no }` with a public IP
- **THEN** the record is created or updated as DNS-only

#### Scenario: Private IP forces DNS-only

- **WHEN** a site declares `cf_dns_manager { host @tail ip 100.64.10.5 }` without `proxied` and Cloudflare would reject proxying a private IP
- **THEN** the plugin still reconciles the record but as DNS-only

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
