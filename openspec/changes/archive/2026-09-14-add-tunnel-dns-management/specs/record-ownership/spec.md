## ADDED Requirements

### Requirement: Record listing includes CNAME
The plugin SHALL list CNAME records alongside A and AAAA records when enumerating a zone's DNS records, so tunnel CNAMEs participate in reconciliation, adoption, and prune.

#### Scenario: CNAME visible to reconciliation
- **WHEN** a zone contains a CNAME record at a managed host's name
- **THEN** the record appears in the reconciliation map for that name with type CNAME

#### Scenario: Non-address, non-CNAME types still filtered
- **WHEN** a zone contains MX or TXT records
- **THEN** they are filtered out and never reconciled or pruned

## MODIFIED Requirements

### Requirement: Per-family adoption
The system SHALL apply ownership-based adoption per record type, where the record types considered are A, AAAA, and (for tunnel hosts) CNAME. An untagged record of any of these types at a managed name is never adopted via ownership of another type.

#### Scenario: Untagged AAAA not adopted via A ownership
- **WHEN** an owned A record exists for a name and an untagged AAAA also exists at that name
- **THEN** the AAAA is not treated as owned

#### Scenario: Untagged CNAME not adopted via A ownership
- **WHEN** an owned A record exists for a normal host's name and an untagged CNAME also exists at that name
- **THEN** the CNAME is not treated as owned

#### Scenario: force_adopt applies per family
- **WHEN** `force_adopt` is set on a host
- **THEN** an untagged record of the managed type(s) for that host is adopted, and records of types the host does not manage are unaffected by adoption (tunnel hosts delete unmanaged owned/untagged address records per the tunnel migration rules)
