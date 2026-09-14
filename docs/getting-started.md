# Getting started

From zero to a reconciled DNS record in ~10 minutes. For running in Docker see
[docker-deployment.md](docker-deployment.md); for tunnels see
[cloudflare-tunnel.md](cloudflare-tunnel.md).

## 1. Create a Cloudflare API token

The plugin needs one token per managed zone, scoped as narrowly as possible:

1. Go to **My Profile → API Tokens → Create Token**, then **Create Custom Token**.
2. Permissions: **Zone → DNS → Edit** for a single zone (e.g. `example.com`).
3. Zone Resources: **Include → Specific zone → example.com**.
4. Create and copy the token (shown once).

That is all the plugin needs. It never requires Account-level permissions:

| Permission | Needed for |
| --- | --- |
| Zone → DNS → Edit | everything the plugin does (read, create, update, delete) |
| Account → any | **not needed** — tunnels are managed by `cloudflared`, not the plugin |

If you manage several zones with separate tokens, declare one `zone` line per
token (below). One token with Edit on several zones also works.

**Rotating a token:** create the new token, update the `api_token` in the
Caddyfile, reload. The old token can be revoked immediately after the reload —
the plugin only calls the API during a reconcile.

> The token is stored in the Caddy configuration JSON (baked from environment
> variables at adapt time) and in Caddy's autosaved config under `/config`.
> Protect that volume like a secret; do not expose the admin endpoint publicly.

## 2. Build Caddy with the plugin

```bash
xcaddy build \
    --with github.com/gmag11/caddy-cloudflare-dns-manager \
    --with github.com/caddy-dns/cloudflare
```

(`github.com/caddy-dns/cloudflare` is only needed for wildcard certificates via
ACME DNS-01.)

Verify:

```bash
./caddy list-modules | grep cf_dns_manager
```

Or use Docker — see [docker-deployment.md](docker-deployment.md); since the
module is registered with the Caddy build service, the official image plus
`caddy add-package` also works.

## 3. Minimal Caddyfile

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_API_TOKEN} prune
		instance my-server-1
	}
}

app.example.com {
	cf_dns_manager {
		host app.example.com
	}
	respond "app up" 200
}
```

What each line does:

- `zone ... api_token ...` — declares a managed zone. Without a declaration,
  any host under that zone is an adapt-time error.
- `prune` — (optional) delete this instance's orphaned records when a host is
  removed from the config.
- `instance` — (optional but recommended) stable ownership id; see
  [multiple servers](#multiple-servers-and-prune).
- `cf_dns_manager { host ... }` — opts the host in. Without it, a hostname is
  never announced in Cloudflare.

Run and reload:

```bash
CF_API_TOKEN=xxx ./caddy run --config Caddyfile
# later:
CF_API_TOKEN=xxx ./caddy reload --config Caddyfile
```

## 4. Verify

Caddy log on the first load:

```
cf_dns_manager  created record  {"host": "app.example.com", "record_type": "A", "content": "203.0.113.10", "proxied": true}
```

In Cloudflare (or with `dig`):

```bash
dig +short app.example.com        # → the detected public IPv4
```

The record carries the ownership comment `caddy-cf-dns:my-server-1`. Reload
again: nothing is written ("record already in sync" at debug level).

## 5. Common variations

```
# Pin an explicit IPv4 (e.g. behind a NAT you control)
ip 203.0.113.7

# Manage IPv6 too: detected public IPv6
ip6 auto

# ...or a literal (e.g. a Tailscale ULA, forced DNS-only automatically)
ip6 fd7a:115c:a1e0::1

# DNS-only instead of proxied
proxied no

# Claim a record that existed before the plugin
force_adopt

# Publish through a Cloudflare Tunnel instead of an address
tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
```

See the README for the full subdirective table and
[cloudflare-tunnel.md](cloudflare-tunnel.md) for the tunnel walkthrough.

## Multiple servers and prune

Several Caddy servers can share one zone safely: every record is tagged with
`<tag_prefix>:<instance>`, and a server only ever touches records carrying its
own tag. Two rules make this work:

- **Use a stable `instance` id per server** (not the default hostname when
  running in Docker — the container id changes on every recreate).
- `prune` only deletes records tagged with **this** instance whose host is no
  longer declared, so server A never prunes server B's records.

If a server is decommissioned, its records are cleaned by removing its
declarations (prune) or by deleting its tagged records by hand; they are
identifiable in the Cloudflare dashboard by the comment.

## Troubleshooting quick list

- *Nothing is created* → the host must be under a declared `zone`, and the
  block must opt in with `host ...`.
- *"not under any declared cf_dns_manager zone"* at adapt → declare the zone.
- *Record exists but untouched* → it is untagged; use `force_adopt`.
- Full details: [troubleshooting.md](troubleshooting.md).
