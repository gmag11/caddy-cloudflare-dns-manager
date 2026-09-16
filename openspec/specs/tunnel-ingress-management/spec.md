# tunnel-ingress-management Specification

## Purpose

Defines how the plugin manages Cloudflare Tunnel ingress for tunnel-backed hosts: the optional account-scoped credential and derivation of the account id from the managed zone, the ingress plan derivation (per-host rules, deterministic ordering, configurable default route, mandatory catch-all), read-modify-write reconciliation against the Tunnel configuration API, preservation of foreign rules, the no-deletion rule for undeclared rules, remotely-managed-only enforcement, and idempotent writes.
## Requirements
### Requirement: Account-scoped tunnel credential

The global `cf_dns_manager` block SHALL accept an optional `account <token>` subdirective supplying an account-scoped credential for the Cloudflare Tunnel API. The account id SHALL be derived from the managed zone's owning account rather than configured, because Cloudflare guarantees a tunnel and its zone share an account. An optional `account_id <account-id>` subdirective SHALL override that derivation. When no `account` subdirective is present, the plugin SHALL NOT call the Tunnel API and SHALL behave exactly as before this capability existed.

#### Scenario: Account token declared

- **WHEN** the global block declares `account <TOKEN>`
- **THEN** the plugin may query and update tunnel configurations

#### Scenario: Account id derived from the zone

- **WHEN** a tunnel host is reconciled and the zone lookup reports the zone's owning account id
- **THEN** Tunnel API requests address that account without the account id being configured

#### Scenario: Account id explicitly overridden

- **WHEN** the global block declares both `account <TOKEN>` and `account_id <account-id>`
- **THEN** Tunnel API requests address the declared account id rather than the derived one

#### Scenario: Account token absent

- **WHEN** the global block declares no `account` subdirective and a host block declares `tunnel <uuid>`
- **THEN** no Tunnel API request is made and DNS reconciliation proceeds unchanged

#### Scenario: Account token missing

- **WHEN** the global block declares `account` with no value
- **THEN** the adapter rejects the config with an argument error

#### Scenario: Account id not resolvable

- **WHEN** tunnel hosts are declared, an account token is configured, and no zone resolved successfully in that run
- **THEN** no Tunnel API request is made and the plugin logs a warning naming the cause

### Requirement: Global default tunnel service

The global `cf_dns_manager` block SHALL accept an optional `tunnel_default_service <service>` subdirective declaring the destination for hostnames not otherwise routed by a declared host rule. It SHALL be emitted as the tunnel's final catch-all rule (no `hostname`). When unset, the final catch-all rule SHALL be `http_status:404`. The declared value SHALL be validated against the accepted tunnel service values.

#### Scenario: Default service configured

- **WHEN** the global block declares `tunnel_default_service https://caddy:443`
- **THEN** the derived ingress plan ends with a catch-all rule serving `https://caddy:443`

#### Scenario: Default service unset

- **WHEN** the global block declares no `tunnel_default_service`
- **THEN** the derived ingress plan ends with a catch-all rule serving `http_status:404`

#### Scenario: Invalid service value

- **WHEN** `tunnel_default_service` is declared with a value outside the accepted tunnel service values
- **THEN** the adapter rejects the config with an error naming the invalid value

#### Scenario: Correctly spelled socket default

- **WHEN** the global block declares `tunnel_default_service unix:/run/app.sock`
- **THEN** the value is accepted and the catch-all serves it, because a socket is a path rather than a URL

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

### Requirement: Ingress plan derivation

For each tunnel UUID declared by at least one host, the plugin SHALL derive exactly one ingress plan from the declared configuration. The plan SHALL contain one rule per hostname declared with that tunnel UUID, ordered deterministically, followed by the configured default rule.

#### Scenario: One rule per declared host

- **WHEN** two hosts each declare `tunnel <uuid>` and no `tunnel_service`
- **THEN** the plan for that tunnel contains one rule for each hostname, each serving the default service

#### Scenario: Deterministic rule order

- **WHEN** the same configuration is reconciled twice
- **THEN** the derived plan lists rules in the same order both times, independent of map iteration order

#### Scenario: Multiple tunnels are independent

- **WHEN** host A declares `tunnel <uuid-1>` and host B declares `tunnel <uuid-2>`
- **THEN** the plan for `uuid-1` contains only A's rule and the plan for `uuid-2` contains only B's rule

### Requirement: Per-host service override

The per-site `cf_dns_manager` host block SHALL accept an optional `tunnel_service <service>` subdirective declaring the destination for that hostname, overriding the global default. The subdirective SHALL be rejected unless the same host block also declares `tunnel`. The declared value SHALL be validated against the accepted tunnel service values.

#### Scenario: Per-host override

