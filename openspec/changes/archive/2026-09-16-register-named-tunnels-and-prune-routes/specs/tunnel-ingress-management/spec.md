## MODIFIED Requirements

### Requirement: Undeclared ingress rules are not deleted

Rules removed from the configuration SHALL NOT be deleted from the tunnel, except when the owning zone has declared `prune` AND the route's hostname is one whose DNS record this plugin deleted during the same reconcile, as specified by the `tunnel-ingress-prune` capability. When any of those conditions fails, the rule SHALL be preserved. The plugin SHALL NOT delete a rule on the basis of its `description` or of any inferred property. The tunnel holding the rule need not be one a host still declares: a route on a registered tunnel that no host currently declares is eligible when its hostname's DNS record was deleted in this run.

#### Scenario: Removed host rule left in place without the opt-in

- **WHEN** a host that previously declared `tunnel <name>` is removed from the config and the owning zone has not declared `prune`
- **THEN** its ingress rule remains on the tunnel and the plugin logs that manual removal is required

#### Scenario: Route whose DNS record survived is preserved

- **WHEN** an undeclared route's hostname still has a DNS record, so no deletion occurred for it in this run
- **THEN** the rule is preserved

#### Scenario: Eligible route pruned under the opt-in

- **WHEN** the owning zone declared `prune`, the route's hostname is undeclared, and this run's DNS prune deleted the record for that hostname
- **THEN** the route is removed from the configuration in the same write as the derived plan

#### Scenario: Eligible route on a registered tunnel no host declares

- **WHEN** the owning zone declared `prune`, the tunnel is registered, no host declares it any more, and this run deleted the CNAME that made a route's hostname reachable
- **THEN** the route is removed from that tunnel, identified from the deleted record, without rewriting any other rule of it

#### Scenario: Description is never a deletion signal

- **WHEN** an undeclared rule carries any `description`, including one resembling an ownership marker
- **THEN** the description does not affect eligibility

### Requirement: Ingress plan derivation

For each tunnel UUID declared by at least one host, the plugin SHALL derive exactly one ingress plan from the declared configuration. The plan SHALL contain one rule per hostname declared with that tunnel UUID, ordered deterministically, followed by the configured default rule.

#### Scenario: One rule per declared host

- **WHEN** two hosts each declare `tunnel <name>` for the same registered tunnel and no `tunnel_service`
- **THEN** the plan for that tunnel contains one rule for each hostname, each serving the default service

#### Scenario: Deterministic rule order

- **WHEN** the same configuration is reconciled twice
- **THEN** the derived plan lists rules in the same order both times, independent of map iteration order

#### Scenario: Multiple tunnels are independent

- **WHEN** host A declares `tunnel <name-1>` and host B declares `tunnel <name-2>`, two distinct registered tunnels
- **THEN** the plan for the first tunnel contains only A's rule and the plan for the second contains only B's rule

### Requirement: Per-host service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective declaring the destination for that hostname, overriding the global default. The subdirective SHALL be rejected unless the same host block also declares `tunnel`. The declared value SHALL be validated against the accepted tunnel service values.

#### Scenario: Per-host override

- **WHEN** a tunnel host declares `tunnel_service ssh://caddy:22` and the global default is `https://caddy:443`
- **THEN** that hostname's rule serves `ssh://caddy:22` while the catch-all still serves `https://caddy:443`

#### Scenario: Override without tunnel

- **WHEN** a host block declares `tunnel_service https://caddy:443` but no `tunnel`
- **THEN** the adapter rejects the config with an error explaining that `tunnel_service` requires `tunnel`

#### Scenario: Host rule precedes the default rule

- **WHEN** a tunnel host declares an override and the plan also has a default rule
- **THEN** the host's rule appears before the default rule so that it takes precedence

#### Scenario: Invalid service value

- **WHEN** a host block declares `tunnel <name>` and a `tunnel_service` outside the accepted tunnel service values
- **THEN** the adapter rejects the config with an error naming the invalid value, by the same rule that governs the global default
