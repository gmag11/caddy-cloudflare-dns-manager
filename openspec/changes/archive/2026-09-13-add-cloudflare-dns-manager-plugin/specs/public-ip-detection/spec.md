## Purpose

Defines how the plugin obtains the server's public IPv4 address on each Caddy reload, from a configurable endpoint with non-blocking failure behavior.

## ADDED Requirements

### Requirement: Public IP detection on reload

The plugin SHALL detect the server's public IPv4 once per config load and reuse that single value for every auto-IP host in that load. The detection endpoint SHALL be configurable via an `ip_url` global option; when unset, the plugin SHALL query a default Cloudflare-owned endpoint that reports the caller's IP.

#### Scenario: Endpoint configured

- **WHEN** the global options block sets `cf_dns_manager { ip_url https://ip.example.net }`
- **THEN** the plugin queries that URL to detect the public IPv4

#### Scenario: Default endpoint used

- **WHEN** no `ip_url` is configured
- **THEN** the plugin queries the default endpoint (`https://cloudflare.com/cdn-cgi/trace`)

#### Scenario: Single detection per load

- **WHEN** several auto-IP hosts are reconciled in one reload
- **THEN** the plugin performs one detection request and uses the same IP for all of them

### Requirement: IPv4-only detection

The plugin SHALL resolve the detected address to an IPv4. If the configured endpoint returns an IPv6 address or no IPv4, the plugin SHALL treat the detection as failed for the purpose of this requirement.

#### Scenario: Endpoint returns IPv6

- **WHEN** the detection endpoint returns an IPv6 address for a host without an explicit IP
- **THEN** the plugin treats detection as unavailable and applies the failure semantics below

### Requirement: Non-blocking detection failure

If public IP detection fails (network error, non-2xx response, malformed body, or IPv6 result), the plugin SHALL NOT block Caddy startup or reload. It SHALL log a warning, SHALL leave existing Cloudflare records of auto-IP hosts unchanged, and SHALL skip creating records for auto-IP hosts that do not yet exist. Hosts with an explicit `ip` override SHALL still be reconciled normally.

#### Scenario: Detection fails, host already has a record

- **WHEN** public IP detection fails and an auto-IP host already has a Cloudflare record
- **THEN** the plugin logs a warning and does not modify the existing record

#### Scenario: Detection fails, host has no record

- **WHEN** public IP detection fails and an auto-IP host has no Cloudflare record yet
- **THEN** the plugin logs a warning and skips creating the record for that host

#### Scenario: Detection fails, host has explicit IP

- **WHEN** public IP detection fails but a host declares an explicit `ip`
- **THEN** the plugin reconciles that host's record normally using the explicit IP
