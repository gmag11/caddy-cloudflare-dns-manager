# dns-record-reconciliation Specification

## Purpose

Defines how the plugin reconciles A records in Cloudflare zones for each declared host on config load: when records are created or updated, how zones and names are assigned, and how proxied versus DNS-only mode is decided.

## Requirements

### Requirement: Reconcile on config load

The plugin SHALL reconcile the DNS records (A and, when enabled, AAAA) for every declared host each time Caddy loads or reloads its configuration, so that the Cloudflare state matches the declared configuration. Reconciliation SHALL be idempotent: when state already matches, no API write is performed. Records are matched per (record name, record type); the two families of a host are independent records.

#### Scenario: No drift, no changes

- **WHEN** a reload runs and a declared host already has an A record with the configured IP and proxy mode (and, when IPv6 is enabled, an AAAA matching its configured values)
- **THEN** the plugin performs no write for that host

### Requirement: Create missing records

When a declared host has no existing record of a given enabled family (A or AAAA) in its zone, the plugin SHALL create that record with the family's effective IP and proxy mode. Apex hosts SHALL be created as the zone's root record and nested hosts with their subdomain relative name; the plugin SHALL NOT create wildcard records.

#### Scenario: New subdomain

- **WHEN** a reload declares `cf_dns_manager { host @foo }` and `foo.example.com` has no A record in Cloudflare
- **THEN** the plugin creates the `foo` A record under `example.com` with the detected public IP

#### Scenario: Apex missing

- **WHEN** a reload declares a managed apex `example.com` and no root A record exists
- **THEN** the plugin creates the apex A record with the configured IP

#### Scenario: Nested subdomain

- **WHEN** a reload declares a managed host `a.app.example.com` within a declared zone `app.example.com`
- **THEN** the plugin creates the A record with relative name `a` in the `app.example.com` zone

#### Scenario: Wildcard site block ignored

- **WHEN** a site block address is a wildcard such as `*.example.com`
- **THEN** the plugin does not attempt to create a wildcard record from the site address

#### Scenario: Existing record returned with an FQDN name

- **WHEN** the Cloudflare list API returns an existing record whose `name` is the fully-qualified `foo.example.com` (the API's real shape) and the plugin computes the relative name `foo`
- **THEN** the plugin matches it as the existing record and does not attempt to create a duplicate

#### Scenario: Missing AAAA created

- **WHEN** a host with `ip6 auto` has an A record but no AAAA record and v6 detection succeeds
- **THEN** the plugin creates the AAAA record with the detected public IPv6

### Requirement: Update existing records

When a declared host has an existing record of a given family that is owned by the plugin and its IP or proxy mode differs from the configured values, the plugin SHALL update that record. Hosts whose existing record is not owned by the plugin SHALL only be updated when the host declares `force_adopt`. Adoption and matching SHALL be strictly per record type: an A record SHALL never be adopted, updated, or overwritten as an AAAA, and vice versa.

#### Scenario: IP changed

- **WHEN** the configured IP for a declared, plugin-owned host changes between reloads
- **THEN** the plugin updates the existing A record to the new IP

#### Scenario: Proxy mode changed

- **WHEN** a declared, plugin-owned host's record is proxied but its effective IP becomes private, or the user changes `proxied`
- **THEN** the plugin updates the record's proxy mode accordingly

#### Scenario: Owned record left on detection failure

- **WHEN** public IP detection fails for an auto-IP host that already has a plugin-owned record
- **THEN** the plugin leaves that record unchanged (see public-ip-detection)

#### Scenario: Cross-family records never cross-adopted

- **WHEN** the plugin manages the AAAA of a host and an untagged A record exists with the same name (or the reverse)
- **THEN** the plugin leaves the untagged record of the other family unchanged

### Requirement: AAAA record management

When a host has IPv6 enabled (`ip6 auto` or `ip6 <literal>`), the plugin SHALL reconcile the host's AAAA record in the same pass as its A record, using the family's effective IP (literal or detected) and the host's `proxied` value. A host with `ip6 false` SHALL never have its AAAA created, updated, or adopted by the plugin.

#### Scenario: Both families managed

- **WHEN** a host declares `ip6 auto` and detection succeeds for both families with no existing records
- **THEN** the plugin creates an A and an AAAA record for the host

#### Scenario: IPv6-only family

- **WHEN** a host declares `ip6 2001:db8::10` and the v4 detection fails but the IPv6 family is manual
- **THEN** the plugin reconciles the AAAA to `2001:db8::10` and applies v4 detection-failure semantics to the A only

### Requirement: Mixed proxied state warning

When both families of a host are resolvable and one family's effective IP is private while the other's is public, the plugin SHALL log a warning naming the host and the per-family proxied outcome. The mixed state SHALL be supported, not rejected: each record is written with its own forced mode. The warning SHALL be emitted at adapt time when both values are literal, and at reconcile time when both families resolve through detection.

#### Scenario: Public v4 with private v6 literal

- **WHEN** a host declares `ip 203.0.113.10` and `ip6 fd7a:115c:a1e0::1` with `proxied yes`
- **THEN** adaptation succeeds with a warning, the A record is proxied, and the AAAA record is DNS-only

#### Scenario: Mixed state detected at reconcile

- **WHEN** `ip6 auto` resolves to a ULA address while v4 is public, with `proxied yes`
- **THEN** the plugin logs a mixed-state warning and writes A proxied, AAAA DNS-only

### Requirement: IPv6 private classification

The plugin SHALL classify IPv6 addresses as private/reserved when they are ULA (fc00::/7), link-local (fe80::/10), or loopback, and as public otherwise. A public IPv6 SHALL be eligible for proxying; a private IPv6 SHALL force DNS-only. The plugin SHALL NOT classify an IPv6 address as private solely because it is not IPv4.

#### Scenario: Public IPv6 proxied

- **WHEN** a host's effective IPv6 is `2001:db8::1` with `proxied yes`
- **THEN** the AAAA record is created or updated proxied

#### Scenario: ULA IPv6 forced DNS-only

- **WHEN** a host's effective IPv6 is in fd00::/8 with `proxied yes`
- **THEN** the AAAA record is created or updated DNS-only

### Requirement: Private IP implies DNS-only

The plugin SHALL classify an IP as private/reserved when it falls in the private, loopback, link-local, CGNAT, or similar reserved ranges. When a record's effective IP is private/reserved, the record SHALL be created or updated as DNS-only, never proxied, even if `proxied yes` is declared.

#### Scenario: Tailscale CGNAT IP

- **WHEN** a host's effective IP is in the CGNAT range (e.g. `100.64.10.5`)
- **THEN** the record is created or updated DNS-only
