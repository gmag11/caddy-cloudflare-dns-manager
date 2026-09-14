## ADDED Requirements

### Requirement: Per-host record plan branches on tunnel declaration
Reconciliation SHALL branch per host: tunnel hosts reconcile a single proxied CNAME (per the `tunnel-dns-management` capability), and normal hosts continue reconciling A (always) and AAAA (when IPv6 is enabled). The managed record-type set used by prune SHALL be derived from this per-host plan.

#### Scenario: Mixed config reconciles both plans
- **WHEN** a zone declares one normal host with A/AAAA and one tunnel host
- **THEN** the normal host's A/AAAA records are reconciled as today and the tunnel host's CNAME is reconciled as specified by the tunnel capability

#### Scenario: Managed map reflects per-host plan
- **WHEN** reconciliation marks managed (name, type) pairs for prune
- **THEN** tunnel hosts mark CNAME only, and normal hosts mark A (and AAAA when enabled) only

### Requirement: Proxy mode resolution excludes CNAME content
The proxy-mode computation based on IP privacy classification SHALL apply only to address records (A/AAAA). CNAME records SHALL always be proxied for tunnel hosts and SHALL never be evaluated against IP privacy rules.

#### Scenario: CNAME content not classified as IP
- **WHEN** a tunnel host's CNAME target (`<uuid>.cfargotunnel.com`) is reconciled
- **THEN** the resulting record is proxied regardless of IP privacy classification rules
