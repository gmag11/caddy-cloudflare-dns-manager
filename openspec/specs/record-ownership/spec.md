# record-ownership Specification

## Purpose

Defines how the plugin tags Cloudflare records it creates so it can distinguish its own records from those created by other instances or by hand, and how conservative adoption and safe per-zone orphan cleanup behave.

## Requirements

### Requirement: Record ownership tag

The plugin SHALL tag every A record it creates or adopts with a comment that combines a configurable prefix and an instance identifier. The prefix SHALL be configurable via a `tag_prefix` global option; the instance identifier SHALL be configurable via an `instance` global option and otherwise default to a stable per-server value (the hostname).

#### Scenario: Tag written on create

- **WHEN** the plugin creates an A record
- **THEN** the record carries a comment of the form `<prefix>:<instance>`

#### Scenario: Configurable prefix and instance

- **WHEN** the global options set `tag_prefix custom` and `instance {$CADDY_INSTANCE_ID}`
- **THEN** records created or adopted carry the comment `custom:<CADDY_INSTANCE_ID>`

#### Scenario: Default instance

- **WHEN** no `instance` option is configured
- **THEN** the plugin uses the server's hostname as the instance identifier

### Requirement: AAAA ownership tagging

Records the plugin creates or updates for IPv6 (AAAA) SHALL carry the same ownership comment tag as IPv4 records (`<tag_prefix>:<instance>`). The tag identifies the managing instance, not the address family.

#### Scenario: Created AAAA carries tag

- **WHEN** the plugin creates an AAAA record for a declared host
- **THEN** the record's comment equals the instance's ownership tag

### Requirement: Conservative default for untagged records

By default, the plugin SHALL treat a Cloudflare A record that lacks the plugin's tag as owned by someone else and SHALL NOT update or delete it. The plugin SHALL only modify an untagged record when the declaring site includes `force_adopt`, and SHALL only delete records carrying its own tag.

#### Scenario: Untagged record left untouched

- **WHEN** a declared host's existing record has no plugin tag
- **THEN** the plugin leaves the record as-is and logs that it was skipped

#### Scenario: Untagged record force-adopted

- **WHEN** the declaring site includes `force_adopt`
- **THEN** the plugin updates the record to the configured state and rewrites its tag to this instance

### Requirement: Per-family adoption

Adoption of untagged records (including `force_adopt`) SHALL operate per record type: an untagged AAAA SHALL only be considered for adoption by the host's IPv6 management, and an untagged A only by the host's IPv4 management. The plugin SHALL NOT treat an untagged record of one family as adoptable because it manages the other family of the same name.

#### Scenario: Untagged AAAA not adopted via A ownership

- **WHEN** the plugin owns the A record of `foo.example.com`, an untagged AAAA exists for the same name, and the host declares `ip6 auto` without `force_adopt`
- **THEN** the plugin leaves the untagged AAAA unchanged and logs that it is not owned

#### Scenario: force_adopt applies per family

- **WHEN** a host declares `ip6 2001:db8::10 force_adopt` and an untagged AAAA exists
- **THEN** the plugin overwrites and claims the AAAA; any untagged A is unaffected by the IPv6 management

### Requirement: Per-zone orphan prune

The plugin SHALL provide a `prune` marker on a zone declaration in the global block. When enabled for a zone, the plugin SHALL delete A and AAAA records that carry this instance's tag but that the current configuration no longer manages: hosts removed from the Caddyfile, and the AAAA of a host whose IPv6 is disabled (`ip6 false` or absent). Prune eligibility SHALL be determined solely by the declared configuration; a record skipped due to a transient detection failure SHALL NOT be pruned. Prune SHALL only ever delete records tagged with this instance's identifier; it SHALL NOT delete records without the tag or tagged with a different instance. When not enabled, orphaned records SHALL be left in place.

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
