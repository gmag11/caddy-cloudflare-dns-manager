## ADDED Requirements

### Requirement: Tunnel subdirective
The per-site `cf_dns_manager` host block SHALL support a `tunnel <uuid>` subdirective that marks the host as tunnel-backed. Its validation rules (UUID format, exclusivity) are specified by the `tunnel-dns-management` capability.

#### Scenario: Tunnel subdirective adapts
- **WHEN** a host block contains `tunnel <valid-uuid>`
- **THEN** the adapted config carries the tunnel ID in the host declaration

#### Scenario: Unrecognized subdirectives still rejected
- **WHEN** a host block contains an unknown subdirective alongside `tunnel`
- **THEN** the adapter rejects the config
