## Purpose

Defines how the plugin reconciles A records in Cloudflare zones for each declared host on config load: when records are created or updated, how zones and names are assigned, and how proxied versus DNS-only mode is decided.

## ADDED Requirements

### Requirement: Reconcile on config load

The plugin SHALL reconcile the DNS records for every declared host each time Caddy loads or reloads its configuration, so that the Cloudflare state matches the declared configuration. Reconciliation SHALL be idempotent: when state already matches, no API write is performed.

#### Scenario: No drift, no changes

- **WHEN** a reload runs and a declared host already has an A record with the configured IP and proxy mode
- **THEN** the plugin performs no write for that host

### Requirement: Create missing records

When a declared host has no existing A record in its zone, the plugin SHALL create the A record with the configured IP and proxy mode. Apex hosts SHALL be created as the zone's root record and nested hosts with their subdomain relative name; the plugin SHALL NOT create wildcard records.

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

### Requirement: Update existing records

When a declared host has an existing A record that is owned by the plugin and its IP or proxy mode differs from the configured values, the plugin SHALL update the record. Hosts whose existing record is not owned by the plugin SHALL only be updated when the host declares `force_adopt`.

#### Scenario: IP changed

- **WHEN** the configured IP for a declared, plugin-owned host changes between reloads
- **THEN** the plugin updates the existing A record to the new IP

#### Scenario: Proxy mode changed

- **WHEN** a declared, plugin-owned host's record is proxied but its effective IP becomes private, or the user changes `proxied`
- **THEN** the plugin updates the record's proxy mode accordingly

#### Scenario: Owned record left on detection failure

- **WHEN** public IP detection fails for an auto-IP host that already has a plugin-owned record
- **THEN** the plugin leaves that record unchanged (see public-ip-detection)

### Requirement: Private IP implies DNS-only

The plugin SHALL classify an IP as private/reserved when it falls in the private, loopback, link-local, CGNAT, or similar reserved ranges. When a record's effective IP is private/reserved, the record SHALL be created or updated as DNS-only, never proxied, even if `proxied yes` is declared.

#### Scenario: Tailscale CGNAT IP

- **WHEN** a host's effective IP is in the CGNAT range (e.g. `100.64.10.5`)
- **THEN** the record is created or updated DNS-only
