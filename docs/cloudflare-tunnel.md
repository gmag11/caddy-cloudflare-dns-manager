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

## Step 1 — Create the tunnel

Choose one of the two Cloudflare tunnel management modes. The DNS configuration
in this guide is identical for both; only where the ingress rules live differs.

### Option A — Remotely-managed (dashboard, recommended)

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

4. In the tunnel's **Routes** tab, add a **Published application**: enter the
   hostname (e.g. `app.example.com`) and the local service URL (e.g.
   `http://localhost:8080`). The dashboard can create the DNS route for you —
   see the note in Step 3 if it does.

The ingress rules live in the dashboard for this mode.

### Option B — Locally-managed (`cloudflared` config file)

```bash
cloudflared tunnel login            # browser login, writes cert.pem
cloudflared tunnel create my-server # prints the tunnel UUID
```

`tunnel create` writes a credentials file named `<TUNNEL-UUID>.json` (default
location `~/.cloudflared/`). Keep it secret; it authenticates this tunnel.

#### Doing it with the Docker image (no host install)

The same two commands work with the official container image — useful when the
host has no `cloudflared` installed, and it is exactly how the test environment
provisions its tunnel directory.

Three details to know first:

- `tunnel login` **always writes to the container user's home**,
  `/home/nonroot/.cloudflared` — not to the path you happen to pass with `-v`.
  So the setup mounts the host directory at `/home/nonroot/.cloudflared`, which
  is where both `cert.pem` and `<UUID>.json` land.
- The image runs as user `nonroot` (uid 65532, home `/home/nonroot`) and
  resolves its home from `/etc/passwd`, **not** from `$HOME`. Running the
  container with `--user <uid>` does **not** work for this: the uid has no
  passwd entry and cloudflared cannot find its home. Use the image's default
  user and make the mounted directory writable instead.
- The directory must be **writable by uid 65532**. On the host, make it
  world-writable for the setup (`chmod 777`) or `chown` it to 65532; the files
  end up owned by 65532 but world-readable.

From the directory that will hold the tunnel files (e.g. `./tunnel/`):

```bash
mkdir -p tunnel && chmod 777 tunnel

# 1) Authorize against the account (writes cert.pem) — interactive, open the URL
docker run -it --rm -v "$PWD/tunnel:/home/nonroot/.cloudflared" \
  cloudflare/cloudflared:latest tunnel login

# 2) Create the tunnel (writes <TUNNEL-UUID>.json)
docker run --rm -v "$PWD/tunnel:/home/nonroot/.cloudflared" \
  cloudflare/cloudflared:latest tunnel create my-server

# 3) Read back the tunnel UUID (it is also the credentials filename)
ls tunnel/*.json
docker run --rm -v "$PWD/tunnel:/home/nonroot/.cloudflared:ro" \
  cloudflare/cloudflared:latest tunnel list
```

> `tunnel login` writes the certificate **only after** you complete the browser
> flow. Keep the command running until it prints `You have successfully logged
> in`; if the container is removed early (or the mount is wrong) the cert is
> lost and the next command fails with *Cannot determine default origin
> certificate path* or *Cannot find a valid certificate*.

After this, `tunnel/` contains:

```
tunnel/
├── cert.pem            # account certificate (gitignore!)
└── <TUNNEL-UUID>.json  # tunnel credentials   (gitignore!)
```

Both files must be kept secret (`cert.pem` can manage **all** tunnels in the
account; the JSON can only run this one). Add them to `.gitignore`:

```gitignore
tunnel/cert.pem
tunnel/*.json
```

Restore the directory permissions for day-to-day use and keep the runtime
container read-only:

```bash
chmod 755 tunnel
```

Create the ingress rules file `tunnel/config.yml` (this one *is* versionable —
no secrets in it):

```yaml
ingress:
  - hostname: app.example.com
    service: http://caddy:80   # or https://caddy:443 — see the note below
  # Required catch-all:
  - service: http_status:404
```

Then run the connector as a service (see
[docker-deployment.md](docker-deployment.md#4-adding-a-cloudflare-tunnel) for a
full compose example). Note the mount target changes from the setup commands:
at runtime the same host directory is mounted at `/etc/cloudflared` and the
credentials file is passed explicitly with `--cred-file`:

```bash
docker run -d --name cloudflared --restart unless-stopped \
  -v "$PWD/tunnel:/etc/cloudflared:ro" \
  cloudflare/cloudflared:latest \
  tunnel --no-autoupdate \
    --config /etc/cloudflared/config.yml \
    --cred-file /etc/cloudflared/<TUNNEL-UUID>.json \
    run <TUNNEL-UUID>
```

The runtime container mounts the directory read-only: it reads the credentials
(world-readable) but cannot write. Only the one-time `login`/`create` steps need
write access to `tunnel/`.

> If your local service is Caddy itself and it redirects HTTP to HTTPS, point
> the ingress at the HTTPS listener (`https://localhost:443`) with
> `originRequest.originServerName` set to the hostname, or use
> `originRequest.noTLSVerify` for a self-signed origin. Aiming at the plain
> HTTP port of a redirecting origin causes a redirect loop through the edge.

In both options, note the tunnel UUID. Verify it with:

```bash
cloudflared tunnel list
```

## Step 2 — Point the plugin at the tunnel

Add the `tunnel` subdirective to the host's `cf_dns_manager` block. It takes the
tunnel UUID:

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE}
	}
}