- **WHEN** a tunnel host declares `tunnel_service ssh://caddy:22` and the global default is `https://caddy:443`
- **THEN** that hostname's rule serves `ssh://caddy:22` while the catch-all still serves `https://caddy:443`

#### Scenario: Override without tunnel

- **WHEN** a host block declares `tunnel_service https://caddy:443` but no `tunnel <uuid>`
- **THEN** the adapter rejects the config with an error explaining that `tunnel_service` requires `tunnel`

#### Scenario: Host rule precedes the default rule

- **WHEN** a tunnel host declares an override and the plan also has a default rule
- **THEN** the host's rule appears before the default rule so that it takes precedence

#### Scenario: Invalid service value

- **WHEN** a host block declares `tunnel <uuid>` and a `tunnel_service` outside the accepted tunnel service values
- **THEN** the adapter rejects the config with an error naming the invalid value, by the same rule that governs the global default

#### Scenario: Socket destination for one host

- **WHEN** a tunnel host declares `tunnel_service unix:///run/app.sock`
- **THEN** the value is accepted and that hostname's rule serves it

### Requirement: Foreign ingress rules are preserved

The plugin SHALL preserve every existing ingress rule whose hostname is not declared by this configuration. A preserved rule SHALL retain its hostname, service and origin request settings, and SHALL remain ordered before the default rule. A rule whose hostname exactly matches a declared host is replaced by the plugin's derived rule.

#### Scenario: Undeclared hostname preserved

- **WHEN** a tunnel's existing configuration contains a rule for `other.example.com` that this config does not declare
- **THEN** the rule is present and unchanged in the configuration written back

#### Scenario: Declared hostname replaced

- **WHEN** a tunnel's existing configuration contains a rule for a hostname this config declares with a different service
- **THEN** the written configuration carries the plugin's derived service for that hostname

#### Scenario: Foreign wildcard does not silently shadow

- **WHEN** a preserved foreign rule uses a wildcard hostname that also matches a declared host
- **THEN** the plugin logs a warning naming the shadowing rule and the affected host

### Requirement: Ingress reconciliation is idempotent

The plugin SHALL read the tunnel's current configuration and write it back only when the derived plan differs from the current one. A reconcile run against an already-matching configuration SHALL issue no update request.

#### Scenario: In-sync configuration

- **WHEN** the tunnel's configuration already matches the derived plan
- **THEN** no update request is issued for that tunnel

#### Scenario: Drift corrected

- **WHEN** the tunnel's configuration lacks a rule for a declared host, or has a rule with a stale service
- **THEN** an update request is issued carrying the derived plan

#### Scenario: First reconcile on an empty configuration

- **WHEN** the tunnel has no ingress rules
- **THEN** the derived plan is written without error, even though the derived plan's per-host rules alone would lack a catch-all

### Requirement: Default rule always terminates the plan

Every plan written by the plugin SHALL end with exactly one catch-all rule that matches all traffic, satisfying the Tunnel configuration requirement that the last rule be a catch-all.

#### Scenario: Catch-all present

- **WHEN** the plugin writes any plan
- **THEN** the last entry has no `hostname` and no `path`

#### Scenario: Single catch-all

- **WHEN** the existing configuration contains a catch-all rule
- **THEN** it is replaced by the derived default rule rather than duplicated

### Requirement: Remotely-managed tunnels only

The plugin SHALL manage ingress only for tunnels whose configuration source is remotely managed. When a declared tunnel is locally managed, the plugin SHALL refuse to write its ingress and SHALL log an error naming the remedy.

#### Scenario: Remotely-managed tunnel accepted

- **WHEN** the tunnel configuration reports `source: cloudflare`
- **THEN** the plugin reconciles the ingress plan

#### Scenario: Locally-managed tunnel refused

- **WHEN** the tunnel configuration reports `source: local`
- **THEN** no ingress write is attempted and an error explains that ingress management requires a remotely-managed tunnel

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

### Requirement: Ingress failures do not block DNS reconciliation

A failure to reconcile tunnel ingress SHALL NOT prevent DNS record reconciliation, and SHALL be reported as an error in the reconcile result.

#### Scenario: Tunnel API failure

- **WHEN** the Tunnel API returns an error for a tunnel
- **THEN** the DNS records for the declared hosts are still reconciled and the ingress error is surfaced

#### Scenario: DNS failure with ingress configured

- **WHEN** a zone's DNS reconciliation fails
- **THEN** ingress reconciliation for tunnels in other zones still proceeds

### Requirement: HTTPS origins carry matchSNItoHost

Every ingress rule the plugin writes whose service uses the `https://` scheme SHALL carry `originRequest.matchSNItoHost` set to true, including the catch-all rule. The plugin SHALL merge this option into any existing origin request rather than replacing it, so other options the operator set are preserved. A rule whose service is `https://` and which lacks the option SHALL be treated as drift and corrected. Rules whose service uses any other scheme SHALL be left without it.

