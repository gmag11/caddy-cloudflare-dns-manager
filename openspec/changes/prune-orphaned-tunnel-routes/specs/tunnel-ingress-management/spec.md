## REMOVED Requirements

### Requirement: Rules carry the instance ownership tag

**Reason**: The mechanism is being reverted. Cloudflare's documented ingress model does not include `description`, the dashboard does not display it, and — verified live — the dashboard clears it whenever a rule is edited there. An ownership marker that is invisible, undocumented and erased by normal use cannot support any behaviour, least of all a destructive one.

**Migration**: No action is required. Rules tagged by an earlier build keep their `description`; from this release the field is treated as unmanaged and inherited from the existing rule, exactly like `path` and `originRequest`, so it survives but is never read. Route cleanup is now driven by the DNS prune instead (see the `tunnel-ingress-prune` capability).

## MODIFIED Requirements

### Requirement: Undeclared ingress rules are not deleted

Rules removed from the configuration SHALL NOT be deleted from the tunnel, except when the owning zone has declared `prune` AND the route's hostname is one whose DNS record this plugin deleted during the same reconcile, as specified by the `tunnel-ingress-prune` capability. When any of those conditions fails, the rule SHALL be preserved. The plugin SHALL NOT delete a rule on the basis of its `description` or of any inferred property.

#### Scenario: Removed host rule left in place without the opt-in

- **WHEN** a host that previously declared `tunnel <uuid>` is removed from the config and the owning zone has not declared `prune`
- **THEN** its ingress rule remains on the tunnel and the plugin logs that manual removal is required

#### Scenario: Route whose DNS record survived is preserved

- **WHEN** an undeclared route's hostname still has a DNS record, so no deletion occurred for it in this run
- **THEN** the rule is preserved

#### Scenario: Eligible route pruned under the opt-in

- **WHEN** the owning zone declared `prune`, the route's hostname is undeclared, and this run's DNS prune deleted the record for that hostname
- **THEN** the route is removed from the configuration in the same write as the derived plan

#### Scenario: Description is never a deletion signal

- **WHEN** an undeclared rule carries any `description`, including one resembling an ownership marker
- **THEN** the description does not affect eligibility

### Requirement: Unmanaged rule metadata is preserved

Ingress rules carry fields the plugin does not manage, notably a human-readable `description`, a `path` and an `originRequest`. Because a write replaces the whole configuration, the plugin SHALL preserve those fields: on rules it does not declare (kept verbatim) and on rules it rewrites, where a non-empty value SHALL be carried over from the rule currently at the same hostname. The plugin SHALL NOT author or clear them, and such a field appearing on its own SHALL NOT be treated as drift.

Preserving `originRequest` is load-bearing rather than cosmetic: an option such as `matchSNItoHost` cannot be expressed by the plugin, and losing it makes `cloudflared` present the service URL's hostname as SNI, which a wildcard-certificate origin rejects, turning every request into a 502.

#### Scenario: Foreign rule keeps its metadata

- **WHEN** an undeclared rule carries a description and an origin request, and the plugin writes the configuration
- **THEN** both are present and unchanged in the written configuration

#### Scenario: Rewritten rule keeps its description

- **WHEN** a declared host's rule has a stale service and a description set outside the plugin
- **THEN** the written rule carries the corrected service and the original description

#### Scenario: Rewritten rule keeps its origin request

- **WHEN** a declared host's rule has a stale service and an `originRequest` such as `matchSNItoHost`
- **THEN** the written rule carries the corrected service and the original `originRequest`

#### Scenario: Rewritten rule keeps its path

- **WHEN** a declared host's rule has a `path` and the plugin corrects its service
- **THEN** the written rule still carries that `path`

#### Scenario: Catch-all metadata preserved

- **WHEN** the existing catch-all carries a description or an origin request and the plugin re-emits the default rule
- **THEN** they are carried over to the emitted catch-all

#### Scenario: An unmanaged field alone is not drift

- **WHEN** the only difference between the stored configuration and the derived plan is an unmanaged field
- **THEN** no write is issued and the field is left intact

#### Scenario: Metadata is never invented

- **WHEN** a declared host has no existing rule to inherit from
- **THEN** its derived rule is written with no description, path or origin request
