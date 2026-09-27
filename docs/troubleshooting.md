# Troubleshooting

Symptoms → causes → fixes, collected from real failures. Log lines are the
plugin's; Cloudflare error codes appear in `reconcile host failed` messages.

## Configuration errors (config never loads)

### `host ... is not under any declared cf_dns_manager zone`

The host is not a subdomain of any `zone` declared in the global block. Add the
zone (with its token) or fix the hostname.

### `tunnel cannot be combined with ip/ip6/proxied`

A tunnel host routes via CNAME and has no address plan. Remove the `ip`, `ip6`
or `proxied` subdirective from that host block. `force_adopt` is allowed.

### `tunnel must be a UUID, got "..."`

The `tunnel` subdirective needs the tunnel's UUID (8-4-4-4-12 hex), as shown by
`cloudflared tunnel list`. Copy it without quotes.

### `the cf_dns_manager global options block is required`

A per-site `cf_dns_manager` directive exists but no global `cf_dns_manager { zone
... }` block is declared.

### `host specified more than once` / `tunnel specified more than once`

One directive manages exactly one host; use separate directives for more hosts.

### `host "..." is declared more than once; each cf_dns_manager directive must manage a distinct host`

Two `cf_dns_manager` directives declare the same hostname. The usual cause is a
site block that was copied and whose `host` was never changed, so both blocks
name one record (and, for tunnel hosts, would write conflicting ingress rules).
Pick one block to own the host and change or remove the other's `host`.
Reporting happens when Caddy loads the configuration, so `caddy adapt` alone
does not show it; `caddy validate` or a reload does.

## Reconcile-time (Caddy loads, DNS does not match)

### Cloudflare error `81054` — "A CNAME record with that host already exists"

A CNAME and an address record cannot coexist at the same name. The plugin clears
*owned* conflicting records automatically when you switch a host between
tunnel and address. If the conflicting record is **untagged** (created by hand,
`cloudflared tunnel route dns`, or another tool), the plugin leaves it alone:

```
host name has an untagged record that blocks the desired type ...
remedy: remove the record or add force_adopt
```

Fix: delete the record in the Cloudflare dashboard, or add `force_adopt` to the
host block and reload.

### Untagged records are never touched

```
existing untagged record not owned by this instance; leaving unchanged (add force_adopt to adopt)
```

The plugin's ownership model is the record comment. A record without the plugin's
tag (`<tag_prefix>:<instance>`) is treated as someone else's — even your own
manual record. Add `force_adopt` to claim it, or tag it manually with the
instance's tag.

### `record of another instance must never be pruned` / records of a dead server linger

