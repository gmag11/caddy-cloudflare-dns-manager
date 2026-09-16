# Design: Cloudflare Tunnel ingress management

## Context

The plugin reconciles DNS records declared per host via `cf_dns_manager` directives. Tunnel hosts (change `add-tunnel-dns-management`) reconcile a proxied CNAME to `<uuid>.cfargotunnel.com`. The **ingress** half — the rule that tells `cloudflared` where to send the request — is not managed: it must be created by hand in the Zero Trust dashboard or in a `cloudflared` `config.yml`.

That manual step is what forces the wildcard. The dashboard's only way to create a DNS record for a public hostname is the **Published application** flow, which requires a hostname; for a catch-all default it must be `*.<zone>`, and that flow also creates the `*.<zone>` **DNS record**. The wildcard then makes every undeclared subdomain resolve.

Verified facts this design rests on:

- Writing ingress by API does **not** create a DNS record. Cloudflare's API guide publishes an application with two separate calls: `PUT /accounts/{id}/cfd_tunnel/{tunnel_id}/configurations` (account-scoped `Cloudflare Tunnel Write`) and `POST /zones/{id}/dns_records` (zone-scoped `DNS Write`). The dashboard couples them; the API does not.
- `PUT .../configurations` accepts only `config.ingress[]` and `config.originRequest[]`. The response also carries `source: "local" | "cloudflare"` and a `version`.
- A catch-all (no `hostname`) is mandatory in the written configuration, both for `config.yml` and per the API guide. The dashboard UI does not require it, but the plugin writes via API.
- Remotely-managed tunnels are synchronised by `cloudflared` itself (`config_version` is documented as "used internally to sync cloudflared with the Zero Trust dashboard"), so no local reload is needed. `--config`/`--origincert` are "for locally-managed tunnels only"; `--token` is "for remotely-managed tunnels only". Cloudflare recommends remotely-managed tunnels "for most use cases".
- `GET /zones?name=<zone>` returns the zone's owning `account.id`, so the Tunnel API path needs no configured account id (see D1b).

Relevant existing code:

- `App`/`HostConfig` (`app.go`) — no account credential, no service override.
- `parseGlobalOption`/`parseZone`/`parseHostBlock` (`caddyfile.go`) — the Caddyfile surface to extend.
- `Reconcile`/`reconcileZone` (`reconcile.go`) — per-zone goroutine fan-out; hosts are grouped by zone, not by tunnel.
- `cloudflareClient.do` (`cloudflare.go`) — generic JSON request helper, reusable for a different base path.
- `hostsSnapshot` (`registry.go`) — hosts are appended from handler `Provision`, so **slice order is not deterministic**.

## Goals / Non-Goals

**Goals:**

- Derive the tunnel ingress plan from the declared configuration and reconcile it via the Tunnel configuration API.
- Eliminate the need for a wildcard DNS record: the default route is expressed as the mandatory catch-all, which has no DNS record.
- Support a per-host service override so hosts on one tunnel can reach different destinations.
- Never destroy anything the plugin did not create.
- Keep the account-scoped credential strictly optional and additive.

**Non-Goals:**

- Creating, deleting, renaming or migrating tunnels.
- Path-based rules, Access settings, or `originRequest` tuning per rule.
- Managing ingress for locally-managed tunnels (Caddy cannot reload `cloudflared`; write-then-reload is impossible).
- Deleting ingress rules removed from the config (see D6).
- Changing DNS reconciliation, ownership tagging, or DNS prune semantics.

## Decisions

### D1: Account credential is optional, additive and token-only

`account <token>` is a **separate** credential from the per-zone DNS tokens, both in config shape and in code path. Without it, the plugin makes zero Tunnel API calls and behaves byte-for-byte as today; a tunnel host's ingress is left alone and a log line explains why.

*Rationale*: the account token carries `Cloudflare Tunnel Write`, which can reconfigure **every tunnel in the account** — a materially larger blast radius than a zone-scoped `DNS Write`. Coupling it to DNS management would silently escalate the permissions of every existing deployment. Keeping it opt-in means the default posture is unchanged.

*Alternative rejected*: piggyback the account token on each `zone` entry. It would duplicate a single account credential across zones and blur the DNS/tunnel permission boundary.

*Alternative rejected*: a global "manage ingress" boolean with a mandatory account token. It makes the credential mandatory, forcing every user to accept the blast radius.

### D1b: The account id is derived, not configured

The Tunnel configuration endpoint is `/accounts/{account_id}/cfd_tunnel/{tunnel_id}/configurations` — the account id is a path parameter the API does not infer from the token. Rather than requiring the user to supply it, the plugin takes it from the zone lookup it already performs for reconciliation: `GET /zones?name=<zone>` returns `result[0].account.id`.

