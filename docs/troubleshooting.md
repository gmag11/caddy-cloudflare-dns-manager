# Troubleshooting

Symptoms → causes → fixes, collected from real failures. Log lines are the
plugin's; Cloudflare error codes appear in `reconcile host failed` messages.

## Adapt-time errors (config never loads)

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

The CNAME exists (plugin did its part) but the tunnel has no ingress rule for
that hostname. Add it to the tunnel configuration (dashboard route or
`config.yml`) and recreate/restart `cloudflared`.

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