A `unix+tls:` service SHALL be among the schemes left without it. Although the alternate scheme speaks TLS, `cloudflared` models a unix origin as a distinct service type that carries no such field, and only its HTTP service consults the option, so a value written there would have no effect. The exclusion is deliberate and SHALL NOT be read as an omission to correct.

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

#### Scenario: A TLS socket is left alone

- **WHEN** a rule's service is `unix+tls:/run/app.sock`
- **THEN** the plugin does not add `matchSNItoHost` to it, because the option has no effect on a unix origin

### Requirement: Accepted tunnel service values

The `tunnel_service` and `tunnel_default_service` subdirectives SHALL be validated against one
shared set of accepted values. A value SHALL be accepted when it is one of:

- an `http://`, `https://`, `tcp://`, `ssh://`, `rdp://` or `smb://` URL carrying a non-empty
  address;
- a `unix:` or `unix+tls:` socket service carrying a non-empty path;
- an `http_status:<code>` service whose code is exactly three digits.

A value that matches none of these SHALL be rejected at adapt time with an error naming the
invalid value and the accepted forms. Validation SHALL be syntactic only: whether a destination
is reachable is the platform's business, not the adapter's.

The accepted set SHALL NOT include `bastion` or `hello_world`, although `cloudflared` accepts
both. Neither is a declared destination: `bastion` turns `cloudflared` into a jump host able to
reach any local address, and `hello_world` is a built-in test server. A subdirective whose
purpose is to name where traffic goes SHALL NOT be able to name either.

#### Scenario: Network service URL

- **WHEN** a subdirective declares `https://caddy:443`
- **THEN** the value is accepted

#### Scenario: Unix socket service

- **WHEN** a subdirective declares any `unix:` or `unix+tls:` value carrying a path
- **THEN** the value is accepted

#### Scenario: Status service

- **WHEN** a subdirective declares `http_status:404`
- **THEN** the value is accepted

#### Scenario: Value with no recognised form

- **WHEN** a subdirective declares `caddy:443`, which carries neither a scheme nor a socket prefix
- **THEN** the adapter rejects the config with an error naming the invalid value

#### Scenario: Unsupported scheme

- **WHEN** a subdirective declares `ftp://caddy:21`
- **THEN** the adapter rejects the config, because the accepted set names the schemes the platform proxies and `ftp` is not one of them

#### Scenario: Built-in test server is not a destination

- **WHEN** a subdirective declares `hello_world`
- **THEN** the adapter rejects the config, because the value names a service `cloudflared` runs rather than a destination to proxy to

#### Scenario: Jump host is not a destination

- **WHEN** a subdirective declares `bastion`
- **THEN** the adapter rejects the config, because the value would turn `cloudflared` into a jump host rather than name a destination

#### Scenario: URL with a scheme but no address

- **WHEN** a subdirective declares `https://`
- **THEN** the adapter rejects the config, because a URL form carries no destination

#### Scenario: Status code of the wrong shape

- **WHEN** a subdirective declares `http_status:40` or `http_status:abcd`
- **THEN** the adapter rejects the config, because the code is not exactly three digits

### Requirement: A unix socket is a path, not a URL

A `unix:` or `unix+tls:` service SHALL be interpreted as a filesystem path, because `cloudflared`
trims the prefix and uses the remainder as the path without ever parsing it as a URL. The slashes
after the colon therefore carry no meaning, and the plugin SHALL accept every spelling the
platform accepts, including the form used throughout Cloudflare's documentation and the
URL-looking form interchangeably. A prefix carrying no path behind it SHALL be rejected, counting
any number of leading slashes as no path.

#### Scenario: Path written directly after the prefix

- **WHEN** a subdirective declares `unix:/run/app.sock`, the spelling Cloudflare's examples use
- **THEN** the value is accepted

#### Scenario: URL-looking spelling

- **WHEN** a subdirective declares `unix:///run/app.sock`
- **THEN** the value is accepted, and refers to the same socket path

#### Scenario: Everything after the prefix is the path

- **WHEN** a subdirective declares `unix://run/app.sock`
- **THEN** the value is accepted, and the socket path is `//run/app.sock`, because the whole remainder is the path and no part of it is read as a host

#### Scenario: TLS socket

- **WHEN** a subdirective declares `unix+tls:/run/app.sock`
- **THEN** the value is accepted

#### Scenario: Socket prefix with no path

- **WHEN** a subdirective declares `unix:`, `unix://`, `unix+tls:` or `unix+tls://`
- **THEN** the adapter rejects the config, because leading slashes alone do not name a socket

