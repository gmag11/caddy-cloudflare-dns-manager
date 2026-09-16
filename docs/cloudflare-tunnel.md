# Configuring a Cloudflare Tunnel connection

This guide walks through publishing a hostname through a [Cloudflare
Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/)
using this plugin.

With a tunnel, visitors reach Cloudflare's edge and traffic is carried to your
server by an outbound `cloudflared` connection — no inbound ports, no public
origin IP. The plugin's job is only the **DNS routing record**: it ensures a
proxied `CNAME` from your hostname to `<tunnel-id>.cfargotunnel.com` exists.
The tunnel itself, its credentials and its ingress rules are managed outside
the plugin (via `cloudflared` or the Zero Trust dashboard).

## How it fits together

```
Visitor ──▶ Cloudflare edge ──▶ CNAME <tunnel-id>.cfargotunnel.com
                (proxied)                │
                                         ▼  outbound connection
                                   cloudflared (your server)
                                         │
                                         ▼
                                   local service / reverse proxy
```

The plugin creates the CNAME. `cloudflared` makes the connection and forwards
requests to your local service according to its ingress rules.

> Running everything in Docker? [docker-deployment.md](docker-deployment.md)
> has ready-to-use `docker compose` examples with a `cloudflared` sidecar.

## What you need

- A Cloudflare account with a zone managed by this plugin (`cf_dns_manager`
  global block configured).
- A `cloudflared` installation on the server. In Docker, the
  `cloudflare/cloudflared` image is enough.
- The tunnel **UUID**. Every step below tells you where to find it.
- For the plugin to manage ingress (recommended): an account-scoped token with
  **Account → Cloudflare Tunnel → Edit**.

> **Recommendation: use a remotely-managed tunnel.** Cloudflare recommends it
> "for most use cases", and this plugin is designed around it. With a
> remotely-managed tunnel the ingress configuration lives on Cloudflare, so the
> plugin can write it, no local file needs reloading, and a change takes effect
> without restarting `cloudflared`. Locally-managed tunnels are for local
> development, testing, or legacy setups — see
> [Locally-managed tunnels](#locally-managed-tunnels-not-recommended) below.

## Step 1 — Create the tunnel (dashboard)

1. In the Cloudflare dashboard go to **Networking > Tunnels** and select
   **Create a tunnel**.
2. Choose **Cloudflared**, name the tunnel (e.g. `my-server`) and create it.
3. Copy the **install/run command** shown. It contains a token; run it on the
   server, for example:

   ```bash
   cloudflared service install <TOKEN>
   # or, without installing a service:
   cloudflared tunnel --no-autoupdate run --token <TOKEN>
   ```

4. Note the tunnel's **UUID** (shown on the tunnel's page).

**Do not add a public hostname here.** The plugin writes the ingress plan; a
hand-made published application would be preserved as a foreign rule instead of
being replaced, and it also creates a DNS record without the plugin's ownership
tag — which the plugin will never update or prune.

Alternatively, create the tunnel with the API (`POST /accounts/{id}/cfd_tunnel`
with `config_src: cloudflare`); see Cloudflare's "Create a tunnel (API)" guide.
The token is available from the dashboard or `GET /accounts/{id}/cfd_tunnel/{id}/token`.

### Locally-managed tunnels (not recommended)

A locally-managed tunnel keeps its ingress in a `config.yml` on the host, and
`cloudflared` is started with `--config`/`--cred-file` instead of `--token`:

```bash
cloudflared tunnel login            # browser login, writes cert.pem
cloudflared tunnel create my-server # prints the tunnel UUID and writes <UUID>.json
```

```yaml
# config.yml
ingress:
  - hostname: app.example.com
    service: http://caddy:80   # or https://caddy:443
  - service: http_status:404   # required catch-all
```

```bash
docker run -d --name cloudflared --restart unless-stopped \
  -v "$PWD/tunnel:/etc/cloudflared:ro" \
  cloudflare/cloudflared:latest \
  tunnel --no-autoupdate \
    --config /etc/cloudflared/config.yml \
    --cred-file /etc/cloudflared/<TUNNEL-UUID>.json \
    run <TUNNEL-UUID>
```

The plugin manages the DNS CNAME for such a tunnel exactly as for a remote one,
but it **will not manage its ingress**: a write through the API would report
success and change nothing, because the running `cloudflared` reads only its own
file. The plugin logs an error naming this and skips the write, so you keep
editing `config.yml` yourself. If you see that error, recreate the tunnel as
remotely-managed (or drop the `account` block to silence it).

Keep `cert.pem` secret: it can create, delete and reconfigure **every** tunnel
in the account, unlike the tunnel credentials file which only runs this one
tunnel. Add both to `.gitignore`:

```gitignore
tunnel/cert.pem
tunnel/*.json
```