Prune only deletes records tagged with **this** instance. Records of a
decommissioned server keep its old tag. Fix: delete them by hand (search the
comment in the dashboard) or, before retiring the server, set its
`tag_prefix`/`instance` appropriately. See
[getting-started.md](getting-started.md#multiple-servers-and-prune).

### Nothing is created and detection failed

```
could not detect public IPv4; leaving existing auto-IP records unchanged and skipping new auto-IP records
```

Auto-IP hosts need a working detection endpoint. Test it from the same network:

```bash
curl -s https://cloudflare.com/cdn-cgi/trace | grep ip=
curl -s https://api6.ipify.org          # IPv6 only
```

Common causes: outbound filtering, IPv6 without connectivity (the default
`ip6_url` is IPv6-only and a v4 answer is rejected on purpose), or a captive
portal. Override with `ip_url` / `ip6_url`, or pin the address with `ip` /
`ip6 <literal>` — explicit addresses never depend on detection.

### Wildcard site: nothing reconciled

A site block `*.example.com` itself is never reconciled (it is a wildcard, not a
record). Declare `cf_dns_manager { host ... }` inside `handle` blocks for the
concrete hosts you want published.

### Config loads but nothing changes at all: "config is unchanged"

Caddy skips the whole load (and the plugin's reconcile) when the adapted JSON is
byte-identical. Editing a comment or an untouched env var does not trigger a new
reconcile. To force one: change the config meaningfully, or restart Caddy.

### Rate limiting / transient API failures

```
cloudflare api ... rate limited (429)
```

The plugin does not retry; the whole reconcile for that zone fails. It will be
retried on the next reload (or restart). Free plans allow ~1200 requests/5 min
per zone; a handful of hosts per reload is nowhere near it unless reloads are
automated in a tight loop.

## Tunnel-specific

### Host resolves but returns 404 from `server: cloudflare`

The CNAME exists but the tunnel has no ingress rule matching that hostname.

- **With the `account` block (plugin-managed ingress):** the plugin should have
  written the rule. Check the log for `wrote tunnel ingress plan`; if it is
  missing, the host is probably not declared with `tunnel <name>` in its
  `cf_dns_manager` block. A 404 can also mean an earlier catch-all rule (a
  preserved foreign wildcard) is matching first — look for the
  `preserved wildcard ingress rule shadows a declared host` warning.
- **Without the `account` block:** the plugin manages DNS only. Add the rule
  yourself in the tunnel configuration, and consider enabling plugin-managed
  ingress so this cannot drift again.

### `tunnel hosts declared but no tunnel API token configured`

Informational, not an error. Tunnel hosts are declared but the global block has
no `account` line, so the plugin skipped ingress management (it will not call
the Tunnel API without a credential). Either add:

```
account <token>   # token needs Account -> Cloudflare Tunnel -> Edit
```

or ignore the line if you maintain the ingress rules yourself.

### `could not determine the Cloudflare account id; skipping tunnel ingress management`

A tunnel token is configured, but no managed zone resolved successfully in this
run — the zone lookup is what supplies the account id for the Tunnel API path.
Check the zone errors reported alongside this warning (`zone "..." not found for
this token`, DNS/API failures). As a workaround, pin the account explicitly:

```
account_id <account-id>
```

### `refusing to manage tunnel ingress: it is locally managed`

The tunnel's configuration reports `source: local`, meaning its ingress lives in
a `config.yml` on the machine running `cloudflared`. Writing through the API
would report success and change nothing, so the plugin refuses rather than
pretending. Recreate the tunnel as remotely-managed, or drop the `account` block
and keep maintaining `config.yml` yourself.

### A removed host still appears in the tunnel's Routes

Expected in these cases. The plugin removes a route automatically only when it
also deletes that hostname's DNS record in the same run, and only in a
`prune`-enabled zone:

- **The zone has no `prune`.** Nothing is deleted, so there is nothing to
  correlate with. Enable `prune` on the zone and reload.
- **The host was switched from `tunnel` to `ip` without `prune`.** The CNAME had
  to go — Cloudflare forbids it coexisting with an address record — but the
  route is only cleaned up under `prune`. It is now unreachable (the name
  resolves to your address, not through the tunnel) and no later run will touch
  it, so delete it by hand if it bothers you.
- **The deleted record names no tunnel.** An A record never implies a tunnel
  route; neither does a CNAME pointing somewhere other than
  `cfargotunnel.com`. The plugin logs that it cannot attribute the route and
  says so rather than guessing.
- **The deleted record names a tunnel that is not registered.** Registration in
  the global block is what authorises an ingress write, so a tunnel absent from
  it is left untouched even when the record clearly points at it. The log names
  the hostnames; add the tunnel to the global block and reload.

Removing the last tunnel host is **not** on this list: the deleted CNAME names
the tunnel, so its route is pruned like any other dead route.

### `tunnel "..." looks like a UUID and no registration uses it`

Every configuration written before the tunnel registry was introduced carries a
raw UUID in the host block, so this is the migration error. Tunnels are now
registered once, by name, in the global block:

```
{
        cf_dns_manager {
                zone example.com api_token {$CF_DNS_TOKEN}
                tunnel edge 8a7f3c2e-1234-4567-89ab-cdef01234567
        }
}

app.example.com {
        cf_dns_manager {
                host app.example.com
                tunnel edge      # was: tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
        }
}
```

Take the UUID from the host block you already had, register it under any name,
and reference the name. A UUID-shaped value is deliberately rejected rather than
treated as an implicit registration: implicit registration would restore the hole
the registry closes, where a valid-looking typo silently wrote to a tunnel that
does not exist.

### `tunnel "..." is not registered; registered tunnels: ...`

The host references a name the global block does not register. The error lists
the names that are registered, so the usual cause is a typo or a name registered
in a different Caddyfile.

### A route was pruned and I want it back

Re-declare the host with `tunnel <name>`. Both halves are recreated from the
same declaration: the DNS record and the route. Prune is not reversible on its
own, which is why it only ever removes routes whose record it deleted moments
earlier.

### An undeclared subdomain resolves

The plugin never creates a wildcard DNS record, so a `*.<zone>` record came from
elsewhere — most often the dashboard's **Published application** flow, which
creates the DNS record alongside the ingress rule when you give it a wildcard
hostname. The record has no ownership tag, so the plugin will neither update nor
prune it: delete it in the dashboard. After that, only hostnames you declare
resolve.

> Note the distinction: a `*.<zone>` **Caddy site block** is unrelated and
> should stay — it is how Caddy serves every subdomain with one certificate. The
> problem is the DNS record, not the site block.

### 522 or `remote error: tls: internal error` through the tunnel

The ingress rule points at an HTTPS origin, but `cloudflared` presents the wrong
SNI. By default it uses the *service URL's* hostname — so `service:
https://caddy:443` sends SNI `caddy`, and a Caddy site using a wildcard
certificate (`*.example.com`) has no certificate for that name. The handshake
fails and every request becomes a 502/522.

**The plugin sets `originRequest.matchSNItoHost: true` on every rule whose
service is `https://`, so this should not happen.** If you still see it:

- Check the rule's service scheme. The option is only added for `https://`,
  because that is the only service type `cloudflared` reads it from. A `unix+tls:`
  socket does speak TLS but is left alone; so is a plain `http://` origin, which
  performs no handshake at all.
- Check whether something external rewrote the route. The plugin repairs a
  missing option on the next reconcile, so a single reload should clear it.
- If a rule carries `originServerName`, that pins the SNI deliberately. The
  plugin does not remove it, and the two options can conflict — drop one.

```
# cloudflared log
error="Unable to reach the origin service ... remote error: tls: internal error" ingressRule=0 originService=https://caddy:443
```

This is the same underlying cause as the redirect-loop entry below, from the
other side: one is the wrong port, this one is the wrong SNI.

### 308 redirect loop through the edge

`cloudflared` points at the plain-HTTP port of an origin that redirects
HTTP→HTTPS (Caddy's default), so every request bounces. Point the ingress at the
HTTPS listener with `originRequest.originServerName`, or serve that hostname
plain-HTTP intentionally. See [docker-deployment.md](docker-deployment.md#4-adding-a-cloudflare-tunnel).

### The CNAME was deleted and the address record did not come back

Fixed behavior since the tunnel feature: the plugin clears the owned CNAME before
creating the address record in the same reload. If you are on an older build,
reload once more or restart Caddy. (`docker compose restart caddy` is enough.)

### Tunnel UUID typo: CNAME created but nothing answers

A valid-UUID typo creates a CNAME to a tunnel that does not exist. The edge
rejects or black-holes it. Verify the UUID against `cloudflared tunnel list`.
With plugin-managed ingress, a wrong UUID also means the plugin wrote the ingress
plan to a *different* tunnel — fix the UUID and reload so it is corrected.

## General

### Enable debug logging

```
{
	log {
		level DEBUG
	}
	...
}
```

The plugin logs every decision (create/update/adopt/prune, "already in sync")
at info/debug level with host, zone, record type and content.

### Inspect what the plugin would do without side effects

Not supported: there is no dry-run flag. Read the log after a reload, or point
`apiBase` at a mock in a test (see [contributing.md](contributing.md), testenv).
