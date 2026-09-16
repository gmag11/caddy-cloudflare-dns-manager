# tunnel-ingress-prune Specification

## Purpose

Defines when the plugin may delete a tunnel ingress route: the per-zone `prune` opt-in, the DNS-correlation rule that makes a route eligible (its record deleted in the same run), which tunnels it may reach (those registered in the global block, identified from the deleted record rather than by enumerating the account), what is never eligible (declared hosts, the catch-all, routes whose records survived), what a prune-only write may and may not change, and the audit logging of deletions.

## Requirements

### Requirement: Prune is opt-in per zone

The plugin SHALL delete an orphaned tunnel ingress route only when the zone that owns the route's hostname has declared `prune`. Absent that opt-in the plugin SHALL NOT delete any ingress route.

#### Scenario: Opt-in absent

- **WHEN** a host is removed from the config and its zone has not declared `prune`
- **THEN** no ingress route is deleted

#### Scenario: Opt-in present

- **WHEN** a zone declares `prune` and an eligible orphaned route exists
- **THEN** the route is deleted

#### Scenario: Opt-in is scoped to the owning zone

- **WHEN** one zone declares `prune` and another does not, and both own an eligible route
- **THEN** only the route whose hostname belongs to the opted-in zone is deleted

### Requirement: A route is eligible only when its DNS record was deleted in the same run

The plugin SHALL delete an ingress route only when its hostname is one whose DNS record the plugin deleted during the same reconcile, whether by the per-zone prune or by the CNAME cleanup that accompanies a host being switched from tunnel-backed to an address host. Any other route SHALL be preserved, whatever its declaration state or DNS state.

#### Scenario: DNS record pruned in this run

- **WHEN** the prune deletes the record for `gone.example.com` and the tunnel holds a route for that hostname
- **THEN** the route is deleted in the same reconcile

#### Scenario: Host reverted from tunnel to address

- **WHEN** a host that declared `tunnel <uuid>` is re-declared with `ip`, so its CNAME is deleted to make room for the address record
- **THEN** the route for that hostname is deleted in the same reconcile

#### Scenario: DNS record not deleted

- **WHEN** a route is not declared by the config but its hostname's DNS record was not deleted in this run
- **THEN** the route is preserved

#### Scenario: Route whose name was never declared

- **WHEN** a route exists for a hostname this plugin never managed
- **THEN** the route is preserved, because no DNS record of its was deleted

### Requirement: Route deletion obeys the prune opt-in even on a revert

Deleting the DNS record that accompanies a tunnel-to-address revert SHALL NOT depend on the `prune` opt-in, because Cloudflare forbids a CNAME coexisting with an address record. Deleting the corresponding route SHALL depend on it, like every other route deletion.

#### Scenario: Revert without the opt-in

- **WHEN** a host is reverted from tunnel-backed to an address host and the zone has not declared `prune`
- **THEN** the CNAME is deleted, the address record is created, and the route is preserved

#### Scenario: Revert with the opt-in

- **WHEN** the same revert happens in a zone that declared `prune`
- **THEN** both the CNAME and the route are deleted

### Requirement: Declared hosts are never pruned

The plugin SHALL NOT delete a route for a hostname the configuration declares, even when that hostname appears in the pruned set for the same run. A hostname is declared for this purpose only by a host the current configuration declares with a tunnel.

#### Scenario: Declared tunnel host survives

- **WHEN** a tunnel-backed host's record was deleted in this run and the tunnel still holds its route
- **THEN** the route is preserved and reconciled normally

#### Scenario: Declared host's drift is still corrected

- **WHEN** a declared host's route has a stale service
- **THEN** the service is corrected as before, independently of pruning

#### Scenario: A host switched to an address host is no longer declared

- **WHEN** a host that declared `tunnel <name>` is re-declared with `ip`, so the same reconcile deletes its CNAME and records an address
- **THEN** the hostname is not declared for the purpose of this requirement, and its route is eligible for pruning under the opt-in

### Requirement: The catch-all is never pruned

The plugin SHALL NOT delete the rule that has no hostname. Every written configuration SHALL still end with exactly one catch-all rule after pruning.

#### Scenario: Catch-all survives a prune

