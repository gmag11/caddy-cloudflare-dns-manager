## ADDED Requirements

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

### Requirement: Global IPv6 detection endpoint option

The plugin SHALL accept an optional `ip6_url` subdirective in the global `cf_dns_manager` block specifying the endpoint used to detect the host's public IPv6 address. When unset, a built-in IPv6-only default endpoint SHALL be used.

#### Scenario: Custom ip6_url

- **WHEN** the global block declares `ip6_url https://v6.example.net/ip`
- **THEN** IPv6 detection queries that endpoint

#### Scenario: ip6_url default

- **WHEN** the global block declares no `ip6_url`
- **THEN** IPv6 detection uses the built-in default endpoint

## MODIFIED Requirements

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
