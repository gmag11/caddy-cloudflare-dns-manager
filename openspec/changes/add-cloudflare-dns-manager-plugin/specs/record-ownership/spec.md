## Purpose

Defines how the plugin tags Cloudflare records it creates so it can distinguish its own records from those created by other instances or by hand, and how conservative adoption and safe per-zone orphan cleanup behave.

## ADDED Requirements

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

### Requirement: Conservative default for untagged records

By default, the plugin SHALL treat a Cloudflare A record that lacks the plugin's tag as owned by someone else and SHALL NOT update or delete it. The plugin SHALL only modify an untagged record when the declaring site includes `force_adopt`, and SHALL only delete records carrying its own tag.

#### Scenario: Untagged record left untouched

- **WHEN** a declared host's existing record has no plugin tag
- **THEN** the plugin leaves the record as-is and logs that it was skipped

#### Scenario: Untagged record force-adopted

- **WHEN** the declaring site includes `force_adopt`
- **THEN** the plugin updates the record to the configured state and rewrites its tag to this instance

### Requirement: Per-zone orphan prune

The plugin SHALL provide a `prune` marker on a zone declaration in the global block. When enabled for a zone, the plugin SHALL delete A records that carry this instance's tag but whose host is no longer declared by the current configuration. Prune SHALL only ever delete records tagged with this instance's identifier; it SHALL NOT delete records without the tag or tagged with a different instance. When not enabled, orphaned records SHALL be left in place.

#### Scenario: Prune removes this instance's orphans

- **WHEN** `prune` is declared on a zone, a tagged record for `foo.example.com` exists, and the current config no longer declares `foo`
- **THEN** the plugin deletes that record

#### Scenario: No prune leaves orphans

- **WHEN** a zone has no `prune` marker and a tagged record's host is no longer declared
- **THEN** the plugin leaves the record in place

#### Scenario: Prune never touches other instances

- **WHEN** `prune` is declared on a zone and an existing record is tagged with a different instance identifier or has no tag
- **THEN** the plugin does not delete that record

#### Scenario: Tagged record still declared is kept

- **WHEN** `prune` is declared on a zone and a tagged record's host is still declared by the current config
- **THEN** the plugin reconciles the record normally and does not delete it
