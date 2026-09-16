## ADDED Requirements

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

The plugin SHALL NOT delete a route for a hostname the configuration declares, even when that hostname appears in the pruned set for the same run.

#### Scenario: Declared host survives a same-run DNS deletion

- **WHEN** a declared host's record was deleted in this run (for example because the host was reverted from a tunnel to an address) and the tunnel still holds its route
- **THEN** the route is preserved and reconciled normally

#### Scenario: Declared host's drift is still corrected

- **WHEN** a declared host's route has a stale service
- **THEN** the service is corrected as before, independently of pruning

### Requirement: The catch-all is never pruned

The plugin SHALL NOT delete the rule that has no hostname. Every written configuration SHALL still end with exactly one catch-all rule after pruning.

#### Scenario: Catch-all survives a prune

- **WHEN** the plugin prunes routes from a tunnel
- **THEN** the written configuration still ends with exactly one catch-all rule

#### Scenario: Every host route pruned

- **WHEN** all of a tunnel's host routes are eligible and pruned
- **THEN** the write leaves the catch-all as the only rule

### Requirement: Prune only reaches declared tunnels

The plugin SHALL only prune routes on tunnels the configuration declares. When the configuration declares no tunnel at all, the plugin SHALL NOT enumerate the account's tunnels and SHALL instead log that the orphaned routes must be removed manually.

#### Scenario: Tunnel still declared

- **WHEN** at least one host declares the tunnel UUID and another host's route is eligible
- **THEN** that route is pruned

#### Scenario: No tunnel declared

- **WHEN** the last tunnel host is removed from the config and its DNS record is pruned
- **THEN** no tunnel is contacted and the plugin logs that the orphaned route needs manual removal

#### Scenario: Unrelated tunnels untouched

- **WHEN** the config declares one tunnel and the account contains others
- **THEN** no request is made against the other tunnels

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
