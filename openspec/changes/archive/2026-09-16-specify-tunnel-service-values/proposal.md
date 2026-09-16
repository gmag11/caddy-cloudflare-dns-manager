# Specify the accepted tunnel service values

## Why

The `tunnel_service` and `tunnel_default_service` subdirectives are validated at adapt time, but
no spec says **which values are valid**. The one requirement that depends on the answer — "Global
default tunnel service" — rejects a value that is "neither a supported service URL nor an
`http_status:<code>` service" without ever defining what a supported service URL is.

That gap already produced a real defect. The validator required the URL-looking `unix://` form
while Cloudflare's documentation, `cloudflared`'s own parser and the plugin's own reference page
all use `unix:/path`, so a configuration written from any of those sources was rejected at adapt
time by the plugin that documented it. Nothing in the spec could catch the divergence, because
the spec did not name the set that the code, the docs and the platform disagreed about.

The same gap hides two deliberate exclusions that look like omissions and would be "fixed" by
the next reader: `bastion` and `hello_world` are absent from the accepted set although
`cloudflared` accepts them, and `unix+tls:` never gains `matchSNItoHost` although it performs a
TLS handshake.

## What Changes

- **State the accepted service values as a requirement**, so the validator has a written
  contract: `http`, `https`, `tcp`, `ssh`, `rdp` and `smb` URLs, `unix` and `unix+tls` socket
  paths, and `http_status:<code>`.
- **Record that a unix socket is a path, not a URL.** `cloudflared` trims the prefix and treats
  the remainder as a filesystem path, never parsing it as a URL, so the slashes after the colon
  carry no meaning. Every spelling the platform accepts SHALL be accepted — `unix:/run/app.sock`
  as written in Cloudflare's examples, and `unix:///run/app.sock` alike — while a prefix with no
  path behind it SHALL be rejected.
- **Record why `bastion` and `hello_world` are rejected** even though `cloudflared` accepts them:
  neither is a declared destination. `bastion` turns `cloudflared` into a jump host for any local
  address, and `hello_world` is a built-in test server. Both are deliberate non-goals of a
  declarative destination subdirective, not oversights.
- **Record why `unix+tls` is excluded from `matchSNItoHost`.** `cloudflared` models a unix origin
  as a distinct service type that carries no such field, and only its HTTP service consults the
  option, so writing it there would have no effect. The existing requirement says HTTPS services
  carry the option and others do not; this makes the unix case explicitly correct rather than
  accidentally so.
- **Both subdirectives are covered by name.** Today `tunnel_default_service` has a validation
  scenario while `tunnel_service` has none, so the per-host path is unverified by contract even
  though it runs the same validator.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tunnel-ingress-management`: Adds the accepted service-value set as a requirement, makes the
  socket-path spelling rules explicit, and documents the two exclusions. Tightens the "Global
  default tunnel service" and "Per-host service override" requirements so both subdirectives are
  validated against it.

## Impact

- **Code**: none. `parseTunnelService` already accepts every value the new requirement mandates;
  this change writes down the contract it implements. No behaviour changes.
- **Specs**: `openspec/specs/tunnel-ingress-management/spec.md` gains one requirement and one
  tightened one.
- **Docs**: no change. The reference page already states the accepted set and the socket-path
  rule; the specifying change makes the spec agree with it.
- **Tests**: no new tests required for behaviour, since the validator already covers the
  mandated set. Existing table-driven cases mirror the new requirement's scenarios.
- **Not included**: adding `bastion` or `hello_world` support; validating that a destination is
  reachable, which stays the platform's business; accepting non-socket services with an empty
  address.
