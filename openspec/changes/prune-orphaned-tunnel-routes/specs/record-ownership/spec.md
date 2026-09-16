## MODIFIED Requirements

### Requirement: Per-zone orphan prune

The plugin SHALL provide a `prune` marker on a zone declaration in the global block. When enabled for a zone, the plugin SHALL delete this instance's orphaned artifacts belonging to that zone: A and AAAA records that carry this instance's tag but that the current configuration no longer manages (hosts removed from the Caddyfile, and the AAAA of a host whose IPv6 is disabled via `ip6 false` or absent), and tunnel ingress routes whose hostname's DNS record this same run pruned (see the `tunnel-ingress-prune` capability). Prune eligibility SHALL be determined solely by the declared configuration; a record skipped due to a transient detection failure SHALL NOT be pruned. Prune SHALL only ever delete artifacts tagged with this instance's identifier, or — for the tunnel routes it deletes — those it matches to a DNS deletion performed in the same run; it SHALL NOT delete records without the tag, records tagged with a different instance, or routes it cannot attribute to such a deletion. When not enabled, orphaned records and routes SHALL be left in place.

#### Scenario: Prune removes this instance's orphans

- **WHEN** `prune` is declared on a zone, a tagged record for `foo.example.com` exists, and the current config no longer declares `foo`
- **THEN** the plugin deletes that record (A and AAAA alike)

#### Scenario: No prune leaves orphans

- **WHEN** a zone has no `prune` marker and a tagged record's host is no longer declared
- **THEN** the plugin leaves the record in place

#### Scenario: Prune never touches other instances

- **WHEN** `prune` is declared on a zone and an existing record is tagged with a different instance identifier or has no tag
- **THEN** the plugin does not delete that record

#### Scenario: Tagged record still declared is kept

- **WHEN** `prune` is declared on a zone and a tagged record's host is still declared by the current config
- **THEN** the plugin reconciles the record normally and does not delete it

#### Scenario: Disabling IPv6 prunes the tagged AAAA in a prune-enabled zone

- **WHEN** `prune` is declared on a zone, a tagged AAAA exists for `foo.example.com`, and the current config declares `foo` with `ip6 false`
- **THEN** the plugin deletes that AAAA record and keeps the A record

#### Scenario: Disabling IPv6 without prune leaves the AAAA

- **WHEN** a zone has no `prune` marker, a tagged AAAA exists for `foo.example.com`, and the current config declares `foo` with `ip6 false`
- **THEN** the plugin leaves the AAAA record in place

#### Scenario: Transient detection failure spares the AAAA from prune

- **WHEN** `prune` is declared on a zone, a tagged AAAA exists for an `ip6 auto` host, and IPv6 detection fails on this reload
- **THEN** the plugin does not delete the AAAA record

#### Scenario: Prune also covers routes made unreachable by its own deletions

- **WHEN** `prune` is declared on a zone and the records it deletes include the one that made a tunnel hostname reachable
- **THEN** the matching tunnel route is deleted as part of the same run and the same write

#### Scenario: A route whose record prune did not delete is kept

- **WHEN** `prune` is declared on a zone but a tunnel route's hostname still has a live record
- **THEN** the route is preserved even though the zone opted in
