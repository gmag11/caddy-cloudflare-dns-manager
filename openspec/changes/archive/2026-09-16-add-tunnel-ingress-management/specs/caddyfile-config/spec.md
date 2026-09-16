## ADDED Requirements

### Requirement: Global account credential for tunnel ingress

The plugin SHALL accept an optional `account <token>` subdirective in the global `cf_dns_manager` block, declaring the account-scoped credential used to manage Cloudflare Tunnel ingress. It SHALL also accept an optional `account_id <account-id>` subdirective overriding the derived account id. The account credential SHALL be handled independently from zone credentials: zone tokens SHALL remain zone-scoped, and the account credential SHALL be used only for Tunnel API calls.

#### Scenario: Account subdirective parsed

- **WHEN** the global block contains `account <token>`
- **THEN** the adapted config carries the token in the app configuration

#### Scenario: Account id subdirective parsed

- **WHEN** the global block contains `account_id <account-id>`
- **THEN** the adapted config carries the account id as an override

#### Scenario: Account subdirective optional

- **WHEN** the global block omits the `account` subdirective
- **THEN** the config adapts successfully with no account credential configured

#### Scenario: Zone tokens unaffected

- **WHEN** the global block declares both `zone ... api_token ...` and `account ...`
- **THEN** DNS operations use the zone token and Tunnel operations use the account token

### Requirement: Global tunnel default service option

The plugin SHALL accept an optional `tunnel_default_service <service>` subdirective in the global `cf_dns_manager` block, declaring the default destination for tunnel traffic. When unset, the plugin SHALL use `http_status:404` as the default.

#### Scenario: Default service parsed

- **WHEN** the global block contains `tunnel_default_service https://caddy:443`
- **THEN** the adapted config carries that value as the tunnel default service

#### Scenario: Default service omitted

- **WHEN** the global block omits `tunnel_default_service`
- **THEN** the plugin treats the tunnel default as `http_status:404`

### Requirement: Per-site tunnel service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective. Declaring `tunnel_service` without a `tunnel <uuid>` in the same block SHALL be a configuration error.

#### Scenario: Override alongside tunnel

- **WHEN** a host block declares both `tunnel <valid-uuid>` and `tunnel_service <service>`
- **THEN** the adapted host declaration carries the service override

#### Scenario: Override without tunnel rejected

- **WHEN** a host block declares `tunnel_service` and no `tunnel`
- **THEN** the adapter rejects the config with an error identifying the missing `tunnel` subdirective

#### Scenario: Override with tunnel and ip rejected

- **WHEN** a host block declares `tunnel`, `tunnel_service` and `ip`
- **THEN** the adapter rejects the config with the existing tunnel/ip conflict error
