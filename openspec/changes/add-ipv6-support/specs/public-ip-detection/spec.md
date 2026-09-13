## ADDED Requirements

### Requirement: Public IPv6 detection

The plugin SHALL detect the host's public IPv6 address by querying the `ip6_url` endpoint (default endpoint when unset) when at least one declared host has IPv6 enabled with `ip6 auto`. IPv6 detection SHALL run in parallel with IPv4 detection and SHALL NOT depend on the IPv4 result.

#### Scenario: IPv6 detected

- **WHEN** a host declares `ip6 auto` and the IPv6 detection endpoint returns an IPv6 address
- **THEN** the plugin uses that address for the host's AAAA record

#### Scenario: No IPv6 hosts

- **WHEN** no declared host has IPv6 enabled with `ip6 auto`
- **THEN** the plugin performs no IPv6 detection request

### Requirement: Independent soft failure per family

IPv6 detection failure (network error, non-2xx, malformed body, or a non-IPv6 result) SHALL be soft and independent of IPv4 detection: the plugin SHALL log a warning, skip creating AAAA records for `ip6 auto` hosts, and leave existing AAAA records of those hosts unchanged. IPv4 detection outcomes SHALL NOT be affected by IPv6 failures, and vice versa. Hosts with a literal `ip6` SHALL be reconciled normally regardless of detection outcomes.

#### Scenario: v6 fails, v4 succeeds

- **WHEN** IPv6 detection fails while IPv4 detection succeeds for a host with `ip6 auto`
- **THEN** the plugin reconciles the A record normally and skips the AAAA with a warning

#### Scenario: v6 failure leaves existing AAAA

- **WHEN** IPv6 detection fails for an `ip6 auto` host that already has a plugin-owned AAAA record
- **THEN** the plugin leaves that AAAA record unchanged

#### Scenario: v4 fails, v6 succeeds

- **WHEN** IPv4 detection fails while IPv6 detection succeeds for a host with `ip6 auto`
- **THEN** the plugin reconciles the AAAA normally and applies v4 failure semantics to the A only

## MODIFIED Requirements

### Requirement: Public IP detection on reload

The plugin SHALL detect the server's public IPv4 address once per config load (reconcile) when at least one declared host has no explicit `ip` override. The plugin SHALL additionally detect the public IPv6 address in parallel when any host has `ip6 auto`. Detection of each family is independent; the failure of one family SHALL NOT block startup, reload, or the other family.

#### Scenario: Detection on reload

- **WHEN** Caddy loads a configuration with at least one auto-IP host
- **THEN** the plugin detects the public IPv4 once and shares it across all auto hosts

#### Scenario: Failure does not block startup

- **WHEN** the IPv4 detection endpoint is unreachable during a config load
- **THEN** Caddy still starts or reloads successfully, with the failure semantics below
