# Proposal

## Why

Two `cf_dns_manager` directives can declare the same hostname. Because validation is per-directive, each block looks valid on its own and the collision only appears downstream, where the plugin reconciles the same record twice (and, for tunnel hosts, builds two conflicting ingress rules). The common cause is copying a whole site block and forgetting to change its `host`, so the mistake is easy to make and hard to notice.

## What Changes

- Reject a configuration in which the same hostname is declared by more than one per-site `cf_dns_manager` directive.
- Surface the failure while Caddy loads and processes the configuration (adapt/load), naming the duplicated host so the copy-paste mistake is obvious, and fail the reload rather than silently managing the record twice.
- Treat differently written but equivalent references as the same host: a literal FQDN, a trailing-dot form, mixed case, and a named matcher resolving to the same FQDN all collide.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `caddyfile-config`: adds a requirement that the same hostname may be declared by only one per-site `cf_dns_manager` directive, and that a duplicate is a configuration error.

## Impact

- `registry.go`: host registration (the aggregation point where all directives meet) gains the duplicate check.
- `caddyfile.go`: the no-op handler's `Provision` propagates the registration error.
- Tests: a new case alongside the existing per-site directive tests.
- No API, credential, or Cloudflare behavior changes; no change to valid configurations.
