# Design

## Context

See proposal.md - Why.

Two facts about how this plugin is wired shape the approach:

1. **Host declarations are aggregated at load time, not adapt time.** The global `cf_dns_manager` option is parsed exactly once and is immutable afterwards; per-site directives are parsed independently and cannot append to it. Each per-site directive emits a no-op `hostHandler` that carries its `HostConfig` through JSON and registers it with the global `App` in `Provision` (`app.addHost`). All validation today is therefore per-directive; nothing sees the whole set until those handlers provision.
2. **`App.Validate` cannot be used as the aggregation check.** Caddy's `provisionContext` loads every app up front, and `LoadModuleByID` runs a module's `Validate` immediately after its own `Provision`. So `App.Validate` can run before the HTTP app's handlers are provisioned (app-map iteration order is not fixed), when `app.hosts` is still empty or partial.

## Goals / Non-Goals

**Goals:**

- Reject, at config load, any configuration where one hostname is declared by more than one per-site directive.
- Make the error name the host so a copied-and-not-edited block is immediately identifiable.
- Keep the check independent of which host mode (address or tunnel) or which subdirective values the colliding declarations use.

**Non-Goals:**

- Detecting hosts that share a target IP. Distinct hostnames pointing at the same address are legitimate and common; only the hostname itself identifies a managed record.
- Detecting hosts that reference the same tunnel. That is normal for tunnel-backed hosts.
- Changing the Caddyfile surface or adding a new option. Valid configurations are unaffected.

## Decisions

### Detect duplicates at host registration, and propagate the error from `Provision`

`App.addHost` is the single point where every per-site declaration converges, and it already runs under `hostsMu`. Change it to return an error when the incoming host matches one already registered, and have `hostHandler.Provision` return that error.

Why here:

- It is the earliest point at which the colliding declaration is visible, and a `Provision` error aborts the config load: Caddy rejects the reload and keeps the previous configuration running.
- The check and the insert happen under the same lock, so concurrent handler provisioning cannot interleave into a missed duplicate.
- It needs no new global state and no change to how directives are parsed.

Alternatives considered:

- **Adapt-time check in `parseDirective`.** Rejected: per-site directives are parsed independently, and Caddy exposes no per-plugin finalization hook in the `httpcaddyfile` adapter. A process-global accumulator keyed by config file would be needed, which is unsafe under concurrent adapts and leaks across configs.
- **`App.Validate`.** Rejected for the ordering reason in Context: it may run before handlers register their hosts.
- **`App.Start` / at the top of `Reconcile`.** Works, since every host is registered by then, but it is later than necessary and puts a configuration error in the same path as network reconciliation. Registration is preferred so a bad config never reaches a running app.

### Compare normalized hostnames

`resolveHostToken` and `parseHostBlock` already normalize every host to lower case with any trailing dot removed, whether it came from a literal FQDN or a matcher. The duplicate check therefore compares `HostConfig.Host` values directly with exact string equality; no second normalization is required. This is what makes `Foo.Example.com.` collide with `foo.example.com`, and a matcher reference collide with the literal it resolves to.

### Error shape

The registration error names the host, e.g. `host "foo.example.com" is declared more than once; each cf_dns_manager directive must manage a distinct host`. The message must not depend on which declaration was seen first, because concurrent provisioning fixes an arbitrary winner. Caddy wraps it as a `provision http.handlers.cf_dns_manager_host:` failure.

## Risks / Trade-offs

- **`caddy adapt` alone does not catch it.** Adaptation produces JSON without provisioning, so the collision is reported by the load path (`caddy run`, reload, `caddy validate`), not by adapting the Caddyfile in isolation. This matches the requested behavior - fail whenever the configuration is loaded and processed - and is the cost of aggregating hosts at load time. A test exercises the load path (`caddy.Validate`), which is the same code a reload runs.
- **First-wins ordering.** With several colliding declarations, the error identifies a host rather than a specific pair of source locations. That is enough to find the copy-paste mistake; adding adapt-time source positions would require the adapt-time mechanism rejected above.

## Migration Plan

None: this only turns a previously silent misconfiguration into a load error. Existing valid configs, including those with many hosts, are unaffected. Rolling back is reverting the check.
