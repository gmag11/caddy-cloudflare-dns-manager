## ADDED Requirements

### Requirement: HTTPS origins carry matchSNItoHost

Every ingress rule the plugin writes whose service uses the `https://` scheme SHALL carry `originRequest.matchSNItoHost` set to true, including the catch-all rule. The plugin SHALL merge this option into any existing origin request rather than replacing it, so other options the operator set are preserved. A rule whose service is `https://` and which lacks the option SHALL be treated as drift and corrected. Rules whose service uses any other scheme SHALL be left without it.

#### Scenario: Derived HTTPS rule carries the option

- **WHEN** the plugin writes a rule for a host whose tunnel service is `https://caddy:443`
- **THEN** the rule's origin request has `matchSNItoHost` set to true

#### Scenario: Catch-all carries it too

- **WHEN** `tunnel_default_service` is an `https://` service
- **THEN** the catch-all rule the plugin emits also carries `matchSNItoHost`

#### Scenario: Other options are preserved

- **WHEN** a rule already carries `originRequest` with options such as `http2Origin`, and the plugin adds the managed option
- **THEN** those options are present in the written rule alongside `matchSNItoHost`

#### Scenario: A missing option is drift

- **WHEN** a declared host's HTTPS rule exists without `matchSNItoHost`
- **THEN** the configuration is rewritten so the option is set

#### Scenario: A rule that already has it is not drift

- **WHEN** a declared host's HTTPS rule already enables `matchSNItoHost`
- **THEN** no write is issued for that configuration

#### Scenario: Non-HTTPS services are left alone

- **WHEN** a rule's service uses `http://`, `http_status:`, or a non-HTTP scheme
- **THEN** the plugin does not add `matchSNItoHost` to it

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

Ingress rules carry fields the plugin does not manage, notably a `path` and origin-request options other than `matchSNItoHost`. Because a write replaces the whole configuration, the plugin SHALL preserve those fields: on rules it does not declare (kept verbatim) and on rules it rewrites, where a non-empty value SHALL be carried over from the rule currently at the same hostname. The plugin SHALL NOT author or clear them, and such a field appearing on its own SHALL NOT be treated as drift.

`matchSNItoHost` is excluded from this inheritance because the plugin authors it: for an HTTPS service it is always enabled, so it is re-asserted from the service rather than inherited.

The `description` field is not modelled at all. It is absent from Cloudflare's documented ingress model and the dashboard never sets it, while a write that omits the key clears any stored value. The plugin therefore does not participate in the field.

#### Scenario: Foreign rule keeps its metadata

- **WHEN** an undeclared rule carries a path or an origin-request option such as `http2Origin`, and the plugin writes the configuration
- **THEN** they are present and unchanged in the written configuration

#### Scenario: Rewritten rule keeps its origin-request options

- **WHEN** a declared host's rule has a stale service and an `originRequest` carrying options other than the managed one
- **THEN** the written rule carries the corrected service and the original options

#### Scenario: Rewritten rule keeps its path

- **WHEN** a declared host's rule has a `path` and the plugin corrects its service
- **THEN** the written rule still carries that `path`

#### Scenario: Catch-all metadata preserved

- **WHEN** the existing catch-all carries an origin-request option and the plugin re-emits the default rule
- **THEN** the option is carried over to the emitted catch-all

#### Scenario: An unmanaged field alone is not drift

- **WHEN** the only difference between the stored configuration and the derived plan is an unmanaged field
- **THEN** no write is issued and the field is left intact

#### Scenario: Descriptions are not managed

- **WHEN** the plugin writes a rule
- **THEN** it does not set or preserve a `description`, because the field is outside Cloudflare's documented model and the dashboard never populates it
