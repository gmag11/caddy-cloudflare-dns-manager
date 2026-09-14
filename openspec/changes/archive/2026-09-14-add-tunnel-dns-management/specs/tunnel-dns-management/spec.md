## ADDED Requirements

### Requirement: Tunnel host declaration
The per-site `cf_dns_manager` directive SHALL accept a `tunnel <uuid>` subdirective declaring that the host's DNS endpoint is the Cloudflare Tunnel with the given UUID. The UUID MUST be a canonical 8-4-4-4-12 hexadecimal UUID.

#### Scenario: Tunnel declared with valid UUID
- **WHEN** a host block declares `tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567`
- **THEN** the config adapts successfully and the host is registered as a tunnel host

#### Scenario: Tunnel UUID malformed
- **WHEN** a host block declares `tunnel not-a-uuid`
- **THEN** the adapter rejects the config with an error identifying the invalid UUID

#### Scenario: Tunnel missing argument
- **WHEN** a host block declares `tunnel` with no argument
- **THEN** the adapter rejects the config with an argument error

### Requirement: Tunnel exclusivity with IP directives
The `tunnel` subdirective SHALL be mutually exclusive with `ip`, `ip6`, and `proxied` within the same host block. The adapter MUST reject the config if both are declared.

#### Scenario: tunnel with ip
- **WHEN** a host block declares both `tunnel <uuid>` and `ip 1.2.3.4`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with ip6
- **WHEN** a host block declares both `tunnel <uuid>` and `ip6 auto`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with proxied
- **WHEN** a host block declares both `tunnel <uuid>` and `proxied no`
- **THEN** the adapter rejects the config with an error naming the conflict

#### Scenario: tunnel with force_adopt allowed
- **WHEN** a host block declares both `tunnel <uuid>` and `force_adopt`
- **THEN** the config adapts successfully

### Requirement: CNAME reconciliation for tunnel hosts
For a tunnel host, the plugin SHALL reconcile exactly one CNAME record with content `<tunnel-uuid>.cfargotunnel.com`, always in proxied mode, regardless of the host's IP situation.

#### Scenario: Tunnel CNAME created
- **WHEN** a tunnel host has no existing record at its name
- **THEN** a proxied CNAME to `<uuid>.cfargotunnel.com` is created

#### Scenario: Tunnel CNAME updated on drift
- **WHEN** an owned CNAME exists with different content (e.g. different tunnel UUID in config)
- **THEN** the CNAME is updated to the new target, staying proxied

#### Scenario: Tunnel CNAME always proxied
- **WHEN** any tunnel host record is created or updated
- **THEN** the record's proxied state is true, never affected by IP privacy classification

#### Scenario: In-sync tunnel CNAME untouched
- **WHEN** an owned proxied CNAME already matches the configured target
- **THEN** no API write occurs for that record

### Requirement: Tunnel adoption and ownership
CNAME records for tunnel hosts SHALL carry the instance ownership tag, and untagged CNAMEs at a tunnel host's name SHALL follow the same conservative adoption rules as A/AAAA records.

#### Scenario: Created CNAME carries ownership tag
- **WHEN** the plugin creates a tunnel CNAME
- **THEN** the record comment equals the instance ownership tag

#### Scenario: Untagged CNAME left without force_adopt
- **WHEN** an untagged CNAME exists at a tunnel host's name and `force_adopt` is not set
- **THEN** the record is left unchanged and a warning suggests `force_adopt`

#### Scenario: Untagged CNAME force-adopted
- **WHEN** an untagged CNAME exists at a tunnel host's name and `force_adopt` is set
- **THEN** the record is updated to the tunnel target, proxied, and tagged as owned

### Requirement: Tunnel migration deletes owned address records
When reconciling a tunnel host whose name already has owned A/AAAA records, the plugin SHALL delete those owned records before creating the CNAME. Untagged A/AAAA records at the name SHALL be left alone unless `force_adopt` is set, and a conflict with untagged records SHALL fail CNAME creation with a clear error when `force_adopt` is absent.

#### Scenario: Owned A deleted before CNAME creation
- **WHEN** a tunnel host's name has an A record tagged with this instance's ownership tag
- **THEN** the A record is deleted and the CNAME is created on the same reconcile run

#### Scenario: Untagged A blocks without force_adopt
- **WHEN** a tunnel host's name has an untagged A record and `force_adopt` is not set
- **THEN** the untagged A is left unchanged and the plugin logs an error explaining the conflict and the `force_adopt` remedy (no CNAME is created)

#### Scenario: Untagged A removed with force_adopt
- **WHEN** a tunnel host's name has an untagged A record and `force_adopt` is set
- **THEN** the untagged A is deleted and the CNAME is created

#### Scenario: AAAA handled like A
- **WHEN** a tunnel host's name has owned or (with `force_adopt`) untagged AAAA records
- **THEN** they are deleted under the same rules as A records

### Requirement: Reverting a tunnel host to an address host
When a host that was previously tunnel-backed is re-declared as a normal (non-tunnel) host, the plugin SHALL delete any owned CNAME record at that name before creating its A/AAAA records, within the same reconcile run. Cloudflare rejects creating an address record while a CNAME exists at the same name, so the CNAME MUST be cleared first (not merely left to prune, which runs after reconciliation).

#### Scenario: Owned CNAME deleted before A creation
- **WHEN** a normal host's name has a CNAME tagged with this instance's ownership tag and no A record
- **THEN** the CNAME is deleted and the A record is created in the same reconcile run

#### Scenario: Revert succeeds without a further reload
- **WHEN** a tunnel host is changed to an address host in a single config load
- **THEN** the name ends the run with the address record present and the CNAME gone

#### Scenario: Untagged CNAME blocks without force_adopt
- **WHEN** a normal host's name has an untagged CNAME record and `force_adopt` is not set
- **THEN** the CNAME is left unchanged, no A record is created, and the plugin logs an error naming the conflict and the `force_adopt` remedy

#### Scenario: Untagged CNAME removed with force_adopt
- **WHEN** a normal host's name has an untagged CNAME record and `force_adopt` is set
- **THEN** the untagged CNAME is deleted and the A record is created

### Requirement: Tunnel hosts do not trigger IP detection
Tunnel hosts SHALL NOT cause public-IP detection. A configuration consisting only of tunnel hosts performs no detection requests.

#### Scenario: Only tunnel hosts declared
- **WHEN** all declared hosts are tunnel hosts
- **THEN** no public-IP detection HTTP request is made

#### Scenario: Mixed tunnel and normal hosts
- **WHEN** a config declares both tunnel hosts and normal hosts needing detection
- **THEN** detection runs once per family as today, shared by the normal hosts only

### Requirement: Tunnel CNAME prune participation
The managed record type set for a tunnel host SHALL include CNAME (and not A/AAAA), so prune treats orphaned owned CNAMEs as prunable and pre-tunnel owned A/AAAA leftovers as orphans.

#### Scenario: Removed tunnel host pruned
- **WHEN** a tunnel host declaration is removed from a prune-enabled zone and the owned CNAME remains
- **THEN** the orphaned CNAME is deleted by prune

#### Scenario: Pre-tunnel A leftover pruned
- **WHEN** a zone with prune enabled contains an owned A record at a name now declared as a tunnel host (e.g. from before migration, where delete failed)
- **THEN** the A record is pruned as an orphan

#### Scenario: Untagged CNAME never pruned
- **WHEN** a CNAME without the ownership tag exists at any name in a prune-enabled zone
- **THEN** it is never deleted
