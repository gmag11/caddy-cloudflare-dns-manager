# Add Cloudflare Tunnel ingress management

## Why

Today the plugin manages only DNS. For a tunnel-backed host it writes a proxied CNAME to `<uuid>.cfargotunnel.com`, but the matching ingress rule that actually routes the request to a local service must be created by hand — in the Zero Trust dashboard or in a `cloudflared` `config.yml`. The only way the dashboard can create that DNS record for you is by adding a **Published application**, which for a catch-all requires a wildcard hostname and therefore also creates a `*.<zone>` DNS record. That wildcard makes every undeclared subdomain resolve, which is exactly the resolution problem this change removes.

Because the plugin already knows every tunnel-backed host, it can own the ingress plan too: one rule per declared hostname, a configurable default route for everything else, and no wildcard DNS record anywhere.

## What Changes

- Add an account-scoped credential to the global block so the plugin can call the Tunnel API:
  - `account <account_id> api_token <token>` (account-level `Cloudflare Tunnel Write`).
- Add a global default route: `tunnel_default_service <service>` (e.g. `https://caddy:443`). It becomes the tunnel's final catch-all rule. When unset, the catch-all is `http_status:404` (fail closed).
- Add an optional per-host override: `tunnel_service <service>` inside a host block, valid only alongside `tunnel <uuid>`.
- Reconcile the ingress plan per tunnel: read the current configuration, merge the declared rules, and write back only when the result differs.
- Preserve foreign ingress rules: rules whose hostname is not declared to the plugin are never dropped, and the catch-all requirement is always satisfied.
- Refuse to manage ingress on a locally-managed tunnel (`source: local`), with an error naming the remedy.
- Ingress rules deleted from the config are **not** removed from the tunnel (ingress rules carry no ownership tag). The leftover rule is inert once the DNS record is pruned, and is documented as a manual cleanup.
- Update `testenv/` to a remotely-managed tunnel so no `tunnel/config.yml` is needed — the plugin creates the ingress plan itself.
- Update user documentation to recommend the remotely-managed tunnel mode, matching Cloudflare's own guidance.

## Capabilities

### New Capabilities

- `tunnel-ingress-management`: The account-scoped credential, the ingress plan derivation (per-host rules, ordering, configurable default route, mandatory catch-all), read-modify-write reconciliation against the Tunnel configuration API, preservation of foreign rules, the no-deletion rule and its rationale, remotely-managed-only enforcement, and idempotent writes.

### Modified Capabilities

- `caddyfile-config`: The global options block gains the `account <id> api_token <token> [prune]` declaration, and the per-host directive gains the `tunnel_service <service>` subdirective with its `tunnel`-required validation.
- `tunnel-dns-management`: Adds the requirement that a tunnel host's ingress rule is managed alongside its CNAME, and that the tunnel must be remotely-managed for ingress management to engage. Existing CNAME reconciliation and ownership requirements are unchanged.

## Impact

- **Code**: `app.go` (`App` gains `AccountID`, `TunnelAPIToken`, `TunnelDefaultService`; `HostConfig` gains `TunnelService`); `caddyfile.go` (`account` global option, `tunnel_service` subdirective, validation); a new tunnel-ingress module (Tunnel API client + plan merge + reconcile); `reconcile.go` (invoke the ingress phase after per-zone DNS reconciliation).
- **Credentials**: introduces a second, **account-scoped** token (`Cloudflare Tunnel Write`) alongside the existing zone-scoped DNS token. It is strictly optional: configs that do not declare `account` keep the current behaviour and the current single-token blast radius.
- **Tests**: parsing/validation unit tests; plan-derivation and merge tests; mock-API tests for create, update-on-drift, idempotent no-op, foreign-rule preservation, catch-all enforcement, no-deletion of undeclared rules, and locally-managed rejection.
- **Test environment**: `testenv/docker-compose.yml` (`cloudflared` runs with `--token`, no `--config`/`--cred-file`), `testenv/tunnel/config.yml` removed, the one-time tunnel creation step changes to a remotely-managed tunnel.
- **Docs**: `docs/cloudflare-tunnel.md`, `docs/docker-deployment.md`, `docs/troubleshooting.md`, `testenv/README.md`.
- **Unchanged**: DNS reconciliation, ownership tagging, prune semantics for DNS records, public-IP detection, zone registration, and the global/flat option handling for existing directives. A config without the new `account` block behaves exactly as today.
- **Not included**: creating, deleting, or migrating tunnels; path-based ingress rules; non-HTTP services (`ssh://`, `tcp://`); Cloudflare Access settings on a rule.