## Step 2 — Point the plugin at the tunnel

Add the `tunnel` subdirective to the host's `cf_dns_manager` block. It takes the
tunnel UUID. To let the plugin write the ingress rules too, add the
account-scoped `account` line and a default service to the global block:

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_DNS_TOKEN}

		# Account-scoped token (Account -> Cloudflare Tunnel -> Edit). Optional:
		# without it the plugin manages DNS only and never calls the Tunnel API.
		# The account id is derived from the zone: a tunnel always lives in its
		# zone's account.
		account {$CF_TUNNEL_TOKEN}

		# Destination for tunnel traffic no declared host claims. It becomes the
		# tunnel's catch-all rule, so no wildcard DNS record is needed.
		# Unset -> the plugin writes http_status:404 (fail closed).
		tunnel_default_service https://caddy:443
	}
}

*.example.com {
	tls {
		dns cloudflare {$CF_DNS_TOKEN}
	}

	@app host app.example.com
	handle @app {
		cf_dns_manager {
			host @app
			tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
			# tunnel_service ssh://caddy:22   # optional per-host destination
			# force_adopt                    # only if a CNAME already exists untagged
		}
		reverse_proxy localhost:8080
	}
}
```

Then reload Caddy:

```bash
caddy reload --config /etc/caddy/Caddyfile
# or, in Docker: docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
```

On reload the plugin:

- creates a **proxied CNAME** `app.example.com → <uuid>.cfargotunnel.com`;
- records it with the ownership tag, so it can update it on drift and prune it
  when you remove the declaration (in `prune`-enabled zones);
- skips public-IP detection for this host entirely;
- **writes the tunnel's ingress plan** (with the `account` line): one rule per
  declared hostname, then the catch-all from `tunnel_default_service`.

### The two halves of a tunnel host

A tunnel host is complete only when both halves exist:

```
app.example.com ──CNAME──▶ <uuid>.cfargotunnel.com     (DNS, per host)
app.example.com ──rule ──▶ https://caddy:443           (ingress, per host)
       anything else ─────▶ https://caddy:443           (catch-all, no DNS record)
```

The plugin writes one rule per hostname you declare, plus a single catch-all.
This is how the default route is expressed **without** a wildcard DNS record:
the catch-all is an ingress concept with no DNS counterpart, so undeclared
subdomains do not resolve at all. Add a host to the Caddyfile and it gets both
halves automatically.

### Rules for the `tunnel` subdirective

- `tunnel <uuid>` is **mutually exclusive** with `ip`, `ip6` and `proxied`. A
  tunnel host has no address plan and its CNAME is always proxied, so combining
  them is a configuration error at adapt time.
- The UUID must be a canonical `8-4-4-4-12` hexadecimal UUID.
- `force_adopt` is allowed and is how you adopt a CNAME that was created outside
  the plugin (for example by `cloudflared tunnel route dns`), which has no
  ownership tag.
- `tunnel_service <service>` sets this hostname's ingress destination, overriding
  the global `tunnel_default_service`. It requires `tunnel` in the same block.

### Rules for the `account` and `tunnel_default_service` options

- `account <token>` supplies the account-scoped credential for the Tunnel API.
  Declared once in the global block. The account id is **not** configured: the
  plugin takes it from the zone lookup, which is exact because Cloudflare only
  proxies a tunnel for records in the same account. `account_id <account-id>`
  overrides that derivation if you ever need to.
- Without it, the plugin reconciles DNS only and logs that ingress management
  needs the credential. Nothing else changes.
- `tunnel_default_service <service>` accepts the same service values as
  `tunnel_service`: `http`, `https`, `tcp`, `ssh`, `rdp` and `smb` URLs,
  `unix`/`unix+tls` socket paths, or `http_status:<code>`.
- Unix sockets are written as a path, not a URL: both `unix:/run/app.sock`
  (the spelling used in Cloudflare's own examples) and `unix:///run/app.sock`
  are accepted, because `cloudflared` trims the prefix and uses whatever
  follows as the filesystem path. `unix+tls:` speaks TLS over the socket.
- The default is written as the tunnel's **final catch-all rule**. Because it
  has no hostname, it creates no DNS record — that is what keeps undeclared
  subdomains unresolvable.

## Step 3 — Verify

```bash
# DNS: the CNAME should resolve to Cloudflare's proxied addresses
dig +short app.example.com

# Ingress: the plugin's plan is on the tunnel
#   dashboard: Networking > Tunnels > <tunnel> > Routes
#   logs:      "wrote tunnel ingress plan"

# End to end: through the tunnel to your local service
curl -sS https://app.example.com/

# The default route must NOT create a wildcard record: an undeclared
# subdomain should not resolve. If it does, a wildcard DNS record exists and
# was created outside the plugin (delete it).
dig +short nothing-declared.example.com    # -> empty
```

