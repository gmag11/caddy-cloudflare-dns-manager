## REMOVED Requirements

### Requirement: Prune only reaches declared tunnels

**Reason**: The requirement keyed prune reachability off the tunnels the *configuration declares*, which is
the constraint this change removes. A tunnel is now registered in the global block independently of
whether any host currently uses it, so "declared by a host" is no longer the right authorisation test.
Replaced by *Route pruning is limited to registered tunnels*, which keeps the no-enumeration guarantee,
states how the tunnel is identified, and adds the registry-rejection case.

## MODIFIED Requirements

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

## ADDED Requirements

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