*.example.com {
	tls {
		dns cloudflare {$CF_EXAMPLE}
	}

	@app host app.example.com
	handle @app {
		cf_dns_manager {
			host @app
			tunnel 8a7f3c2e-1234-4567-89ab-cdef01234567
			# force_adopt   # only if a CNAME already exists untagged
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
- skips public-IP detection for this host entirely.

### Rules for the `tunnel` subdirective

- `tunnel <uuid>` is **mutually exclusive** with `ip`, `ip6` and `proxied`. A
  tunnel host has no address plan and its CNAME is always proxied, so combining
  them is a configuration error at adapt time.
- The UUID must be a canonical `8-4-4-4-12` hexadecimal UUID.
- `force_adopt` is allowed and is how you adopt a CNAME that was created outside
  the plugin (for example by `cloudflared tunnel route dns`), which has no
  ownership tag.

## Step 3 — Verify

```bash
# DNS: the CNAME should resolve to Cloudflare's proxied addresses
dig +short app.example.com

# End to end: through the tunnel to your local service
curl -sS https://app.example.com/
```

Check the Caddy log for the reconcile action:

```
cf_dns_manager  created record  {"host": "app.example.com", "record_type": "CNAME", "content": "<uuid>.cfargotunnel.com", "proxied": true}
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

The tunnel itself is not touched by the plugin; remove it separately with
`cloudflared tunnel delete <name>` or from the dashboard.

## Reference

### The `tunnel` subdirective (plugin side)

| Subdirective | Value | Notes |
| --- | --- | --- |
| `tunnel` | `<uuid>` | Marks the host tunnel-backed. Reconciles a proxied CNAME to `<uuid>.cfargotunnel.com`. |
| `force_adopt` | — | Adopt an existing untagged CNAME (or an untagged A/AAAA blocking a switch). |

Notes:

- One directive manages one host; repeat the directive for more tunnel hosts.
  Multiple hosts can share the same tunnel UUID (add each to the ingress rules).
- The plugin only manages the DNS routing record. Adding hostnames to the DNS
  does not by itself route traffic: each hostname also needs a matching ingress
  rule in the tunnel configuration.

### The tunnel ingress configuration (`cloudflared` side)

The plugin manages DNS; `cloudflared` decides *where* each hostname goes. That
mapping lives in the tunnel configuration — the locally-managed `config.yml` or
the dashboard for a remotely-managed tunnel. Two hard rules:

1. **A config file must exist.** A locally-managed tunnel with an empty config
   fails: *"No configuration file was found"*. The `ingress` key is mandatory
   (it is the only thing you need — `tunnel:` and `credentials-file:` can come
   from the command line as shown above).
2. **The last rule must be a catch-all** (no `hostname`, no `path`). Validation
   fails otherwise: *"The last ingress rule must match all URLs"*.

#### Minimal config (all hostnames → one Caddy)

If Caddy serves every hostname (routing by `Host`), a single catch-all rule is
enough. This is the dynamic setup: **add a host to the plugin and it works —
no ingress changes.**

```yaml
ingress:
  - service: https://caddy:443
    originRequest:
      matchSNItoHost: true
```

#### HTTPS origin and `matchSNItoHost`

The default SNI when connecting to your HTTPS origin is the *service URL's*
hostname (`caddy`), which will not match a certificate issue for your public
hostnames. `matchSNItoHost: true` makes `cloudflared` send the request's `Host`
as SNI, so Caddy presents the matching certificate (e.g. its `*.example.com`
wildcard) for every host. This is what makes the catch-all work with HTTPS and
multiple hosts.

Alternatives, and why they are worse here:

| Option | Works with many hosts? | Notes |
| --- | --- | --- |
| `matchSNItoHost: true` | ✅ | SNI follows the request `Host`. **Preferred.** |
| `originServerName: <host>` | ❌ | A single fixed SNI; breaks the second host. Use only per-host rules. |
| `noTLSVerify: true` | ✅ | Disables origin verification; last resort only. |
| Origin over plain `http://` | ✅ | Only if the origin does not redirect HTTP→HTTPS. |

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