Check the Caddy log for the reconcile actions:

```
cf_dns_manager  created record  {"host": "app.example.com", "record_type": "CNAME", "content": "<uuid>.cfargotunnel.com", "proxied": true}
cf_dns_manager  wrote tunnel ingress plan  {"tunnel_id": "<uuid>", "rules": 2, "preserved_foreign_rules": 0, "catch_all_origin": "configured default"}
```

If Cloudflare already had a hand-made CNAME for that hostname (no ownership
tag), the plugin leaves it untouched unless you add `force_adopt`. In that case
you will see a log line explaining that the record is not owned; add
`force_adopt` and reload to let the plugin manage it.

## Switching a host between a tunnel and an IP

You can move a hostname between tunnel-backed and address-backed just by editing
the directive; no manual DNS cleanup is needed.

**Address → tunnel:** remove `ip`/`ip6`/`proxied` and add `tunnel <uuid>`. On the
next reload the plugin deletes the owned A/AAAA record and creates the CNAME.

**Tunnel → address:** remove `tunnel` and add `ip` (or `ip6 auto`). On the next
reload the plugin deletes the owned CNAME and creates the A/AAAA record.

```
# before: tunnel
cf_dns_manager {
	host app.example.com
	tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
}

# after: direct address
cf_dns_manager {
	host app.example.com
	ip 203.0.113.7
}
```

Cloudflare does not allow a CNAME to coexist with A/AAAA at the same name, so
the plugin clears the conflicting owned record first. A record created outside
the plugin (untagged) blocks the switch: the plugin logs an error and leaves it
alone unless you add `force_adopt`.

## Removing a tunnel host

Delete the host's `cf_dns_manager` block (or the whole site block). On the next
reload, in a zone declared with `prune`, the now-orphaned CNAME — carrying this
instance's tag — is deleted. Without `prune`, the CNAME is left in place.

The hostname's **ingress rule is removed automatically when its record is pruned.**
In a `prune`-enabled zone the plugin deletes the CNAME, so the hostname stops
resolving; in the same reconcile it deletes the now-unreachable route. There is
no marker involved and nothing is inferred: the route is removed because the
plugin just made it dead.

Two cases leave the route in place, both inert:

- **The hostname's record was not deleted** (the zone has no `prune`). The route
  is preserved. In particular, switching a host from `tunnel` to `ip` always
  deletes the CNAME — Cloudflare forbids it coexisting with an address record —
  but the route is only cleaned up when the zone opted into `prune`.
- **This was the last tunnel host of the config.** With no tunnel declared the
  plugin cannot tell which one holds the route, and it will not scan the
  account's tunnels. It logs that manual removal is needed; the route is inert
  because the hostname no longer resolves.

The tunnel itself is not touched by the plugin; remove it separately with
`cloudflared tunnel delete <name>` or from the dashboard.

## Reference

### The `tunnel` subdirective (plugin side)

| Subdirective | Value | Notes |
| --- | --- | --- |
| `tunnel` | `<uuid>` | Marks the host tunnel-backed. Reconciles a proxied CNAME to `<uuid>.cfargotunnel.com`. |
| `tunnel_service` | `<service>` | Optional. This hostname's ingress destination, overriding `tunnel_default_service`. Requires `tunnel`. |
| `force_adopt` | — | Adopt an existing untagged CNAME (or an untagged A/AAAA blocking a switch). |

Global options that affect tunnels:

| Option | Value | Notes |
| --- | --- | --- |
| `account` | `<id> api_token <token>` | Account-scoped credential for the Tunnel API. Optional; without it the plugin never calls it. |
| `tunnel_default_service` | `<service>` | The tunnel's catch-all destination. Defaults to `http_status:404`. |

Notes:

- One directive manages one host; repeat the directive for more tunnel hosts.
  Multiple hosts can share the same tunnel UUID — each becomes its own ingress
  rule, so they may point at different services via `tunnel_service`.
- With the `account` line, the plugin manages both halves: the CNAME (per host)
  and the ingress rule (per host), plus the tunnel's catch-all.
- Ingress rules the plugin did not derive from your config are preserved
  untouched, and a `description`, `path` or `originRequest` you set on them is
  carried through. A preserved wildcard rule that also matches a declared
  hostname produces a warning, because rule order then decides the destination.
- **Route cleanup follows the DNS record.** In a zone declared with `prune`,
  when the plugin deletes the record that made a tunnel hostname reachable, it
  deletes that hostname's route in the same reconcile: the route can no longer
  receive traffic, so nothing is inferred. This covers both removing a host and
  switching one from `tunnel` to `ip`. A route whose record still exists is
  never touched, and a declared host's route is never removed.
