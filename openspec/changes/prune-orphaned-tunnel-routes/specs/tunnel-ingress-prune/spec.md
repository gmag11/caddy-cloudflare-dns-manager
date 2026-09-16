## ADDED Requirements

### Requirement: Prune is opt-in per zone

The plugin SHALL delete orphaned tunnel ingress rules only when the zone owning the rule's hostname has declared `prune`. Absent that opt-in, the plugin SHALL NOT delete any ingress rule and SHALL behave exactly as before this capability existed.

#### Scenario: Opt-in absent

- **WHEN** a host is removed from the config and its zone has not declared `prune`
- **THEN** no ingress rule is deleted

#### Scenario: Opt-in present

- **WHEN** a zone declares `prune` and an eligible orphaned rule exists
- **THEN** the rule is deleted

#### Scenario: Opt-in is scoped to the owning zone

- **WHEN** one zone declares `prune` and another does not, and both have an eligible orphaned rule
- **THEN** only the rule whose hostname belongs to the opted-in zone is deleted

### Requirement: Prune eligibility requires all three conditions

A rule SHALL be a prune candidate only when every one of the following holds: its `description` equals this instance's ownership tag; its `hostname` is non-empty and not declared by this configuration; and its `hostname` has no DNS record in its zone. The plugin SHALL NOT delete a rule that fails any condition, regardless of the opt-in.

#### Scenario: All conditions hold

- **WHEN** a rule is tagged by this instance, its hostname is undeclared, and no DNS record exists at that name
- **THEN** the rule is deleted

#### Scenario: Untagged rule spared

- **WHEN** a rule has no description, or a description that is not this instance's tag
- **THEN** the rule is preserved, whatever its DNS state

#### Scenario: Another instance's tag spared

- **WHEN** a rule carries a tag belonging to a different instance
- **THEN** the rule is preserved

#### Scenario: Declared hostname spared

- **WHEN** a rule is tagged by this instance but its hostname is declared by the configuration
- **THEN** the rule is preserved

#### Scenario: Resolving hostname spared

- **WHEN** a rule is tagged by this instance, its hostname is undeclared, but a DNS record still exists at that name
- **THEN** the rule is preserved and the plugin logs that it was spared because it resolves

### Requirement: Prune never removes the catch-all

The plugin SHALL NOT delete the rule without a `hostname`, which terminates the configuration. Every written configuration SHALL still end with exactly one catch-all rule after pruning.

#### Scenario: Catch-all exempt

- **WHEN** the plugin prunes rules from a tunnel
- **THEN** the written configuration still ends with exactly one catch-all rule

#### Scenario: Only the catch-all remains

- **WHEN** every host-declared rule is eligible and the plugin prunes them all
- **THEN** the write succeeds with a configuration containing only the catch-all

### Requirement: Prune fails closed when liveness is unknown

The plugin SHALL treat a hostname's liveness as unknown when the zone's DNS reconciliation did not complete in that run, and SHALL NOT prune rules for that zone in that run. Unknown liveness SHALL never be treated as "does not resolve".

#### Scenario: Zone DNS reconciliation failed

- **WHEN** a zone's DNS reconciliation fails and its tunnel has candidate rules
- **THEN** no rule for that zone is deleted in that run

#### Scenario: Prune resumes after a successful run

- **WHEN** the zone's DNS reconciliation succeeds on a later run and the candidates are still eligible
- **THEN** the rules are deleted then

### Requirement: Prune is idempotent and auditable

Pruning SHALL happen in the same read-modify-write as the plan, producing at most one write per tunnel. Every deletion SHALL be logged with the rule's hostname, service and the tag that authorised it. Every rule that was considered but spared SHALL be logged with the condition that spared it.

#### Scenario: Single write

- **WHEN** a reconcile both updates the plan and prunes rules
- **THEN** exactly one update request is issued for that tunnel

#### Scenario: Second run is a no-op

- **WHEN** a reconcile follows one that already pruned the eligible rules
- **THEN** no update request is issued

#### Scenario: Deletion is logged with its evidence

- **WHEN** a rule is pruned
- **THEN** the log entry carries its hostname, its service, the instance tag, and the fact that no DNS record existed

#### Scenario: Spared rule is logged with its reason

- **WHEN** a rule looks like a candidate but is spared
- **THEN** the log entry names the condition that spared it (not tagged, declared, or still resolving)