*Rationale*: the derivation is **exact, not a heuristic**. Cloudflare only proxies traffic through a `cfargotunnel.com` CNAME for DNS records in the same account, so a working tunnel and its zone necessarily share an account. Verified against a real zone-scoped token: the lookup returns the owning account id even though the token has no account-level permission.

*Consequences*: one fewer config field, and one fewer failure mode (a wrong account id would surface as an opaque 403/404 from the Tunnel API). `account_id <id>` remains as an escape hatch for the case where the derivation cannot work — e.g. every zone lookup failing while ingress must still be managed.

*Alternative rejected*: `GET /accounts` to enumerate the token's accounts. Unverified whether a `Cloudflare Tunnel Write` token can list accounts, and it would add an API call for information the zone lookup already returns.

*Alternative rejected*: decode the account id from the `cloudflared` run token (its payload contains `a`). The plugin never sees that token — it belongs to the `cloudflared` process, not the Caddy config.

### D2: Ordering is derived, not arbitrary

Rules are ordered by `(rule kind, hostname)`: declared host rules sorted by FQDN, then the default rule last. Sorting is required because `hosts` accumulates from handler `Provision` calls that Caddy may run concurrently, so slice order is non-deterministic and would produce spurious drift.

*Alternative rejected*: preserve declaration order. Not obtainable — the Caddyfile adapter does not expose per-directive sequence numbers to the app.

*Alternative rejected*: sort by service. Groups unrelated hosts together and makes the dashboard view harder to read.

### D3: Read-modify-write, mutating only `ingress`

The reconcile step is `GET .../configurations`, replace `config.ingress` with the derived plan, `PUT` the whole `config` object back. Everything else in `config` — top-level `originRequest`, `warp-routing` — is echoed back untouched, because it is round-tripped rather than reconstructed.

*Rationale*: `PUT` replaces the configuration document. Reconstructing it from scratch would silently erase settings the plugin does not model (notably `warp-routing`, which would break private-network access).

### D4: Drift detection compares only managed fields

To decide whether to write, the plugin compares the **derived plan** against the current rules on only the fields it manages: `hostname` (or absence, for the catch-all) and `service`. It does not deep-equal the whole rule, and it ignores `originRequest` on rules it did not author.

*Rationale*: Cloudflare normalises and fills defaults (e.g. an empty `originRequest` object, `version` bumps). A deep equality check against a server-normalised document would report drift on every run and issue a write every reload — an idempotence bug that is invisible in unit tests with a fixed mock.

### D5: Foreign rules are preserved by hostname, and shadowing is warned about

A rule whose `hostname` is not declared by this config is preserved in place (before the default rule). A rule whose `hostname` exactly matches a declared host is replaced by the derived rule.

Because a preserved foreign **wildcard** rule can match a hostname the plugin also declares, the plugin logs a warning naming the shadowing rule and the affected host. Rules match top-to-bottom, so the effective destination depends on ordering the plugin does not own.

*Rationale*: silently preserving a wildcard that shadows a declared host would produce the exact "resolution goes somewhere unexpected" class of bug this change exists to eliminate.

### D5b: Unmanaged rule metadata is carried over, not erased

A write replaces the whole configuration, so any field the plugin neither models nor round-trips is lost. `Path` and `OriginRequest` are raw JSON and survive by construction; `description` is an ordinary string field, so it is modelled explicitly and, for rules the plugin rewrites, inherited by hostname from the rule currently there.

*Rationale*: verified against the live API that `description` is accepted and persisted on a Free-plan zone, so descriptions are something users actually set from the dashboard. Erasing them on every reconcile would be a silent data loss of exactly the kind D3 avoids for `warp-routing`.

This is **not** an ownership claim: the plugin never authors or clears a description, and a description alone is not drift. Enforcing ownership via `description` (the ingress equivalent of the DNS comment tag, which would unlock ingress prune and reopen D6) is deliberately left out — the field is only preserved.

*Alternative rejected*: leaving `description` unmodelled and relying on the raw-JSON treatment. It only protects fields the decoder does not see; a known field on a rewritten rule is dropped because the derived rule simply has no value for it.

### D6: Undeclared rules are never deleted — and that is a deliberate limitation

Ingress rules have **no comment/ownership field**, unlike DNS records which carry `<tag_prefix>:<instance>`. Ownership by convention is therefore impossible: a rule with a given hostname is indistinguishable from a hand-made one. The plugin SHALL NOT delete rules it cannot prove it wrote.

Consequence, accepted and documented: removing a host from the config leaves its ingress rule on the tunnel. The leftover is **inert** because the DNS record is pruned (in a `prune`-enabled zone), so the hostname no longer resolves. Manual cleanup in the dashboard is the remedy.

*Alternative rejected*: an opt-in ingress prune based on "delete every rule that is neither declared nor a catch-all". This would delete hand-made rules on first run — destroying configuration the plugin never owned, exactly the failure mode the DNS ownership model was built to prevent.

