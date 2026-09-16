## MODIFIED Requirements

### Requirement: Tunnel host declaration

The per-site `cf_dns_manager` directive SHALL accept a `tunnel <name>` subdirective declaring that the host's DNS endpoint is the Cloudflare Tunnel registered under that name in the global `cf_dns_manager` block. The adapter SHALL resolve the name to the registered UUID at adapt time. A name that is not registered SHALL be rejected; a value that is not a registered name SHALL NOT be treated as an implicit registration of a UUID.

#### Scenario: Tunnel declared with a registered name

- **WHEN** a host block declares `tunnel edge` and the global block registers `edge` with a valid UUID
- **THEN** the config adapts successfully and the host is registered as a tunnel host with that UUID

#### Scenario: Tunnel name not registered

- **WHEN** a host block declares `tunnel edge` and no `edge` is registered
- **THEN** the adapter rejects the config with an error naming the unknown tunnel

#### Scenario: UUID-shaped value is not an implicit registration

- **WHEN** a host block declares `tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567` and no registry entry has that UUID
- **THEN** the adapter rejects the config with an error explaining that `tunnel` takes a registered name and pointing at the global block

#### Scenario: Tunnel missing argument

- **WHEN** a host block declares `tunnel` with no argument
- **THEN** the adapter rejects the config with an argument error

### Requirement: Tunnel exclusivity with IP directives

The `tunnel` subdirective SHALL be mutually exclusive with `ip`, `ip6`, and `proxied` within the same host block. The adapter MUST reject the config if both are declared.

#### Scenario: tunnel with ip

- **WHEN** a host block declares both `tunnel <registered-name>` and `ip 1.2.3.4`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with ip6

- **WHEN** a host block declares both `tunnel <registered-name>` and `ip6 auto`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with proxied

- **WHEN** a host block declares both `tunnel <registered-name>` and `proxied no`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with force_adopt allowed

- **WHEN** a host block declares both `tunnel <registered-name>` and `force_adopt`
- **THEN** the config adapts successfully

### Requirement: Tunnel host declaration accepts a service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective alongside `tunnel <name>`, declaring the service that hostname's ingress rule serves. Its validation rules (requires `tunnel`) are specified by the `caddyfile-config` and `tunnel-ingress-management` capabilities.

#### Scenario: Host with service override

- **WHEN** a host block declares `tunnel <registered-name>` and `tunnel_service ssh://caddy:22`
- **THEN** the host's ingress rule serves that service instead of the global default