- **WHEN** the plugin prunes routes from a tunnel
- **THEN** the written configuration still ends with exactly one catch-all rule

#### Scenario: Every host route pruned

- **WHEN** all of a tunnel's host routes are eligible and pruned
- **THEN** the write leaves the catch-all as the only rule

### Requirement: Prune is idempotent and auditable

Pruning SHALL occur within the same read-modify-write as the plan, producing at most one update per tunnel. Every pruned route SHALL be logged with its hostname and the fact that its DNS record was deleted in the same run.

#### Scenario: Single write

- **WHEN** a reconcile both updates the plan and prunes routes
- **THEN** exactly one update request is issued for that tunnel

#### Scenario: Second run is a no-op

- **WHEN** a reconcile follows one that already pruned the eligible routes
- **THEN** no update request is issued

#### Scenario: Deletion is logged with its cause

- **WHEN** a route is pruned
- **THEN** the log entry names the hostname and states that its DNS record was pruned in the same run

### Requirement: Route pruning is limited to registered tunnels

The plugin SHALL delete an ingress route only on a tunnel registered in the global `cf_dns_manager` block. For a hostname whose record this run deleted, the plugin SHALL derive the tunnel from the deleted record's target of the form `<uuid>.cfargotunnel.com` and SHALL require that UUID to match a registry entry. It SHALL NOT enumerate the account's tunnels. When a pruned name's deleted record names no tunnel, or names one the registry does not contain, the plugin SHALL NOT contact any tunnel for that name and SHALL log which of the two cases applies.

#### Scenario: Registered tunnel identified from the deleted record

- **WHEN** a pruned name's deleted CNAME target is `<uuid>.cfargotunnel.com` and `<uuid>` is registered
- **THEN** that tunnel is contacted and the hostname's route is pruned

#### Scenario: Last tunnel host removed

- **WHEN** the last host declaring a registered tunnel is removed from the config and its DNS record is pruned
- **THEN** the tunnel is identified from the deleted CNAME's target and the orphaned route is pruned

#### Scenario: Every tunnel host switched to an address

- **WHEN** every host declaring a registered tunnel is re-declared with `ip`, so each CNAME to that tunnel is deleted in one reconcile
- **THEN** each hostname's route is pruned from the tunnel in that reconcile, and the plugin logs no manual-removal report

#### Scenario: Tunnel named by the record is not registered

- **WHEN** a pruned name's deleted CNAME target names a UUID the registry does not contain
- **THEN** no tunnel is contacted and the plugin logs that the tunnel is not registered

#### Scenario: Deleted record names no tunnel

- **WHEN** a pruned name's deleted record is an address record, or a CNAME whose target is not under `cfargotunnel.com`
- **THEN** no tunnel is contacted for that name and the plugin logs that its route, if any, must be removed manually

#### Scenario: CNAME to an unrelated target is not a tunnel reference

- **WHEN** a deleted CNAME's target is `somewhere.example.net`
- **THEN** it is not treated as naming a tunnel

#### Scenario: Unregistered tunnels untouched

- **WHEN** the config registers one tunnel and the account contains others
- **THEN** no request is made against the others

### Requirement: A prune-only write changes nothing else

When the plugin prunes routes on a tunnel for which the run derived no plan, it SHALL remove only the routes whose hostnames this run pruned and SHALL NOT write a derived plan. Every other rule of that tunnel SHALL be preserved unchanged, including its position, and the catch-all SHALL NOT be rewritten.

#### Scenario: Surrounding rules are preserved

- **WHEN** a route is pruned from a tunnel with no derived plan, and that tunnel holds other rules and a catch-all
- **THEN** the written configuration equals the read configuration minus the pruned route

#### Scenario: Catch-all is untouched

- **WHEN** a route is pruned from a tunnel with no derived plan
- **THEN** the catch-all retains exactly the service and origin request it had, and is not replaced by the configured default

#### Scenario: No plan writes nothing

- **WHEN** a deleted record identifies a registered tunnel, and no pruned name belongs to that tunnel
- **THEN** no write is issued for it and no rule is added

#### Scenario: Idempotent

- **WHEN** a reconcile follows one that already pruned the eligible routes of a tunnel with no derived plan
- **THEN** no update request is issued for that tunnel