*Alternative rejected*: persist a state file listing previously written rules. Introduces state that must be synchronised across instances, is lost when the container is recreated, and adds a new failure mode (stale state deleting a re-created rule).

### D7: `http_status:404` as the fail-closed default

When `tunnel_default_service` is unset, the catch-all serves `http_status:404` rather than routing to a guessed origin.

*Rationale*: the plugin cannot know the origin's service URL. Routing unmatched traffic to a guessed `https://caddy:443` would republish every hostname resolving through the tunnel, including ones the user never declared. Failing closed keeps the blast radius at zero, and one config line opts into the convenient behaviour.

### D8: Ingress reconciles per tunnel, after DNS, without blocking it

`Reconcile` keeps its per-zone DNS fan-out and gains a second phase: group the snapshot by (account, tunnel UUID), then reconcile one plan per tunnel. Because a tunnel's hosts can span zones, and because grouping by zone would fragment plans, the phase runs **after** the zone `WaitGroup`, over the host snapshot.

Failures are collected into the existing error aggregation, not fatal: a Tunnel API failure (rate limit, transient 5xx) SHALL NOT prevent DNS reconciliation, and vice versa. A partial failure leaves DNS correct and reports the ingress error, which the next reload retries.

### D9: Locally-managed tunnels are refused, not guessed

A `GET .../configurations` returning `source: local` means the tunnel's ingress lives on the host running `cloudflared`. The plugin SHALL log an error naming the remedy and skip the write, rather than writing a configuration that the running `cloudflared` would ignore.

*Rationale*: `cloudflared` started with `--config` reads its own file; an API write would appear to succeed and change nothing — the worst failure mode (silent no-op).

### D10: One HTTP helper, two clients

The Tunnel API uses the same `Authorization: Bearer`, JSON envelope and error code (e.g. `81044`) as the DNS API. The existing request helper is reused with an account-scoped path prefix (`/accounts/{id}/cfd_tunnel/...`) via a small dedicated type, rather than duplicating transport, retry and error-decoding logic.

### D11: Service value validation is delegated to Cloudflare, with a local sanity check

The plugin validates that a service is syntactically one of `http/https/unix/unix+tls/tcp/ssh/rdp/smb` (with a port) or `http_status:<code>`, so a typo fails at adapt time. It does not attempt to validate reachability; the API and `cloudflared` remain the authority.

## Risks / Trade-offs

- **[Account token blast radius]** → Opt-in and documented; configs that omit `account` are unaffected. Docs state plainly that the token can reconfigure every tunnel in the account.
- **[Server-side normalisation causes perpetual drift]** → D4 compares only managed fields. A mock test must simulate normalisation (add `originRequest: {}`, bump `version`) and assert no write occurs.
- **[A `PUT` clobbers `warp-routing`]** → D3 round-trips the whole `config` object; a test asserts a `warp-routing`-bearing configuration survives a reconcile.
- **[Manual rules for a declared hostname are overwritten]** → Documented: `hostname` is the plugin's ownership key for ingress. Users needing a custom rule for a declared hostname express it via `tunnel_service`, or keep the hostname undeclared to the plugin so it is preserved as foreign.
- **[Leftover rules accumulate]** → Accepted limitation (D6). Inert once DNS is pruned; documented in troubleshooting.
- **[In-flight race with the dashboard]** → Read-modify-write can lose an edit made between `GET` and `PUT`. Inherent to the API, which has a `version` field but no documented compare-and-swap. Mitigation: writes are rare and only on drift; docs recommend not editing ingress for declared hostnames from the dashboard.
- **[Cluster of hosts on one tunnel fails together]** → One tunnel = one write. Batched by design; a failure defers all hosts on that tunnel to the next reload.
- **[Remotely-managed requirement surprises local users]** → Explicit error naming the remedy (D9), plus the `testenv` migration and a docs note recommending remote mode.

## Migration Plan

1. Land the code with the account credential unused by any existing config: no behavioural change.
2. Existing deployments opt in by adding the `account` block and a `tunnel_default_service`; the first reconcile writes the derived plan.
3. Because D4 only writes on drift and D5 preserves foreign rules, the first write on an already-working tunnel is a no-op unless the derived plan differs.
4. Rollback: remove the `account` block. The plugin stops touching ingress; previously written rules remain and continue to work.

## Open Questions

- Whether `cloudflared` picks up a remotely-managed configuration change immediately or on its next reconnect (`config_version` suggests polling, but the interval is undocumented). Affects the "how long until a new host routes" expectation in the docs. Verification: write a rule via API and watch `cloudflared` logs and an end-to-end request.
- Whether `PUT .../configurations` rejects a body whose `ingress` lacks a catch-all with a specific error code, or accepts and normalises it. Affects only the error message a broken derivation would produce; the plugin always emits one.
- Exact behaviour of `GET .../configurations` for a tunnel that has never been configured (empty `config` vs `config: null` vs 404). Handled defensively by treating an empty/missing `ingress` as "no rules".
