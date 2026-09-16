## ADDED Requirements

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

## MODIFIED Requirements

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