- **Removing the last tunnel host leaves its route behind.** With no tunnel
  declared, the plugin cannot tell which tunnel holds the route, and it will not
  scan the account's tunnels to find out. It logs the orphan instead; delete it
  by hand in the dashboard. Removing one host among several is fully automatic.

### The tunnel ingress configuration (`cloudflared` side)

Every tunnel decides *where* each hostname goes, through its ingress rules. With
a remotely-managed tunnel those rules are stored on Cloudflare — and with the
`account` credential, written by this plugin. With a locally-managed tunnel they
live in the `config.yml` you maintain yourself.

Two hard rules either way:

1. **An ingress list must exist.** A locally-managed tunnel with an empty config
   fails: *"No configuration file was found"*. The `ingress` key is mandatory
   (it is the only thing you need — `tunnel:` and `credentials-file:` can come
   from the command line as shown above). The plugin always writes a valid list.
2. **The last rule must be a catch-all** (no `hostname`, no `path`). Validation
   fails otherwise: *"The last ingress rule must match all URLs"*. The plugin
   always emits one, from `tunnel_default_service` or the `http_status:404`
   fallback.

#### Minimal config (all hostnames → one Caddy)

If Caddy serves every hostname (routing by `Host`), a single catch-all rule is
enough — and if you let the plugin manage ingress, that is exactly what you get
for free. This is the dynamic setup: **add a host to the plugin and it works —
no ingress changes.**

```yaml
ingress:
  - service: https://caddy:443
    originRequest:
      matchSNItoHost: true
```

#### HTTPS origin and `matchSNItoHost`

The default SNI when connecting to your HTTPS origin is the *service URL's*
hostname (`caddy`), which will not match a certificate issued for your public
hostnames. `matchSNItoHost: true` makes `cloudflared` send the request's `Host`
as SNI, so Caddy presents the matching certificate (e.g. its `*.example.com`
wildcard) for every host. This is what makes the catch-all work with HTTPS and
multiple hosts.

**The plugin sets this for you.** Every rule it writes whose service is
`https://` carries `originRequest.matchSNItoHost: true`, including the catch-all.
You do not add it by hand, and a rule that loses it is repaired on the next
reconcile. The plugin merges its key into the existing origin request rather than
replacing it, so options you set yourself are preserved.

It is only added for `https://` services, and the reason is the service type
rather than whether TLS is involved: `matchSNItoHost` is read only by
`cloudflared`'s HTTP service. A `unix+tls:` socket also speaks TLS but is a
different service type, so it is left alone — writing the option there would
have no effect.

Alternatives, and why they are worse here:

| Option | Works with many hosts? | Notes |
| --- | --- | --- |
| `matchSNItoHost: true` | ✅ | SNI follows the request `Host`. **Set automatically by the plugin.** |
| `originServerName: <host>` | ❌ | A single fixed SNI; breaks the second host. Use only per-host rules, and note it can conflict with the managed option. |
| `noTLSVerify: true` | ✅ | Disables origin verification; last resort only. |
| Origin over plain `http://` | ✅ | Only if the origin does not redirect HTTP→HTTPS. No SNI involved. |

#### Scoping: wildcard or per-host rules

Rules match top to bottom; the first match wins.

```yaml
ingress:
  # Only your subdomains reach Caddy; anything else 404s at the edge.
  - hostname: "*.example.com"
    service: https://caddy:443
    originRequest:
      matchSNItoHost: true
  - service: http_status:404
```

- `"*.example.com"` also matches deeper names (`a.b.example.com`) — wildcards
  match at any depth.
- A plain catch-all (`- service: https://caddy:443`) routes **everything**,
  including hostnames you never declared to the plugin. Scope with a wildcard
  or an explicit list if the tunnel should only serve your domains.
- Route specific hostnames elsewhere with earlier rules (path matching and
  non-HTTP services like `ssh://`, `tcp://` are supported):

```yaml
ingress:
  - hostname: app.example.com
    path: /api/.*
    service: http://api:8081
  - hostname: "*.example.com"
    service: https://caddy:443
    originRequest:
      matchSNItoHost: true
  - service: http_status:404
```

#### Validate and test before restarting

```bash
# Syntax + the catch-all rule
docker run --rm -v "$PWD/tunnel:/etc/cloudflared:ro" \
  cloudflare/cloudflared:latest tunnel --config /etc/cloudflared/config.yml ingress validate

# Which rule matches a URL (first match)
docker run --rm -v "$PWD/tunnel:/etc/cloudflared:ro" \
  cloudflare/cloudflared:latest tunnel --config /etc/cloudflared/config.yml \
  ingress rule https://app.example.com/api/v1
```

Editing `config.yml` does not restart a running connector — recreate it (see
[docker-deployment.md](docker-deployment.md#5-reloading-configuration)).
