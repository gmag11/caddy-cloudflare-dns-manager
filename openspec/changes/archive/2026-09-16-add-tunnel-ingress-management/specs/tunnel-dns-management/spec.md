## ADDED Requirements

### Requirement: Tunnel host ingress accompanies its CNAME

For each tunnel host, the plugin SHALL treat the DNS CNAME (§ tunnel-dns-management) and the tunnel ingress rule (§ tunnel-ingress-management) as two halves of one declaration: a hostname that resolves but has no ingress rule, and an ingress rule whose hostname does not resolve, are both considered an incomplete declaration. When the account credential is configured, reconciling a tunnel host SHALL reconcile both halves in the same run.

#### Scenario: CNAME and ingress created together

- **WHEN** a host declares `tunnel <uuid>` for the first time, with the account credential configured
- **THEN** the proxied CNAME is created and an ingress rule for that hostname is written to the tunnel

#### Scenario: No wildcard DNS is ever created

- **WHEN** a host is declared tunnel-backed and the default service routes unmatched hostnames
- **THEN** no wildcard DNS record is created for the zone, so undeclared subdomains do not resolve

#### Scenario: Ingress skipped without an account credential

- **WHEN** a host declares `tunnel <uuid>` and no `account` credential is configured
- **THEN** the CNAME is reconciled as before and no ingress request is attempted, with a log line explaining that ingress management needs the account credential

#### Scenario: Removed host leaves DNS authoritative

- **WHEN** a tunnel host is removed from the config in a prune-enabled zone
- **THEN** its CNAME is pruned, so the hostname no longer resolves even if its ingress rule remains on the tunnel

### Requirement: Tunnel host declaration accepts a service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective alongside `tunnel <uuid>`, declaring the service that hostname's ingress rule serves. Its validation rules (requires `tunnel`) are specified by the `caddyfile-config` and `tunnel-ingress-management` capabilities.

#### Scenario: Host with service override

- **WHEN** a host block declares `tunnel <uuid>` and `tunnel_service ssh://caddy:22`
- **THEN** the host's ingress rule serves that service instead of the global default
