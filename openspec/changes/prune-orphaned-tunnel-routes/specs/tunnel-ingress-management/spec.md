## MODIFIED Requirements

### Requirement: Undeclared ingress rules are not deleted

Rules removed from the configuration SHALL NOT be deleted from the tunnel, except when the operator has opted into pruning for the owning zone AND the rule satisfies every eligibility condition defined by the `tunnel-ingress-prune` capability. Absent that opt-in, or when any condition fails, the rule SHALL be preserved and the plugin SHALL log why it was left in place. The plugin SHALL NOT delete a rule on the basis of its description alone.

#### Scenario: Removed host rule left in place without the opt-in

- **WHEN** a host that previously declared `tunnel <uuid>` is removed from the config and the owning zone has not declared `prune`
- **THEN** its ingress rule remains on the tunnel and the plugin logs that manual removal is required

#### Scenario: Rule that still resolves is preserved

- **WHEN** an undeclared rule carries this instance's tag but its hostname still has a DNS record
- **THEN** the rule is preserved and the plugin logs that it was spared because it resolves

#### Scenario: Rule without a tag is preserved

- **WHEN** an undeclared rule has no description matching this instance's tag
- **THEN** the rule is preserved regardless of the opt-in and of its DNS state

#### Scenario: Eligible rule pruned under the opt-in

- **WHEN** the owning zone declared `prune`, an undeclared rule carries this instance's tag, and its hostname resolves to no DNS record
- **THEN** the rule is removed from the configuration in the same write as the derived plan

### Requirement: Rules carry the instance ownership tag

Every ingress rule the plugin authors SHALL carry the instance ownership tag in its `description`, the ingress counterpart of the comment written on DNS records. The tag SHALL be corrected on drift, so a rule that lost or changed its tag is rewritten. The tag SHALL NOT be sufficient grounds for deletion on its own: it is one of several conditions the `tunnel-ingress-prune` capability requires.

#### Scenario: Derived rules are tagged

- **WHEN** the plugin writes a plan for a tunnel
- **THEN** every rule it authors, including the catch-all, carries the instance ownership tag as its description

#### Scenario: A missing or changed tag is drift

- **WHEN** a declared host's rule exists with the right service but no tag, or a different description
- **THEN** the configuration is rewritten so the rule carries the instance tag

#### Scenario: Tag in place is not drift

- **WHEN** a declared host's rule already carries the right service and the instance tag
- **THEN** no write is issued for that configuration

#### Scenario: Tag alone never authorises deletion

- **WHEN** an undeclared rule carries this instance's tag but a condition of the prune eligibility rule does not hold
- **THEN** the rule is preserved
