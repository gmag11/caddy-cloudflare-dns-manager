## ADDED Requirements

### Requirement: Global tunnel registry

The global `cf_dns_manager` block SHALL accept a repeatable `tunnel <name> <uuid>` subdirective registering a Cloudflare Tunnel this configuration may manage. The name SHALL be unique and non-empty; the UUID SHALL be a canonical 8-4-4-4-12 hexadecimal UUID. Registering a tunnel SHALL NOT by itself cause any API call: the registry is a declaration of scope, not an instruction to write.

#### Scenario: Tunnel registered

- **WHEN** the global block contains `tunnel edge 13fc5d01-f96f-4f18-a0be-7adf03200f18`
- **THEN** the config adapts successfully and the registry contains `edge` mapped to that UUID

#### Scenario: Several tunnels registered

- **WHEN** the global block declares two `tunnel` entries with distinct names and UUIDs
- **THEN** both are registered and each is addressable by its name

#### Scenario: Duplicate name rejected

- **WHEN** the global block declares two `tunnel` entries with the same name
- **THEN** the adapter rejects the config with an error naming the duplicated name

#### Scenario: Malformed UUID rejected

- **WHEN** a `tunnel` entry's UUID is not a canonical UUID
- **THEN** the adapter rejects the config with an error identifying the invalid UUID

#### Scenario: Missing arguments rejected

- **WHEN** a `tunnel` entry omits the name or the UUID
- **THEN** the adapter rejects the config with an argument error

#### Scenario: Registry is declaration only

- **WHEN** a tunnel is registered and no host declares it
- **THEN** no ingress request is made for that tunnel on account of the registration alone

## MODIFIED Requirements

### Requirement: Tunnel subdirective

The per-site `cf_dns_manager` host block SHALL support a `tunnel <name>` subdirective that marks the host as tunnel-backed, where `<name>` is a tunnel registered in the global `cf_dns_manager` block. The adapter SHALL resolve the name to its UUID at adapt time and SHALL reject a name that is not registered. Its remaining validation rules (exclusivity) are specified by the `tunnel-dns-management` capability.

#### Scenario: Tunnel subdirective adapts

- **WHEN** a host block contains `tunnel edge` and the global block registers `edge`
- **THEN** the adapted config carries the resolved UUID in the host declaration

#### Scenario: Unknown tunnel name rejected

- **WHEN** a host block contains `tunnel edge` and the global block does not register `edge`
- **THEN** the adapter rejects the config with an error naming the unknown tunnel and listing the registered names

#### Scenario: Raw UUID rejected with migration guidance

- **WHEN** a host block declares `tunnel 13fc5d01-f96f-4f18-a0be-7adf03200f18` and no registry entry has that UUID
- **THEN** the adapter rejects the config with an error explaining that `tunnel` takes a registered name and that the UUID belongs in the global block

#### Scenario: Unrecognized subdirectives still rejected

- **WHEN** a host block contains an unknown subdirective alongside `tunnel`
- **THEN** the adapter rejects the config

### Requirement: Per-site tunnel service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective. Declaring `tunnel_service` without a `tunnel <name>` in the same block SHALL be a configuration error.

#### Scenario: Override alongside tunnel

- **WHEN** a host block declares both `tunnel <registered-name>` and `tunnel_service <service>`
- **THEN** the config adapts successfully

#### Scenario: Override without tunnel rejected

- **WHEN** a host block declares `tunnel_service` and no `tunnel`
- **THEN** the adapter rejects the config with an error identifying the missing `tunnel` subdirective

#### Scenario: Override with tunnel and ip rejected

- **WHEN** a host block declares `tunnel`, `tunnel_service` and `ip`
- **THEN** the adapter rejects the config with the existing tunnel/ip conflict error
